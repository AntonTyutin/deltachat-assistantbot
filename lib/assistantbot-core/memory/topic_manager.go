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
	store    *storage.Store
	llm      llm.Client
	embedder llm.Embedder
	cfg      Config
}

func NewTopicManager(store *storage.Store, llmClient llm.Client, embedder llm.Embedder, cfg Config) *TopicManager {
	return &TopicManager{store: store, llm: llmClient, embedder: embedder, cfg: cfg.withDefaults()}
}

func (m *TopicManager) AssignTopic(ctx context.Context, message storage.Message) (storage.Topic, []float32, error) {
	now := time.Now()
	if topic, ok, err := m.store.TopicForReply(ctx, message.ChatID, message.ReplyToID); err != nil {
		return storage.Topic{}, nil, err
	} else if ok {
		return m.updateExistingTopic(ctx, topic, message, now)
	}

	embedding, err := m.embedMessage(ctx, message)
	if err != nil {
		return storage.Topic{}, nil, err
	}
	if embedding == nil {
		return storage.Topic{}, nil, nil
	}

	nearestMessages, err := m.store.NearestMessages(ctx, message.ChatID, embedding, m.cfg.TopicKNNMessages)
	if err != nil {
		return storage.Topic{}, nil, err
	}
	nearestTopics, err := m.store.NearestTopics(ctx, message.ChatID, embedding, m.cfg.TopicKNNTopics)
	if err != nil {
		return storage.Topic{}, nil, err
	}

	raw, err := m.llm.CompleteJSON(ctx, llm.TaskClassifyMessageTopic, map[string]any{
		"message":          message,
		"nearest_messages": nearestMessages,
		"nearest_topics":   nearestTopics,
	}, `{"is_new_topic":true,"topic_id":"string","title":"string","summary":"string","decisions":["string"],"open_questions":["string"]}`)
	if err != nil {
		return m.fallbackTopic(ctx, message, embedding, now)
	}
	var decision topicClassification
	if err := json.Unmarshal(raw, &decision); err != nil {
		return m.fallbackTopic(ctx, message, embedding, now)
	}

	if !decision.IsNewTopic && strings.TrimSpace(decision.TopicID) != "" {
		topic, ok, err := m.store.GetTopic(ctx, decision.TopicID)
		if err != nil {
			return storage.Topic{}, nil, err
		}
		if ok {
			return m.applyTopicDecision(ctx, topic, message, decision, embedding, now)
		}
	}
	return m.createTopic(ctx, message, decision, embedding, now)
}

func (m *TopicManager) UpdateFromMessage(ctx context.Context, message storage.Message) (storage.Topic, error) {
	topic, embedding, err := m.AssignTopic(ctx, message)
	if err != nil {
		return storage.Topic{}, err
	}
	message.TopicID = topic.ID
	if err := m.store.UpsertMessage(ctx, message, embedding); err != nil {
		return storage.Topic{}, err
	}
	return topic, nil
}

func (m *TopicManager) PatchFromMessageEdit(ctx context.Context, topicID string, message, previous storage.Message) (storage.Topic, error) {
	topic, ok, err := m.store.GetTopic(ctx, topicID)
	if err != nil {
		return storage.Topic{}, err
	}
	if !ok {
		return storage.Topic{}, nil
	}
	return m.applyTopicUpdate(ctx, topic, message, &previous, "edit", time.Now())
}

func (m *TopicManager) PatchFromMessageDelete(ctx context.Context, topicID string, deleted storage.Message) (storage.Topic, error) {
	topic, ok, err := m.store.GetTopic(ctx, topicID)
	if err != nil {
		return storage.Topic{}, err
	}
	if !ok {
		return storage.Topic{}, nil
	}
	return m.applyTopicUpdate(ctx, topic, storage.Message{}, &deleted, "delete", time.Now())
}

type topicClassification struct {
	IsNewTopic    bool     `json:"is_new_topic"`
	TopicID       string   `json:"topic_id"`
	Title         string   `json:"title"`
	Summary       string   `json:"summary"`
	Decisions     []string `json:"decisions"`
	OpenQuestions []string `json:"open_questions"`
}

func (m *TopicManager) createTopic(ctx context.Context, message storage.Message, decision topicClassification, embedding []float32, now time.Time) (storage.Topic, []float32, error) {
	topic := storage.Topic{
		ID:                 storage.NewTopicID(message.ChatID, now),
		ChatID:             message.ChatID,
		Title:              titleFromDecision(decision, message.Text),
		Summary:            summaryFromDecision(decision, message.Text),
		Decisions:          decision.Decisions,
		OpenQuestions:      decision.OpenQuestions,
		ActiveParticipants: participantIDs(message.SenderID),
		UpdatedAt:          now,
	}
	if err := m.store.UpsertTopic(ctx, topic, embedding); err != nil {
		return storage.Topic{}, nil, err
	}
	return topic, embedding, nil
}

func (m *TopicManager) updateExistingTopic(ctx context.Context, topic storage.Topic, message storage.Message, now time.Time) (storage.Topic, []float32, error) {
	updated, err := m.applyTopicUpdate(ctx, topic, message, nil, "", now)
	if err != nil {
		return storage.Topic{}, nil, err
	}
	messageEmbedding, err := m.embedMessage(ctx, message)
	if err != nil {
		return updated, nil, err
	}
	return updated, messageEmbedding, nil
}

