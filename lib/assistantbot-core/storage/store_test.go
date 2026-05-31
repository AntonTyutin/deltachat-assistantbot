package storage

import (
	"context"
	"testing"
	"time"
)

func TestRecentMessagesKeepsRollingWindow(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	defer store.Close()

	for i := 0; i < 25; i++ {
		err := store.UpsertMessage(ctx, Message{
			ID:       string(rune('a' + i)),
			ChatID:   "chat-1",
			SenderID: "user-1",
			Text:     "hello",
			SentAt:   time.Date(2026, 4, 26, 12, i, 0, 0, time.UTC),
		}, []float32{0.1, 0.2, 0.3})
		if err != nil {
			t.Fatalf("upsert message: %v", err)
		}
	}

	recent, err := store.RecentMessages(ctx, "chat-1", 30)
	if err != nil {
		t.Fatalf("recent messages: %v", err)
	}
	if len(recent) != 20 {
		t.Fatalf("expected 20 messages, got %d", len(recent))
	}
	if recent[0].ID != "f" {
		t.Fatalf("expected oldest retained message f, got %q", recent[0].ID)
	}
}

func TestChatNamesAreScopedPerChat(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	defer store.Close()

	if err := store.SetChatName(ctx, "family", "user-1", "Лёша"); err != nil {
		t.Fatal(err)
	}
	if err := store.SetChatName(ctx, "work", "user-1", "Алексей"); err != nil {
		t.Fatal(err)
	}

	family, ok, err := store.ChatName(ctx, "family", "user-1")
	if err != nil || !ok {
		t.Fatalf("family name missing: ok=%v err=%v", ok, err)
	}
	work, ok, err := store.ChatName(ctx, "work", "user-1")
	if err != nil || !ok {
		t.Fatalf("work name missing: ok=%v err=%v", ok, err)
	}
	if family == work {
		t.Fatalf("expected different per-chat names, got %q", family)
	}
}

func TestUpsertMessageStoresTopicID(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	defer store.Close()

	message := Message{
		ID:       "msg-1",
		ChatID:   "chat-1",
		SenderID: "user-1",
		Text:     "hello",
		TopicID:  "topic-1",
		SentAt:   time.Now(),
	}
	if err := store.UpsertMessage(ctx, message, []float32{0.1, 0.2, 0.3}); err != nil {
		t.Fatal(err)
	}
	topicID, ok, err := store.TopicIDForMessage(ctx, "chat-1", "msg-1")
	if err != nil || !ok || topicID != "topic-1" {
		t.Fatalf("topic id missing: topicID=%q ok=%v err=%v", topicID, ok, err)
	}
}

func TestDeleteMessageRemovesMessage(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	defer store.Close()

	message := Message{
		ID:       "msg-1",
		ChatID:   "chat-1",
		SenderID: "user-1",
		Text:     "hello",
		TopicID:  "topic-1",
		SentAt:   time.Now(),
	}
	if err := store.UpsertMessage(ctx, message, []float32{0.1, 0.2, 0.3}); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteMessage(ctx, "chat-1", "msg-1"); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := store.GetMessage(ctx, "chat-1", "msg-1"); err != nil || ok {
		t.Fatalf("message should be deleted: ok=%v err=%v", ok, err)
	}
}

func openTestStore(t *testing.T) *Store {
	t.Helper()
	return OpenTestDB(t, "test-secret")
}
