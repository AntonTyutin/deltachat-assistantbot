package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	openai "github.com/sashabaranov/go-openai"

	"assistantbot/internal/deltachat"
	"assistantbot/internal/llm"
	"assistantbot/internal/memory"
	"assistantbot/internal/reply"
	"assistantbot/internal/storage"
)

func TestHandleMessageSendsFallbackWhenLLMExhausted(t *testing.T) {
	ctx := context.Background()
	store, err := storage.Open(ctx, filepath.Join(t.TempDir(), "test.db"), "test-secret")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	llmClient := taskFailClient{
		StaticClient: llm.StaticClient{Responses: map[string]json.RawMessage{
			"update_participant_profile": json.RawMessage(`{"names":{"chat":"Anton"}}`),
			"update_chat_topic":          json.RawMessage(`{"title":"date","summary":"date question","active_participants":["user-1"]}`),
		}},
		failTask: "generate_chat_reply",
	}
	delta := &recordingDeltaClient{sentID: "25"}
	app := New(
		delta,
		store,
		memory.NewPipeline(store, llmClient, nil),
		reply.NewService(store, llmClient, []string{"чатик"}, nil, nil, nil),
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		nil,
	)

	err = app.HandleEvent(ctx, deltachat.MessageEvent{
		Kind:      deltachat.MessageEventNew,
		Message:   botAddressedMessage(),
		ChatID:    "10",
		MessageID: "24",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(delta.sent) != 1 {
		t.Fatalf("expected one outbound message, got %d", len(delta.sent))
	}
	if delta.sent[0].Text != "😵" {
		t.Fatalf("expected fallback reply %q, got %q", "😵", delta.sent[0].Text)
	}
	if delta.sent[0].ReplyToID != "24" {
		t.Fatalf("expected reply_to_id 24, got %q", delta.sent[0].ReplyToID)
	}
}

func TestHandleEventSwallowsMemoryErrorWithoutReplyNotice(t *testing.T) {
	ctx := context.Background()
	store, err := storage.Open(ctx, filepath.Join(t.TempDir(), "test.db"), "test-secret")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	llmClient := taskFailClient{
		StaticClient: llm.StaticClient{Responses: map[string]json.RawMessage{}},
		failTask:     "update_chat_topic",
	}
	delta := &recordingDeltaClient{sentID: "25"}
	app := New(
		delta,
		store,
		memory.NewPipeline(store, llmClient, nil),
		reply.NewService(store, llmClient, []string{"чатик"}, nil, nil, nil),
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		nil,
	)

	err = app.HandleEvent(ctx, deltachat.MessageEvent{
		Kind:      deltachat.MessageEventNew,
		Message:   botAddressedMessage(),
		ChatID:    "10",
		MessageID: "24",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(delta.sent) != 0 {
		t.Fatalf("expected no outbound message, got %d", len(delta.sent))
	}
}

func TestHandleMessageStoresSentReplyInMemory(t *testing.T) {
	ctx := context.Background()
	store, err := storage.Open(ctx, filepath.Join(t.TempDir(), "test.db"), "test-secret")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	llmClient := llm.StaticClient{Responses: map[string]json.RawMessage{
		"update_participant_profile": json.RawMessage(`{"names":{"chat":"Anton"}}`),
		"update_chat_topic":          json.RawMessage(`{"title":"date","summary":"date question","active_participants":["user-1"]}`),
		"generate_chat_reply":        json.RawMessage(`{"reply":"Сегодня 27 апреля 2026 года."}`),
	}}
	delta := &recordingDeltaClient{sentID: "25"}
	app := New(
		delta,
		store,
		memory.NewPipeline(store, llmClient, nil),
		reply.NewService(store, llmClient, []string{"чатик"}, nil, nil, nil),
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		nil,
	)

	err = app.HandleMessage(ctx, deltachat.Message{
		ID:       "24",
		ChatID:   "10",
		SenderID: "user-1",
		Sender:   "Anton",
		Text:     "Чатик, какое сегодня число?",
		IsGroup:  true,
		SentAt:   time.Date(2026, 4, 27, 17, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}

	stored, ok, err := store.GetMessage(ctx, "10", "25")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected sent reply to be stored")
	}
	if !stored.IsFromSelf {
		t.Fatal("expected sent reply to be marked as self")
	}
	if stored.ReplyToID != "24" {
		t.Fatalf("expected reply_to_id 24, got %q", stored.ReplyToID)
	}
	if stored.Text != "Сегодня 27 апреля 2026 года." {
		t.Fatalf("unexpected stored reply text: %q", stored.Text)
	}
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

func (c taskFailClient) ChatWithTools(ctx context.Context, task string, messages []openai.ChatCompletionMessage, tools []openai.Tool, exec llm.ToolExecutorFunc) (string, error) {
	if task == c.failTask {
		return "", fmt.Errorf("%w for task %q: %w", llm.ErrAllModelsFailed, task, errors.New("429 Too Many Requests"))
	}
	return c.StaticClient.ChatWithTools(ctx, task, messages, tools, exec)
}

type recordingDeltaClient struct {
	sentID string
	sent   []deltachat.OutboundMessage
}

func (c *recordingDeltaClient) Run(context.Context, deltachat.EventHandler) error {
	return nil
}

func (c *recordingDeltaClient) SendText(_ context.Context, message deltachat.OutboundMessage) (string, error) {
	c.sent = append(c.sent, message)
	return c.sentID, nil
}

func botAddressedMessage() deltachat.Message {
	return deltachat.Message{
		ID:       "24",
		ChatID:   "10",
		SenderID: "user-1",
		Sender:   "Anton",
		Text:     "Чатик, какое сегодня число?",
		IsGroup:  true,
		SentAt:   time.Date(2026, 4, 27, 17, 0, 0, 0, time.UTC),
	}
}
