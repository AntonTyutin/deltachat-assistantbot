package storage

import (
	"context"
	"testing"
	"time"
)

func TestCancelReminderRequiresMatchingChat(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	defer store.Close()

	now := time.Now()
	reminder := Reminder{
		ID:          "r1",
		ChatID:      "chat-a",
		RequesterID: "user-1",
		DueAt:       now.Add(time.Hour),
		Text:        "ping",
		Status:      ReminderPending,
		CreatedAt:   now,
	}
	if err := store.UpsertReminder(ctx, reminder); err != nil {
		t.Fatal(err)
	}

	if err := store.CancelReminder(ctx, "chat-b", "r1"); err == nil {
		t.Fatal("expected error when chat_id does not match")
	}
	pending, err := store.ListReminders(ctx, "chat-a", ReminderPending)
	if err != nil || len(pending) != 1 {
		t.Fatalf("reminder should remain pending: len=%d err=%v", len(pending), err)
	}

	if err := store.CancelReminder(ctx, "chat-a", "r1"); err != nil {
		t.Fatal(err)
	}
	pending, err = store.ListReminders(ctx, "chat-a", ReminderPending)
	if err != nil || len(pending) != 0 {
		t.Fatalf("reminder should be cancelled: len=%d err=%v", len(pending), err)
	}
}
