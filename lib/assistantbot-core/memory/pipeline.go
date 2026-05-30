package memory

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	openai "github.com/sashabaranov/go-openai"

	"github.com/AntonTyutin/assistantbot-core/llm"
	"github.com/AntonTyutin/assistantbot-core/storage"
	"github.com/AntonTyutin/assistantbot-core/transport"
)

type Pipeline struct {
	store *storage.Store
	llm   llm.Client
	mcp   mcpToolRuntime

	profiles  *ProfileManager
	topics    *TopicManager
	locations *LocationResolver
}

type mcpToolRuntime interface {
	OpenAITools() []openai.Tool
	ExecuteTool(ctx context.Context, prefixedName string, argumentsJSON string) (string, error)
	SystemPromptAppend() string
}

func NewPipeline(store *storage.Store, llmClient llm.Client, mcp mcpToolRuntime, promptReg promptRegistry) *Pipeline {
	p := &Pipeline{store: store, llm: llmClient, mcp: mcp}
	p.profiles = NewProfileManager(store, llmClient)
	p.topics = NewTopicManager(store, llmClient)
	p.locations = NewLocationResolver(store, llmClient, mcp, promptReg)
	return p
}

type promptRegistry interface {
	SystemPromptForMCP(mcpAppend string) string
}

func (p *Pipeline) ProcessMessage(ctx context.Context, message transport.Message) (storage.Topic, error) {
	return p.ProcessNewMessage(ctx, message)
}

// PrepareForReply persists the inbound message and ensures a topic is attached for reply generation.
// It does not run LLM profile/topic updates; use ProcessMessageUpdate for that in the background.
func (p *Pipeline) PrepareForReply(ctx context.Context, message transport.Message) (storage.Topic, error) {
	storedMessage := storage.Message(message)
	if err := p.store.UpsertMessage(ctx, storedMessage); err != nil {
		return storage.Topic{}, err
	}
	topicID, hasTopic, err := p.store.TopicIDForMessage(ctx, message.ChatID, message.ID)
	if err != nil {
		return storage.Topic{}, err
	}
	if hasTopic {
		topic, ok, err := p.store.GetTopic(ctx, topicID)
		if err != nil {
			return storage.Topic{}, err
		}
		if !ok {
			return storage.Topic{}, errors.New("topic not found")
		}
		return topic, nil
	}
	topic, err := p.topics.ResolveTopicWithoutLLM(ctx, storedMessage)
	if err != nil {
		return storage.Topic{}, err
	}
	if err := p.store.UpsertTopic(ctx, topic); err != nil {
		return storage.Topic{}, err
	}
	if err := p.store.AttachMessageToTopic(ctx, message.ChatID, message.ID, topic.ID); err != nil {
		return storage.Topic{}, err
	}
	return topic, nil
}

func (p *Pipeline) ProcessNewMessage(ctx context.Context, message transport.Message) (storage.Topic, error) {
	storedMessage := storage.Message(message)
	if err := p.store.UpsertMessage(ctx, storedMessage); err != nil {
		return storage.Topic{}, err
	}
	if !message.IsFromSelf {
		if err := p.profiles.UpdateFromMessage(ctx, storedMessage); err != nil {
			return storage.Topic{}, err
		}
	}
	topic, err := p.topics.UpdateFromMessage(ctx, storedMessage)
	if err != nil {
		return storage.Topic{}, err
	}
	return topic, p.store.AttachMessageToTopic(ctx, message.ChatID, message.ID, topic.ID)
}

