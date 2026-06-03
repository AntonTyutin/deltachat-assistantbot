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

type testMCPRuntime struct {
	tools []llm.ToolDefinition
}

func (t testMCPRuntime) ToolsForTask(task string) []llm.ToolDefinition {
	if task != llm.TaskGenerateChatReply {
		return nil
	}
	return t.tools
}

func (t testMCPRuntime) HasToolsForTask(task string) bool {
	return len(t.ToolsForTask(task)) > 0
}

func (t testMCPRuntime) ExecuteTool(_ context.Context, _ string, _ string) (string, error) {
	return `{"ok":true}`, nil
}

func (t testMCPRuntime) SystemPromptAppendForTask(_ string) string {
	return ""
}

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

func TestSetActionReminderRequiresActionPrompt(t *testing.T) {
	ctx := context.Background()
	store := storage.OpenTestDB(t, "secret")
	defer store.Close()
	registry := NewToolRegistry(store, llm.StaticEmbedder{Vector: []float32{0.1, 0.2, 0.3}}, nil)

	args, _ := json.Marshal(map[string]any{
		"mode":   "action",
		"due_at": "2026-06-03T09:00:00Z",
	})
	if _, err := registry.ExecuteTool(ctx, "chat-1", "user-1", "memory_set_reminder", string(args)); err == nil {
		t.Fatal("expected action_prompt validation error")
	}
}

func TestDeliverDueRemindersExecutesActionMode(t *testing.T) {
	ctx := context.Background()
	store := storage.OpenTestDB(t, "secret")
	defer store.Close()

	client := llm.StaticClient{
		Responses: map[string]json.RawMessage{
			llm.TaskGenerateChatReply: json.RawMessage(`{"reply":"Прогноз на день: +22, без осадков."}`),
		},
	}
	mcp := testMCPRuntime{
		tools: []llm.ToolDefinition{
			functionTool("mcp_weather", "fake weather tool", map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			}),
		},
	}
	pipeline := NewPipeline(store, client, llm.StaticEmbedder{Vector: []float32{0.1}}, mcp, prompts.FixedTestRegistry(), Config{})

	due := time.Date(2026, 6, 1, 8, 0, 0, 0, time.UTC)
	reminder := storage.Reminder{
		ID:           "r-action",
		ChatID:       "chat-1",
		RequesterID:  "user-1",
		DueAt:        due,
		AnchorAt:     due,
		Status:       storage.ReminderPending,
		CreatedAt:    due,
		Mode:         storage.ReminderModeAction,
		ActionPrompt: "Пришли сводку погоды на день",
	}
	if err := store.UpsertReminder(ctx, reminder); err != nil {
		t.Fatal(err)
	}

	var sent []string
	if err := pipeline.DeliverDueReminders(ctx, due, func(_ context.Context, _ string, text string) error {
		sent = append(sent, text)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(sent) != 1 {
		t.Fatalf("sent = %d", len(sent))
	}
	if sent[0] != "Прогноз на день: +22, без осадков." {
		t.Fatalf("unexpected action text: %q", sent[0])
	}
}

func TestDeliverDueRemindersSkipsFailedActionAndContinues(t *testing.T) {
	ctx := context.Background()
	store := storage.OpenTestDB(t, "secret")
	defer store.Close()

	client := llm.StaticClient{
		Responses: map[string]json.RawMessage{
			llm.TaskGenerateChatReply: json.RawMessage(`{"reply":"ok from llm"}`),
		},
	}
	pipeline := NewPipeline(store, client, llm.StaticEmbedder{Vector: []float32{0.1}}, nil, prompts.FixedTestRegistry(), Config{})

	now := time.Date(2026, 6, 1, 8, 0, 0, 0, time.UTC)
	action := storage.Reminder{
		ID:           "r-action-fail",
		ChatID:       "chat-1",
		RequesterID:  "user-1",
		DueAt:        now,
		AnchorAt:     now,
		Status:       storage.ReminderPending,
		CreatedAt:    now,
		Mode:         storage.ReminderModeAction,
		ActionPrompt: "fetch weather",
	}
	if err := store.UpsertReminder(ctx, action); err != nil {
		t.Fatal(err)
	}
	text := storage.Reminder{
		ID:          "r-text",
		ChatID:      "chat-1",
		RequesterID: "user-1",
		DueAt:       now,
		Text:        "static",
		Status:      storage.ReminderPending,
		CreatedAt:   now,
	}
	if err := store.UpsertReminder(ctx, text); err != nil {
		t.Fatal(err)
	}

	var sent []string
	if err := pipeline.DeliverDueReminders(ctx, now, func(_ context.Context, _ string, msg string) error {
		sent = append(sent, msg)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(sent) != 1 || sent[0] != "🔔 static" {
		t.Fatalf("unexpected sent messages: %#v", sent)
	}
	// Failed action reminder should remain pending for retry.
	remaining, err := store.ListReminders(ctx, "chat-1", storage.ReminderPending)
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 1 || remaining[0].ID != "r-action-fail" {
		t.Fatalf("unexpected pending reminders: %#v", remaining)
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
