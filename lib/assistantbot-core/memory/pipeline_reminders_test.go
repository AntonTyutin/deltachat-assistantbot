package memory

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/AntonTyutin/assistantbot-core/llm"
	"github.com/AntonTyutin/assistantbot-core/llm/prompts"
	"github.com/AntonTyutin/assistantbot-core/reminders"
	"github.com/AntonTyutin/assistantbot-core/storage"
)

func TestSetRecurringReminder(t *testing.T) {
	ctx := context.Background()
	store := storage.OpenTestDB(t, "secret")
	defer store.Close()
	registry := NewToolRegistry(store, llm.StaticEmbedder{Vector: []float32{0.1, 0.2, 0.3}}, nil)

	if err := store.UpsertProfile(ctx, storage.ParticipantProfile{
		ID:       "user-1",
		Timezone: "UTC",
	}); err != nil {
		t.Fatal(err)
	}

	args, _ := json.Marshal(map[string]any{
		"text":   "standup",
		"due_at": "2026-06-03T09:00:00Z",
		"recurrence": map[string]any{
			"interval":  1,
			"frequency": "week",
			"weekdays":  []string{"tuesday"},
			"end":       map[string]any{"type": "never"},
		},
	})
	out, err := registry.ExecuteTool(ctx, "chat-1", "user-1", "memory_set_reminder", string(args))
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Recurring bool `json:"recurring"`
	}
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatal(err)
	}
	if !result.Recurring {
		t.Fatalf("expected recurring=true in %s", out)
	}

	pending, err := store.ListReminders(ctx, "chat-1", storage.ReminderPending)
	if err != nil || len(pending) != 1 {
		t.Fatalf("pending reminders: len=%d err=%v", len(pending), err)
	}
	if pending[0].Recurrence == nil {
		t.Fatal("expected recurrence on stored reminder")
	}
	if pending[0].Recurrence.Frequency != reminders.FrequencyWeek {
		t.Fatalf("frequency = %q", pending[0].Recurrence.Frequency)
	}
	if !pending[0].AnchorAt.Equal(pending[0].DueAt) {
		t.Fatal("anchor_at should match first due_at")
	}
}

func TestSetReminderRejectsInvalidRecurrence(t *testing.T) {
	ctx := context.Background()
	store := storage.OpenTestDB(t, "secret")
	defer store.Close()
	registry := NewToolRegistry(store, llm.StaticEmbedder{Vector: []float32{0.1, 0.2, 0.3}}, nil)

	args, _ := json.Marshal(map[string]any{
		"text":   "bad",
		"due_at": "2026-06-03T09:00:00Z",
		"recurrence": map[string]any{
			"interval":  1,
			"frequency": "month",
			"end":       map[string]any{"type": "after_count", "count": 0},
		},
	})
	_, err := registry.ExecuteTool(ctx, "chat-1", "user-1", "memory_set_reminder", string(args))
	if err == nil {
		t.Fatal("expected validation error")
	}
}

func TestDeliverDueRemindersReschedulesDaily(t *testing.T) {
	ctx := context.Background()
	store := storage.OpenTestDB(t, "secret")
	defer store.Close()

	pipeline := NewPipeline(store, llm.StaticClient{}, llm.StaticEmbedder{Vector: []float32{0.1}}, nil, prompts.FixedTestRegistry(), Config{})

	anchor := time.Date(2026, 6, 1, 9, 0, 0, 0, time.UTC)
	rule := &reminders.RecurrenceRule{
		Interval:  1,
		Frequency: reminders.FrequencyDay,
		End:       reminders.RecurrenceEnd{Type: reminders.EndNever},
		Timezone:  "UTC",
	}
	reminder := storage.Reminder{
		ID:          "r1",
		ChatID:      "chat-1",
		RequesterID: "user-1",
		DueAt:       anchor,
		AnchorAt:    anchor,
		Text:        "ping",
		Status:      storage.ReminderPending,
		CreatedAt:   anchor,
		Recurrence:  rule,
	}
	if err := store.UpsertReminder(ctx, reminder); err != nil {
		t.Fatal(err)
	}

	var sent []string
	now := anchor.Add(time.Minute)
	if err := pipeline.DeliverDueReminders(ctx, now, func(_ context.Context, _, text string) error {
		sent = append(sent, text)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(sent) != 1 {
		t.Fatalf("sent = %d", len(sent))
	}

	pending, err := store.ListReminders(ctx, "chat-1", storage.ReminderPending)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 {
		t.Fatalf("expected rescheduled reminder, got %d", len(pending))
	}
	wantNext := time.Date(2026, 6, 2, 9, 0, 0, 0, time.UTC)
	if !pending[0].DueAt.Equal(wantNext) {
		t.Fatalf("next due_at = %v want %v", pending[0].DueAt, wantNext)
	}
	if pending[0].OccurrenceCount != 1 {
		t.Fatalf("occurrence_count = %d want 1", pending[0].OccurrenceCount)
	}
}

func TestDeliverDueRemindersCompletesOneShot(t *testing.T) {
	ctx := context.Background()
	store := storage.OpenTestDB(t, "secret")
	defer store.Close()

	pipeline := NewPipeline(store, llm.StaticClient{}, llm.StaticEmbedder{Vector: []float32{0.1}}, nil, prompts.FixedTestRegistry(), Config{})

	due := time.Date(2026, 6, 1, 9, 0, 0, 0, time.UTC)
	reminder := storage.Reminder{
		ID:          "r1",
		ChatID:      "chat-1",
		RequesterID: "user-1",
		DueAt:       due,
		Text:        "once",
		Status:      storage.ReminderPending,
		CreatedAt:   due,
	}
	if err := store.UpsertReminder(ctx, reminder); err != nil {
		t.Fatal(err)
	}

	if err := pipeline.DeliverDueReminders(ctx, due, func(context.Context, string, string) error { return nil }); err != nil {
		t.Fatal(err)
	}
	pending, err := store.ListReminders(ctx, "chat-1", storage.ReminderPending)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Fatalf("one-shot should be delivered, pending=%d", len(pending))
	}
}
