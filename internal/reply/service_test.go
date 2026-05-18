package reply

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"assistantbot/internal/deltachat"
	"assistantbot/internal/llm"
	"assistantbot/internal/storage"
)

func TestDecideReturnsFailureReplyOnLLMError(t *testing.T) {
	store, err := storage.Open(context.Background(), filepath.Join(t.TempDir(), "test.db"), "test-secret")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	client := taskFailClient{
		StaticClient: llm.StaticClient{Responses: map[string]json.RawMessage{}},
		failTask:     taskGenerateReply,
	}
	service := NewService(store, client, []string{"bot"}, nil, nil, nil)

	outbound, _, err := service.Decide(context.Background(), deltachat.Message{
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
	if outbound.ReplyToID != "m1" {
		t.Fatalf("expected reply_to_id m1, got %q", outbound.ReplyToID)
	}
}

func TestDecideKeepsReplyToInGroupChats(t *testing.T) {
	service, cleanup := testReplyService(t, `{"reply":"ok"}`)
	defer cleanup()

	outbound, _, err := service.Decide(context.Background(), deltachat.Message{
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
	service, cleanup := testReplyService(t, `{"reply":"Anton, ok"}`)
	defer cleanup()

	outbound, _, err := service.Decide(context.Background(), deltachat.Message{
		ID:       "m2",
		ChatID:   "chat-2",
		SenderID: "user-2",
		Sender:   "Anton Tyutin",
		Text:     "hello there",
		IsGroup:  false,
	}, storage.Topic{})
	if err != nil {
		t.Fatal(err)
	}
	if outbound == nil {
		t.Fatal("expected outbound reply")
	}
	if outbound.ReplyToID != "" {
		t.Fatalf("expected private chat reply_to_id to be empty, got %q", outbound.ReplyToID)
	}
	if outbound.Text != "ok" {
		t.Fatalf("expected private reply without direct address, got %q", outbound.Text)
	}
}

func TestDecideStripsDirectAddressInGroupReply(t *testing.T) {
	service, cleanup := testReplyService(t, `{"reply":"Anton, готово"}`)
	defer cleanup()

	if err := service.store.UpsertMessage(context.Background(), storage.Message{
		ID:         "bot-prev",
		ChatID:     "chat-3",
		SenderID:   "self",
		Sender:     "Me",
		Text:       "previous reply",
		IsGroup:    true,
		IsFromSelf: true,
		SentAt:     time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}

	outbound, _, err := service.Decide(context.Background(), deltachat.Message{
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
	if outbound.ReplyToID != "m3" {
		t.Fatalf("expected group reply_to_id m3, got %q", outbound.ReplyToID)
	}
}

func TestDecideStripsVocativePatternsInPrivateChat(t *testing.T) {
	cases := []struct {
		name     string
		sender   string
		reply    string
		expected string
	}{
		{name: "ru_leading", sender: "Илон Маск (elon@example.org)", reply: "Илон, как тебе такое!", expected: "как тебе такое!"},
		{name: "ru_trailing", sender: "Илон Маск (elon@example.org)", reply: "Как тебе такое, Илон!", expected: "Как тебе такое"},
		{name: "ru_middle", sender: "Илон Маск (elon@example.org)", reply: "Как тебе, Илон, такое!", expected: "Как тебе такое!"},
		{name: "en_leading", sender: "Elon Musk (elon@example.org)", reply: "Elon, how about this!", expected: "how about this!"},
		{name: "en_trailing", sender: "Elon Musk (elon@example.org)", reply: "How about this, Elon!", expected: "How about this"},
		{name: "en_middle", sender: "Elon Musk (elon@example.org)", reply: "How about, Elon, this!", expected: "How about this!"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			service, cleanup := testReplyService(t, `{"reply":"`+tc.reply+`"}`)
			defer cleanup()

			outbound, _, err := service.Decide(context.Background(), deltachat.Message{
				ID:       "m-v",
				ChatID:   "chat-v",
				SenderID: "user-v",
				Sender:   tc.sender,
				Text:     "hello",
				IsGroup:  false,
			}, storage.Topic{})
			if err != nil {
				t.Fatal(err)
			}
			if outbound == nil {
				t.Fatal("expected outbound reply")
			}
			if outbound.Text != tc.expected {
				t.Fatalf("expected %q, got %q", tc.expected, outbound.Text)
			}
		})
	}
}

func TestDecideStripsVocativePatternsInGroupReply(t *testing.T) {
	cases := []struct {
		name     string
		sender   string
		reply    string
		expected string
	}{
		{name: "ru_leading", sender: "Илон Маск (elon@example.org)", reply: "Илон, как тебе такое!", expected: "как тебе такое!"},
		{name: "ru_trailing", sender: "Илон Маск (elon@example.org)", reply: "Как тебе такое, Илон!", expected: "Как тебе такое"},
		{name: "ru_middle", sender: "Илон Маск (elon@example.org)", reply: "Как тебе, Илон, такое!", expected: "Как тебе такое!"},
		{name: "en_leading", sender: "Elon Musk (elon@example.org)", reply: "Elon, how about this!", expected: "how about this!"},
		{name: "en_trailing", sender: "Elon Musk (elon@example.org)", reply: "How about this, Elon!", expected: "How about this"},
		{name: "en_middle", sender: "Elon Musk (elon@example.org)", reply: "How about, Elon, this!", expected: "How about this!"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			service, cleanup := testReplyService(t, `{"reply":"`+tc.reply+`"}`)
			defer cleanup()

			if err := service.store.UpsertMessage(context.Background(), storage.Message{
				ID:         "bot-prev-v",
				ChatID:     "chat-vg",
				SenderID:   "self",
				Sender:     "Me",
				Text:       "prev",
				IsGroup:    true,
				IsFromSelf: true,
				SentAt:     time.Now().UTC(),
			}); err != nil {
				t.Fatal(err)
			}

			outbound, _, err := service.Decide(context.Background(), deltachat.Message{
				ID:        "m-vg",
				ChatID:    "chat-vg",
				SenderID:  "user-vg",
				Sender:    tc.sender,
				Text:      "reply",
				IsGroup:   true,
				ReplyToID: "bot-prev-v",
			}, storage.Topic{})
			if err != nil {
				t.Fatal(err)
			}
			if outbound == nil {
				t.Fatal("expected outbound reply")
			}
			if outbound.Text != tc.expected {
				t.Fatalf("expected %q, got %q", tc.expected, outbound.Text)
			}
		})
	}
}

func testReplyService(t *testing.T, replyJSON string) (*Service, func()) {
	t.Helper()

	store, err := storage.Open(context.Background(), filepath.Join(t.TempDir(), "test.db"), "test-secret")
	if err != nil {
		t.Fatal(err)
	}
	client := llm.StaticClient{Responses: map[string]json.RawMessage{
		taskGenerateReply: json.RawMessage(replyJSON),
	}}
	return NewService(store, client, []string{"bot"}, nil, nil, nil), func() {
		_ = store.Close()
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