func (m *TopicManager) applyTopicUpdate(ctx context.Context, topic storage.Topic, message storage.Message, previous *storage.Message, change string, now time.Time) (storage.Topic, error) {
	recent, err := m.store.RecentMessages(ctx, topic.ChatID, 20)
	if err != nil {
		return storage.Topic{}, err
	}
	payload := map[string]any{
		"topic":   topic,
		"message": message,
		"recent":  recent,
	}
	if previous != nil {
		payload["previous_message"] = *previous
	}
	if change != "" {
		payload["change"] = change
		payload["correction"] = true
	}
	raw, err := m.llm.CompleteJSON(ctx, llm.TaskUpdateTopic, payload,
		`{"title":"string","summary":"string","decisions":["string"],"open_questions":["string"],"active_participants":["id"]}`)
	if err == nil {
		var patch topicPatch
		if json.Unmarshal(raw, &patch) == nil {
			mergeTopic(&topic, patch)
		}
	}
	senderID := message.SenderID
	if senderID == "" && previous != nil {
		senderID = previous.SenderID
	}
	topic = attachSenderToTopic(topic, senderID, now)
	topicEmbedding, err := m.embedTopicSummary(ctx, topic.Summary)
	if err != nil {
		return topic, m.store.UpsertTopic(ctx, topic, nil)
	}
	return topic, m.store.UpsertTopic(ctx, topic, topicEmbedding)
}

func (m *TopicManager) applyTopicDecision(ctx context.Context, topic storage.Topic, message storage.Message, decision topicClassification, messageEmbedding []float32, now time.Time) (storage.Topic, []float32, error) {
	if decision.Title != "" {
		topic.Title = decision.Title
	}
	if decision.Summary != "" {
		topic.Summary = decision.Summary
	}
	if decision.Decisions != nil {
		topic.Decisions = decision.Decisions
	}
	if decision.OpenQuestions != nil {
		topic.OpenQuestions = decision.OpenQuestions
	}
	topic = attachSenderToTopic(topic, message.SenderID, now)
	topicEmbedding, err := m.embedTopicSummary(ctx, topic.Summary)
	if err != nil {
		return topic, messageEmbedding, m.store.UpsertTopic(ctx, topic, nil)
	}
	return topic, messageEmbedding, m.store.UpsertTopic(ctx, topic, topicEmbedding)
}

func (m *TopicManager) embedMessage(ctx context.Context, message storage.Message) ([]float32, error) {
	parentText := ""
	if message.ReplyToID != "" {
		if parent, ok, err := m.store.GetMessage(ctx, message.ChatID, message.ReplyToID); err != nil {
			return nil, err
		} else if ok {
			parentText = parent.Text
		}
	}
	vectors, err := m.embedder.Embed(ctx, llm.MessageEmbeddingText(message.Text, parentText))
	if err != nil {
		return nil, err
	}
	if len(vectors) == 0 {
		return nil, nil
	}
	return vectors[0], nil
}

func (m *TopicManager) embedTopicSummary(ctx context.Context, summary string) ([]float32, error) {
	summary = strings.TrimSpace(summary)
	if summary == "" {
		return nil, nil
	}
	vectors, err := m.embedder.Embed(ctx, summary)
	if err != nil {
		return nil, err
	}
	if len(vectors) == 0 {
		return nil, nil
	}
	return vectors[0], nil
}

func (m *TopicManager) fallbackTopic(ctx context.Context, message storage.Message, embedding []float32, now time.Time) (storage.Topic, []float32, error) {
	topic := storage.Topic{
		ID:                 storage.NewTopicID(message.ChatID, now),
		ChatID:             message.ChatID,
		Title:              titleFromText(message.Text),
		Summary:            message.Text,
		ActiveParticipants: participantIDs(message.SenderID),
		UpdatedAt:          now,
	}
	return topic, embedding, m.store.UpsertTopic(ctx, topic, embedding)
}

func attachSenderToTopic(topic storage.Topic, senderID string, updatedAt time.Time) storage.Topic {
	if senderID != "" && !containsString(topic.ActiveParticipants, senderID) {
		topic.ActiveParticipants = append(topic.ActiveParticipants, senderID)
	}
	topic.UpdatedAt = updatedAt
	return topic
}

func participantIDs(senderID string) []string {
	if senderID == "" {
		return nil
	}
	return []string{senderID}
}

func titleFromDecision(decision topicClassification, fallback string) string {
	if strings.TrimSpace(decision.Title) != "" {
		return strings.TrimSpace(decision.Title)
	}
	return titleFromText(fallback)
}

func summaryFromDecision(decision topicClassification, fallback string) string {
	if strings.TrimSpace(decision.Summary) != "" {
		return strings.TrimSpace(decision.Summary)
	}
	return strings.TrimSpace(fallback)
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

func titleFromText(text string) string {
	text = strings.TrimSpace(text)
	runes := []rune(text)
	if len(runes) > 48 {
		return string(runes[:48])
	}
	if text == "" {
		return "New topic"
	}
	return text
}
