package app

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"assistantbot/internal/deltachat"
	"assistantbot/internal/llm"
	"assistantbot/internal/memory"
	"assistantbot/internal/reply"
	"assistantbot/internal/storage"
)

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
		reply.NewService(store, llmClient, []string{"чатик"}, nil),
		slog.New(slog.NewTextHandler(io.Discard, nil)),
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

type recordingDeltaClient struct {
	sentID string
}

func (c *recordingDeltaClient) Run(context.Context, deltachat.EventHandler) error {
	return nil
}

func (c *recordingDeltaClient) SendText(context.Context, deltachat.OutboundMessage) (string, error) {
	return c.sentID, nil
}
