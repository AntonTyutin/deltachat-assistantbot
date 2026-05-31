package memory

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/AntonTyutin/assistantbot-core/llm"
	"github.com/AntonTyutin/assistantbot-core/llm/prompts"
	"github.com/AntonTyutin/assistantbot-core/storage"
)

func openTestPipeline(t *testing.T, client llm.Client) (*Pipeline, *storage.Store) {
	t.Helper()
	store := storage.OpenTestDB(t, "secret")
	pipeline := NewPipeline(store, client, llm.StaticEmbedder{Vector: []float32{0.1, 0.2, 0.3}}, nil, prompts.FixedTestRegistry(), Config{})
	return pipeline, store
}

func classifyNewTopicResponse(title, summary string) json.RawMessage {
	raw, _ := json.Marshal(map[string]any{
		"is_new_topic": true,
		"title":        title,
		"summary":      summary,
	})
	return raw
}

func classifyExistingTopicResponse(topicID, title, summary string) json.RawMessage {
	raw, _ := json.Marshal(map[string]any{
		"is_new_topic": false,
		"topic_id":     topicID,
		"title":        title,
		"summary":      summary,
	})
	return raw
}

func mustTopicID(t *testing.T, store *storage.Store, chatID, messageID string) string {
	t.Helper()
	topicID, ok, err := store.TopicIDForMessage(context.Background(), chatID, messageID)
	if err != nil || !ok || topicID == "" {
		t.Fatalf("topic id missing: topicID=%q ok=%v err=%v", topicID, ok, err)
	}
	return topicID
}
