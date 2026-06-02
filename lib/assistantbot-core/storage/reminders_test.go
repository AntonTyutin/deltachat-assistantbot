package storage

import (
	"context"
	"testing"
	"time"

	"github.com/AntonTyutin/assistantbot-core/reminders"
)

func TestGetReminderPendingOnly(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	defer store.Close()

	now := time.Now()
	reminder := Reminder{
		ID:          "r-get",
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
	got, ok, err := store.GetReminder(ctx, "chat-a", "r-get")
	if err != nil || !ok {
		t.Fatalf("get pending: ok=%v err=%v", ok, err)
	}
	if got.ID != reminder.ID {
		t.Fatalf("id = %q", got.ID)
	}
	if err := store.AdvanceReminder(ctx, "r-get", nil, 0); err != nil {
		t.Fatal(err)
	}
	_, ok, err = store.GetReminder(ctx, "chat-a", "r-get")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("delivered reminder should not be gettable")
	}
}

func TestAdvanceReminderReschedulesRecurring(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	defer store.Close()

	loc := time.UTC
	anchor := time.Date(2026, 6, 1, 9, 0, 0, 0, loc)
	rule := &reminders.RecurrenceRule{
		Interval:  1,
		Frequency: reminders.FrequencyDay,
		End:       reminders.RecurrenceEnd{Type: reminders.EndNever},
		Timezone:  "UTC",
	}
	reminder := Reminder{
		ID:          "r-recur",
		ChatID:      "chat-a",
		RequesterID: "user-1",
		DueAt:       anchor,
		AnchorAt:    anchor,
		Text:        "daily ping",
		Status:      ReminderPending,
		CreatedAt:   anchor,
		Recurrence:  rule,
	}
	if err := store.UpsertReminder(ctx, reminder); err != nil {
		t.Fatal(err)
	}

	next := time.Date(2026, 6, 2, 9, 0, 0, 0, loc)
	if err := store.AdvanceReminder(ctx, reminder.ID, &next, 1); err != nil {
		t.Fatal(err)
	}
	pending, err := store.ListReminders(ctx, "chat-a", ReminderPending)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 {
		t.Fatalf("expected 1 pending reminder, got %d", len(pending))
	}
	if !pending[0].DueAt.Equal(next) {
		t.Fatalf("due_at = %v want %v", pending[0].DueAt, next)
	}
	if pending[0].OccurrenceCount != 1 {
		t.Fatalf("occurrence_count = %d want 1", pending[0].OccurrenceCount)
	}
}

func TestAdvanceReminderCompletesSeries(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	defer store.Close()

	now := time.Now()
	reminder := Reminder{
		ID:          "r-done",
		ChatID:      "chat-a",
		RequesterID: "user-1",
		DueAt:       now,
		Text:        "once",
		Status:      ReminderPending,
		CreatedAt:   now,
	}
	if err := store.UpsertReminder(ctx, reminder); err != nil {
		t.Fatal(err)
	}
	if err := store.AdvanceReminder(ctx, reminder.ID, nil, 0); err != nil {
		t.Fatal(err)
	}
	pending, err := store.ListReminders(ctx, "chat-a", ReminderPending)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Fatalf("expected no pending reminders, got %d", len(pending))
	}
}

func TestReminderMigrationAddsColumns(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	defer store.Close()

	cols, err := store.reminderColumnNames(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"anchor_at", "recurrence_json", "occurrence_count"} {
		if !cols[name] {
			t.Fatalf("missing column %q", name)
		}
	}
}
