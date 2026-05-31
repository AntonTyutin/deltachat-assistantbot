package reply

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/AntonTyutin/assistantbot-core/llm"
	"github.com/AntonTyutin/assistantbot-core/llm/prompts"
	"github.com/AntonTyutin/assistantbot-core/memory"
	"github.com/AntonTyutin/assistantbot-core/storage"
	"github.com/AntonTyutin/assistantbot-core/transport"
)

func TestDecideReturnsFailureReplyOnLLMError(t *testing.T) {
	client := taskFailClient{
		StaticClient: llm.StaticClient{Responses: map[string]json.RawMessage{}},
		failTask:     llm.TaskGenerateChatReply,
	}
	store := storage.OpenTestDB(t, "test-secret")
	defer store.Close()
	pipeline := memory.NewPipeline(store, client, llm.StaticEmbedder{Vector: []float32{0.1, 0.2, 0.3}}, nil, prompts.MustTestRegistry(t), memory.Config{})
	service := NewService(pipeline, client, prompts.MustTestRegistry(t), []string{"bot"}, nil, nil, nil, 20)

	outbound, _, err := service.Decide(context.Background(), transport.Message{
		ID:       "m1",
		ChatID:   "chat-1",
		SenderID: "user-1",
		Text:     "bot, answer please",
		IsGroup:  true,
	}, storage.Topic{})
	if err != nil {
		t.Fatal(err)
	}
	if outbound == nil {
		t.Fatal("expected outbound reply")
	}
	if outbound.Text != failureReply {
		t.Fatalf("expected %q, got %q", failureReply, outbound.Text)
	}
}

func TestDecideKeepsReplyToInGroupChats(t *testing.T) {
	service, _, cleanup := testReplyService(t, `{"reply":"ok"}`)
	defer cleanup()

	outbound, _, err := service.Decide(context.Background(), transport.Message{
		ID:       "m1",
		ChatID:   "chat-1",
		SenderID: "user-1",
		Text:     "bot, answer please",
		IsGroup:  true,
	}, storage.Topic{})
	if err != nil {
		t.Fatal(err)
	}
	if outbound == nil {
		t.Fatal("expected outbound reply")
	}
	if outbound.ReplyToID != "m1" {
		t.Fatalf("expected group reply_to_id m1, got %q", outbound.ReplyToID)
	}
}

func TestDecideDoesNotReplyToInPrivateChats(t *testing.T) {
	service, _, cleanup := testReplyService(t, `{"reply":"Anton, ok"}`)
	defer cleanup()

	outbound, _, err := service.Decide(context.Background(), transport.Message{
		ID:       "m2",
		ChatID:   "chat-2",
		SenderID: "user-2",
		Sender:   "Anton",
		Text:     "hello",
		IsGroup:  false,
	}, storage.Topic{})
	if err != nil {
		t.Fatal(err)
	}
	if outbound == nil {
		t.Fatal("expected outbound reply")
	}
	if outbound.ReplyToID != "" {
		t.Fatalf("expected empty reply_to_id in private chat, got %q", outbound.ReplyToID)
	}
	if outbound.Text != "ok" {
		t.Fatalf("expected private reply without direct address, got %q", outbound.Text)
	}
}

func TestDecideStripsDirectAddressInGroupReply(t *testing.T) {
	service, store, cleanup := testReplyService(t, `{"reply":"Anton, готово"}`)
	defer cleanup()

	if err := store.UpsertMessage(context.Background(), storage.Message{
		ID:         "bot-prev",
		ChatID:     "chat-3",
		SenderID:   "self",
		Sender:     "Me",
		Text:       "previous reply",
		IsGroup:    true,
		IsFromSelf: true,
		SentAt:     time.Now().UTC(),
	}, []float32{0.1, 0.2, 0.3}); err != nil {
		t.Fatal(err)
	}

	outbound, _, err := service.Decide(context.Background(), transport.Message{
		ID:        "m3",
		ChatID:    "chat-3",
		SenderID:  "user-3",
		Sender:    "Anton Tyutin",
		Text:      "thanks",
		IsGroup:   true,
		ReplyToID: "bot-prev",
	}, storage.Topic{})
	if err != nil {
		t.Fatal(err)
	}
	if outbound == nil {
		t.Fatal("expected outbound reply")
	}
	if outbound.Text != "готово" {
		t.Fatalf("expected group reply without direct address, got %q", outbound.Text)
	}
}

func testReplyService(t *testing.T, replyJSON string) (*Service, *storage.Store, func()) {
	t.Helper()
	store := storage.OpenTestDB(t, "test-secret")
	client := llm.StaticClient{Responses: map[string]json.RawMessage{
		llm.TaskGenerateChatReply: json.RawMessage(replyJSON),
	}}
	pipeline := memory.NewPipeline(store, client, llm.StaticEmbedder{Vector: []float32{0.1, 0.2, 0.3}}, nil, prompts.MustTestRegistry(t), memory.Config{})
	service := NewService(pipeline, client, prompts.MustTestRegistry(t), []string{"bot"}, nil, nil, nil, 20)
	return service, store, func() { _ = store.Close() }
}

type taskFailClient struct {
	llm.StaticClient
	failTask string
}

func (c taskFailClient) CompleteJSON(ctx context.Context, task string, input any, schema string) (json.RawMessage, error) {
	if task == c.failTask {
		return nil, fmt.Errorf("%w for task %q: %w", llm.ErrAllModelsFailed, task, errors.New("429 Too Many Requests"))
	}
	return c.StaticClient.CompleteJSON(ctx, task, input, schema)
}
