package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/AntonTyutin/assistantbot-core/llm"
	"github.com/AntonTyutin/assistantbot-core/storage"
)

func TestUpdateExistingTopicKeepsSeparateMessageEmbedding(t *testing.T) {
	ctx := context.Background()
	store := storage.OpenTestDB(t, "secret")
	defer store.Close()

	const (
		childText    = "unique child message"
		parentText   = "parent"
		topicSummary = "distinct topic summary after update"
	)
	childEmbText := llm.MessageEmbeddingText(childText, parentText)
	embedder := llm.TextKeyedEmbedder{
		Default: []float32{0, 0, 0},
		ByText: map[string][]float32{
			childEmbText: {1, 0, 0},
			topicSummary: {0, 1, 0},
		},
	}
	tm := NewTopicManager(store, llm.StaticClient{
		Responses: map[string]json.RawMessage{
			llm.TaskUpdateTopic: json.RawMessage(fmt.Sprintf(`{"summary":%q}`, topicSummary)),
		},
	}, embedder, Config{})

	topic := storage.Topic{
		ID:        "t1",
		ChatID:    "c1",
		Title:     "old",
		Summary:   "old summary",
		UpdatedAt: time.Now(),
	}
	if err := store.UpsertTopic(ctx, topic, []float32{0, 0, 1}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertMessage(ctx, storage.Message{
		ID: "m1", ChatID: "c1", SenderID: "u1", Text: parentText, TopicID: "t1", SentAt: time.Now(),
	}, []float32{0.5, 0.5, 0}); err != nil {
		t.Fatal(err)
	}

	child := storage.Message{
		ID: "m2", ChatID: "c1", SenderID: "u1", Text: childText, ReplyToID: "m1", SentAt: time.Now(),
	}
	resultTopic, messageEmb, err := tm.AssignTopic(ctx, child)
	if err != nil {
		t.Fatal(err)
	}
	if !vectorsEqual(messageEmb, []float32{1, 0, 0}) {
		t.Fatalf("message embedding = %v, want [1 0 0]", messageEmb)
	}

	nearestTopics, err := store.NearestTopics(ctx, "c1", []float32{0, 1, 0}, 1)
	if err != nil || len(nearestTopics) == 0 || nearestTopics[0].Topic.ID != resultTopic.ID {
		t.Fatalf("topic embedding not stored: %+v err=%v", nearestTopics, err)
	}

	child.TopicID = resultTopic.ID
	if err := store.UpsertMessage(ctx, child, messageEmb); err != nil {
		t.Fatal(err)
	}
	nearestMessages, err := store.NearestMessages(ctx, "c1", []float32{1, 0, 0}, 5)
	if err != nil || len(nearestMessages) == 0 || nearestMessages[0].Message.ID != "m2" {
		t.Fatalf("message embedding not stored: %+v err=%v", nearestMessages, err)
	}
}

func TestApplyTopicDecisionKeepsSeparateMessageEmbedding(t *testing.T) {
	ctx := context.Background()
	store := storage.OpenTestDB(t, "secret")
	defer store.Close()

	const (
		messageText  = "follow-up question"
		topicSummary = "classified topic summary"
	)
	messageEmb := []float32{1, 0, 0}
	topicEmb := []float32{0, 1, 0}
	embedder := llm.TextKeyedEmbedder{
		Default: []float32{0, 0, 0},
		ByText: map[string][]float32{
			messageText:  messageEmb,
			topicSummary: topicEmb,
		},
	}
	tm := NewTopicManager(store, llm.StaticClient{
		Responses: map[string]json.RawMessage{
			llm.TaskClassifyMessageTopic: classifyExistingTopicResponse("t1", "old title", topicSummary),
		},
	}, embedder, Config{})

	topic := storage.Topic{
		ID: "t1", ChatID: "c1", Title: "old title", Summary: "old", UpdatedAt: time.Now(),
	}
	if err := store.UpsertTopic(ctx, topic, []float32{0, 0, 1}); err != nil {
		t.Fatal(err)
	}

	message := storage.Message{
		ID: "m1", ChatID: "c1", SenderID: "u1", Text: messageText, SentAt: time.Now(),
	}
	resultTopic, gotMessageEmb, err := tm.AssignTopic(ctx, message)
	if err != nil {
		t.Fatal(err)
	}
	if !vectorsEqual(gotMessageEmb, messageEmb) {
		t.Fatalf("message embedding = %v, want %v", gotMessageEmb, messageEmb)
	}

	nearestTopics, err := store.NearestTopics(ctx, "c1", topicEmb, 1)
	if err != nil || len(nearestTopics) == 0 || nearestTopics[0].Topic.ID != resultTopic.ID {
		t.Fatalf("topic embedding not stored: %+v err=%v", nearestTopics, err)
	}
}

func vectorsEqual(a, b []float32) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
