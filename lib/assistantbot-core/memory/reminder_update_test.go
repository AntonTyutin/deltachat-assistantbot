package memory

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/AntonTyutin/assistantbot-core/llm"
	"github.com/AntonTyutin/assistantbot-core/reminders"
	"github.com/AntonTyutin/assistantbot-core/storage"
)

func TestUpdateReminderTimeOnly(t *testing.T) {
	ctx := context.Background()
	store := storage.OpenTestDB(t, "secret")
	defer store.Close()
	registry := NewToolRegistry(store, llm.StaticEmbedder{Vector: []float32{0.1, 0.2, 0.3}}, nil)

	loc := time.UTC
	anchor := time.Date(2026, 6, 3, 9, 0, 0, 0, loc) // Tuesday
	rule := &reminders.RecurrenceRule{
		Interval:  1,
		Frequency: reminders.FrequencyWeek,
		Weekdays:  []time.Weekday{time.Tuesday},
		End:       reminders.RecurrenceEnd{Type: reminders.EndNever},
		Timezone:  "UTC",
	}
	reminder := storage.Reminder{
		ID:          "r1",
		ChatID:      "chat-1",
		RequesterID: "user-1",
		DueAt:       anchor,
		AnchorAt:    anchor,
		Text:        "выходить на работу",
		Status:      storage.ReminderPending,
		CreatedAt:   anchor,
		Recurrence:  rule,
	}
	if err := store.UpsertReminder(ctx, reminder); err != nil {
		t.Fatal(err)
	}

	args, _ := json.Marshal(map[string]any{
		"reminder_id": "r1",
		"time":        "10:30",
	})
	out, err := registry.ExecuteTool(ctx, "chat-1", "user-1", "memory_update_reminder", string(args))
	if err != nil {
		t.Fatal(err)
	}
	if out == "" {
		t.Fatal("expected output")
	}

	updated, ok, err := store.GetReminder(ctx, "chat-1", "r1")
	if err != nil || !ok {
		t.Fatalf("get reminder: ok=%v err=%v", ok, err)
	}
	anchorLocal := updated.AnchorAt.In(loc)
	if anchorLocal.Hour() != 10 || anchorLocal.Minute() != 30 {
		t.Fatalf("anchor time = %v", anchorLocal)
	}
	if updated.Recurrence == nil || updated.Recurrence.Frequency != reminders.FrequencyWeek {
		t.Fatal("recurrence should remain weekly")
	}
}

func TestUpdateReminderWeekdaysOnly(t *testing.T) {
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

	loc := time.UTC
	anchor := time.Date(2026, 6, 4, 14, 0, 0, 0, loc) // Thursday 14:00
	reminder := storage.Reminder{
		ID:          "r2",
		ChatID:      "chat-1",
		RequesterID: "user-1",
		DueAt:       anchor,
		AnchorAt:    anchor,
		Text:        "приезд мамы",
		Status:      storage.ReminderPending,
		CreatedAt:   anchor,
	}
	if err := store.UpsertReminder(ctx, reminder); err != nil {
		t.Fatal(err)
	}

	args, _ := json.Marshal(map[string]any{
		"reminder_id": "r2",
		"weekdays":    []string{"sunday"},
	})
	_, err := registry.ExecuteTool(ctx, "chat-1", "user-1", "memory_update_reminder", string(args))
	if err != nil {
		t.Fatal(err)
	}

	updated, ok, err := store.GetReminder(ctx, "chat-1", "r2")
	if err != nil || !ok {
		t.Fatalf("get reminder: ok=%v err=%v", ok, err)
	}
	dueLocal := updated.DueAt.In(loc)
	if dueLocal.Weekday() != time.Sunday {
		t.Fatalf("due weekday = %v want Sunday", dueLocal.Weekday())
	}
	if dueLocal.Hour() != 14 || dueLocal.Minute() != 0 {
		t.Fatalf("due time = %v want 14:00", dueLocal.Format(time.Kitchen))
	}
}

func TestUpdateReminderMergeRecurrenceEnd(t *testing.T) {
	ctx := context.Background()
	store := storage.OpenTestDB(t, "secret")
	defer store.Close()
	registry := NewToolRegistry(store, llm.StaticEmbedder{Vector: []float32{0.1, 0.2, 0.3}}, nil)

	rule := &reminders.RecurrenceRule{
		Interval:  1,
		Frequency: reminders.FrequencyWeek,
		Weekdays:  []time.Weekday{time.Tuesday},
		End:       reminders.RecurrenceEnd{Type: reminders.EndNever},
		Timezone:  "UTC",
	}
	anchor := time.Date(2026, 6, 3, 9, 0, 0, 0, time.UTC)
	reminder := storage.Reminder{
		ID:          "r-merge",
		ChatID:      "chat-1",
		RequesterID: "user-1",
		DueAt:       anchor,
		AnchorAt:    anchor,
		Text:        "выходить на работу",
		Status:      storage.ReminderPending,
		CreatedAt:   anchor,
		Recurrence:  rule,
	}
	if err := store.UpsertReminder(ctx, reminder); err != nil {
		t.Fatal(err)
	}

	args, _ := json.Marshal(map[string]any{
		"reminder_id": "r-merge",
		"recurrence":  map[string]any{"end": map[string]any{"type": "after_count", "count": 10}},
	})
	if _, err := registry.ExecuteTool(ctx, "chat-1", "user-1", "memory_update_reminder", string(args)); err != nil {
		t.Fatal(err)
	}

	updated, ok, err := store.GetReminder(ctx, "chat-1", "r-merge")
	if err != nil || !ok {
		t.Fatalf("get reminder: ok=%v err=%v", ok, err)
	}
	if updated.Recurrence == nil || updated.Recurrence.Frequency != reminders.FrequencyWeek {
		t.Fatal("weekly recurrence should be preserved")
	}
	if updated.Recurrence.End.Type != reminders.EndAfterCount || updated.Recurrence.End.Count != 10 {
		t.Fatalf("end = %+v", updated.Recurrence.End)
	}
}

func TestGetReminderRequiresPending(t *testing.T) {
	ctx := context.Background()
	store := storage.OpenTestDB(t, "secret")
	defer store.Close()

	now := time.Now()
	reminder := storage.Reminder{
		ID:          "r3",
		ChatID:      "chat-a",
		RequesterID: "user-1",
		DueAt:       now,
		Text:        "done",
		Status:      storage.ReminderDelivered,
		CreatedAt:   now,
	}
	if err := store.UpsertReminder(ctx, reminder); err != nil {
		t.Fatal(err)
	}
	_, ok, err := store.GetReminder(ctx, "chat-a", "r3")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("delivered reminder should not be returned")
	}
}
