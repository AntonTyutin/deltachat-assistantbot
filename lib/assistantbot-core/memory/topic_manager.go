package memory

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/AntonTyutin/assistantbot-core/llm"
	"github.com/AntonTyutin/assistantbot-core/storage"
)

type TopicManager struct {
	store *storage.Store
	llm   llm.Client
}

func NewTopicManager(store *storage.Store, llmClient llm.Client) *TopicManager {
	return &TopicManager{store: store, llm: llmClient}
}

// ResolveTopicWithoutLLM picks or creates a topic using storage heuristics only (no LLM).
func (m *TopicManager) ResolveTopicWithoutLLM(ctx context.Context, message storage.Message) (storage.Topic, error) {
	now := time.Now()
	if topic, ok, err := m.store.TopicForReply(ctx, message.ChatID, message.ReplyToID); err != nil {
		return storage.Topic{}, err
	} else if ok {
		return attachSenderToTopic(topic, message.SenderID, now), nil
	}

	topics, err := m.store.ListTopics(ctx, message.ChatID, 5)
	if err != nil {
		return storage.Topic{}, err
	}
	if matched, ok := matchTopic(message.Text, topics); ok {
		return attachSenderToTopic(matched, message.SenderID, now), nil
	}

	topic := storage.Topic{
		ID:                 storage.NewTopicID(message.ChatID, now),
		ChatID:             message.ChatID,
		Title:              titleFromText(message.Text),
		Summary:            message.Text,
		ActiveParticipants: []string{},
		UpdatedAt:          now,
	}
	return attachSenderToTopic(topic, message.SenderID, now), nil
}

func attachSenderToTopic(topic storage.Topic, senderID string, updatedAt time.Time) storage.Topic {
	if senderID != "" && !containsString(topic.ActiveParticipants, senderID) {
		topic.ActiveParticipants = append(topic.ActiveParticipants, senderID)
	}
	topic.UpdatedAt = updatedAt
	return topic
}

func (m *TopicManager) UpdateFromMessage(ctx context.Context, message storage.Message) (storage.Topic, error) {
	now := time.Now()
	if topic, ok, err := m.store.TopicForReply(ctx, message.ChatID, message.ReplyToID); err != nil {
		return storage.Topic{}, err
	} else if ok {
		return m.patchTopic(ctx, topic, message)
	}

	topics, err := m.store.ListTopics(ctx, message.ChatID, 5)
	if err != nil {
		return storage.Topic{}, err
	}
	if matched, ok := matchTopic(message.Text, topics); ok {
		return m.patchTopic(ctx, matched, message)
	}

	topic := storage.Topic{
		ID:                 storage.NewTopicID(message.ChatID, now),
		ChatID:             message.ChatID,
		Title:              titleFromText(message.Text),
		Summary:            message.Text,
		ActiveParticipants: []string{message.SenderID},
		UpdatedAt:          now,
	}
	return m.patchTopic(ctx, topic, message)
}

func (m *TopicManager) RebuildTopic(ctx context.Context, topicID string) (storage.Topic, error) {
	topic, ok, err := m.store.GetTopic(ctx, topicID)
	if err != nil || !ok {
		return topic, err
	}
	messages, err := m.store.TopicMessages(ctx, topic.ChatID, topicID)
	if err != nil {
		return storage.Topic{}, err
	}
	rebuilt := rebuildTopicFallback(topic, messages)
	raw, err := m.llm.CompleteJSON(ctx, llm.TaskRebuildTopic, map[string]any{
		"topic":    topic,
		"messages": messages,
	}, `{"title":"string","summary":"string","decisions":["string"],"open_questions":["string"],"active_participants":["id"]}`)
	if err == nil {
		var patch topicPatch
		if json.Unmarshal(raw, &patch) == nil {
			mergeTopic(&rebuilt, patch)
		}
	}
	rebuilt.UpdatedAt = time.Now()
	return rebuilt, m.store.UpsertTopic(ctx, rebuilt)
}

func (m *TopicManager) patchTopic(ctx context.Context, topic storage.Topic, message storage.Message) (storage.Topic, error) {
	recent, err := m.store.RecentMessages(ctx, message.ChatID, 20)
	if err != nil {
		return storage.Topic{}, err
	}
	raw, err := m.llm.CompleteJSON(ctx, llm.TaskUpdateTopic, map[string]any{
		"topic":   topic,
		"message": message,
		"recent":  recent,
	}, `{"title":"string","summary":"string","decisions":["string"],"open_questions":["string"],"active_participants":["id"]}`)
	if err == nil {
		var patch topicPatch
		if json.Unmarshal(raw, &patch) == nil {
			mergeTopic(&topic, patch)
		}
	}
	if !containsString(topic.ActiveParticipants, message.SenderID) && message.SenderID != "" {
		topic.ActiveParticipants = append(topic.ActiveParticipants, message.SenderID)
	}
	topic.UpdatedAt = time.Now()
	return topic, m.store.UpsertTopic(ctx, topic)
}

func mergeTopic(topic *storage.Topic, patch topicPatch) {
	if patch.Title != "" {
		topic.Title = patch.Title
	}
	if patch.Summary != "" {
		topic.Summary = patch.Summary
	}
	if patch.Decisions != nil {
		topic.Decisions = patch.Decisions
	}
	if patch.OpenQuestions != nil {
		topic.OpenQuestions = patch.OpenQuestions
	}
	for _, participant := range patch.ActiveParticipants {
		if participant != "" && !containsString(topic.ActiveParticipants, participant) {
			topic.ActiveParticipants = append(topic.ActiveParticipants, participant)
		}
	}
}

func rebuildTopicFallback(topic storage.Topic, messages []storage.Message) storage.Topic {
	topic.Decisions = nil
	topic.OpenQuestions = nil
	topic.ActiveParticipants = nil
	if len(messages) == 0 {
		topic.Summary = ""
		return topic
	}
	topic.Title = titleFromText(messages[0].Text)
	var summaries []string
	for _, message := range messages {
		if strings.TrimSpace(message.Text) != "" {
			summaries = append(summaries, message.Text)
		}
		if message.SenderID != "" && !containsString(topic.ActiveParticipants, message.SenderID) {
			topic.ActiveParticipants = append(topic.ActiveParticipants, message.SenderID)
		}
	}
	topic.Summary = strings.Join(summaries, "\n")
	return topic
}

func matchTopic(text string, topics []storage.Topic) (storage.Topic, bool) {
	lower := strings.ToLower(text)
	for _, topic := range topics {
		title := strings.ToLower(topic.Title)
		if title != "" && strings.Contains(lower, title) {
			return topic, true
		}
		for _, word := range strings.Fields(title) {
			if len([]rune(word)) > 4 && strings.Contains(lower, word) {
				return topic, true
			}
		}
	}
	return storage.Topic{}, false
}

func titleFromText(text string) string {
	text = strings.TrimSpace(text)
	runes := []rune(text)
	if len(runes) > 48 {
		return string(runes[:48])
	}
	if text == "" {
		return "Новая тема"
	}
	return text
}