func (p *Pipeline) ProcessMessageUpdate(ctx context.Context, message transport.Message) error {
	storedMessage := storage.Message(message)
	oldMessage, hadOldMessage, err := p.store.GetMessage(ctx, message.ChatID, message.ID)
	if err != nil {
		return err
	}
	topicID, hasTopic, err := p.store.TopicIDForMessage(ctx, message.ChatID, message.ID)
	if err != nil {
		return err
	}
	if !hasTopic {
		topic, err := p.ProcessNewMessage(ctx, message)
		if err != nil {
			return err
		}
		topicID = topic.ID
		hasTopic = true
	} else if err := p.store.UpsertMessage(ctx, storedMessage); err != nil {
		return err
	}
	if hasTopic {
		if _, err := p.RebuildTopic(ctx, topicID); err != nil {
			return err
		}
	}
	if !storedMessage.IsFromSelf {
		if err := p.RebuildParticipantProfile(ctx, storedMessage.SenderID); err != nil {
			return err
		}
	}
	if hadOldMessage && oldMessage.SenderID != "" && oldMessage.SenderID != storedMessage.SenderID && !oldMessage.IsFromSelf {
		return p.RebuildParticipantProfile(ctx, oldMessage.SenderID)
	}
	return nil
}

func (p *Pipeline) ProcessMessageDelete(ctx context.Context, chatID, messageID string) error {
	oldMessage, ok, err := p.store.GetMessage(ctx, chatID, messageID)
	if err != nil {
		return err
	}
	topicID, hasTopic, err := p.store.TopicIDForMessage(ctx, chatID, messageID)
	if err != nil {
		return err
	}
	if err := p.store.DeleteMessage(ctx, chatID, messageID); err != nil {
		return err
	}
	if hasTopic {
		if _, err := p.RebuildTopic(ctx, topicID); err != nil {
			return err
		}
	}
	if ok && oldMessage.SenderID != "" && !oldMessage.IsFromSelf {
		return p.RebuildParticipantProfile(ctx, oldMessage.SenderID)
	}
	return nil
}

func (p *Pipeline) RebuildTopic(ctx context.Context, topicID string) (storage.Topic, error) {
	return p.topics.RebuildTopic(ctx, topicID)
}

func (p *Pipeline) RebuildParticipantProfile(ctx context.Context, participantID string) error {
	return p.profiles.RebuildParticipantProfile(ctx, participantID)
}

func (p *Pipeline) UpdateDailySummary(ctx context.Context, chatID string, date time.Time) error {
	recent, err := p.store.RecentMessages(ctx, chatID, 20)
	if err != nil {
		return err
	}
	topics, err := p.store.ListTopics(ctx, chatID, 20)
	if err != nil {
		return err
	}
	raw, err := p.llm.CompleteJSON(ctx, llm.TaskDailySummary, map[string]any{
		"chat_id": chatID,
		"date":    date.Format("2006-01-02"),
		"recent":  recent,
		"topics":  topics,
	}, `{"summary":"string"}`)
	if err != nil {
		return err
	}
	var response struct {
		Summary string `json:"summary"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return err
	}
	return p.store.SaveDailySummary(ctx, storage.DailySummary{
		ChatID:    chatID,
		Date:      date.Format("2006-01-02"),
		Summary:   response.Summary,
		CreatedAt: time.Now(),
	})
}

func (p *Pipeline) UpdateParticipantLocationFromCoordinates(ctx context.Context, participantID string, latitude, longitude float64) error {
	return p.locations.UpdateParticipantLocationFromCoordinates(ctx, participantID, latitude, longitude)
}

type profilePatch struct {
	City      string            `json:"city"`
	Address   string            `json:"address"`
	Style     string            `json:"style"`
	Verbosity string            `json:"verbosity"`
	Expertise map[string]string `json:"expertise"`
	Interests []string          `json:"interests"`
}

type profileRebuild struct {
	Names     map[string]string `json:"names"`
	City      string            `json:"city"`
	Address   string            `json:"address"`
	Style     string            `json:"style"`
	Verbosity string            `json:"verbosity"`
	Expertise map[string]string `json:"expertise"`
	Interests []string          `json:"interests"`
}

type topicPatch struct {
	Title              string   `json:"title"`
	Summary            string   `json:"summary"`
	Decisions          []string `json:"decisions"`
	OpenQuestions      []string `json:"open_questions"`
	ActiveParticipants []string `json:"active_participants"`
}
