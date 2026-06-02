package memory

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/AntonTyutin/assistantbot-core/llm"
	"github.com/AntonTyutin/assistantbot-core/storage"
)

func TestListItemBatchOperations(t *testing.T) {
	ctx := context.Background()
	store := storage.OpenTestDB(t, "secret")
	defer store.Close()
	registry := NewToolRegistry(store, llm.StaticEmbedder{Vector: []float32{0.1, 0.2, 0.3}}, nil)

	addArgs, _ := json.Marshal(map[string]any{
		"list_title": "Shopping",
		"items":      []string{"milk", "bread", "eggs"},
	})
	addOut, err := registry.ExecuteTool(ctx, "chat-1", "user-1", "memory_add_list_items", string(addArgs))
	if err != nil {
		t.Fatal(err)
	}
	var addResult struct {
		ListID string `json:"list_id"`
	}
	if err := json.Unmarshal([]byte(addOut), &addResult); err != nil {
		t.Fatal(err)
	}
	if addResult.ListID == "" {
		t.Fatalf("missing list_id in add result: %s", addOut)
	}

	readList := func() struct {
		Title string `json:"title"`
		Items []struct {
			ID   string `json:"id"`
			Text string `json:"text"`
			Done bool   `json:"done"`
		} `json:"items"`
	} {
		t.Helper()
		readArgs, _ := json.Marshal(map[string]string{"list_id": addResult.ListID})
		readOut, err := registry.ExecuteTool(ctx, "chat-1", "user-1", "memory_read_list", string(readArgs))
		if err != nil {
			t.Fatal(err)
		}
		var list struct {
			Title string `json:"title"`
			Items []struct {
				ID   string `json:"id"`
				Text string `json:"text"`
				Done bool   `json:"done"`
			} `json:"items"`
		}
		if err := json.Unmarshal([]byte(readOut), &list); err != nil {
			t.Fatal(err)
		}
		return list
	}

	list := readList()
	if list.Title != "Shopping" || len(list.Items) != 3 {
		t.Fatalf("unexpected list: title=%q items=%d", list.Title, len(list.Items))
	}

	completeArgs, _ := json.Marshal(map[string]any{
		"list_title": "Shopping",
		"items":      []string{"milk", "bread"},
	})
	completeOut, err := registry.ExecuteTool(ctx, "chat-1", "user-1", "memory_complete_list_items", string(completeArgs))
	if err != nil {
		t.Fatal(err)
	}
	var completeResult struct {
		Status  string   `json:"status"`
		Count   int      `json:"count"`
		ItemIDs []string `json:"item_ids"`
	}
	if err := json.Unmarshal([]byte(completeOut), &completeResult); err != nil {
		t.Fatal(err)
	}
	if completeResult.Status != "completed" || completeResult.Count != 2 {
		t.Fatalf("complete result: %s", completeOut)
	}

	list = readList()
	doneCount := 0
	for _, item := range list.Items {
		if item.Done {
			doneCount++
		}
	}
	if doneCount != 2 {
		t.Fatalf("expected 2 done items, got %d", doneCount)
	}

	uncompleteArgs, _ := json.Marshal(map[string]any{
		"list_title": "Shopping",
		"item_ids":   []string{completeResult.ItemIDs[0]},
	})
	if _, err := registry.ExecuteTool(ctx, "chat-1", "user-1", "memory_uncomplete_list_items", string(uncompleteArgs)); err != nil {
		t.Fatal(err)
	}

	removeArgs, _ := json.Marshal(map[string]any{
		"list_title": "Shopping",
		"items":      []string{"eggs"},
	})
	removeOut, err := registry.ExecuteTool(ctx, "chat-1", "user-1", "memory_remove_list_items", string(removeArgs))
	if err != nil {
		t.Fatal(err)
	}
	var removeResult struct {
		Status string `json:"status"`
		Count  int    `json:"count"`
	}
	if err := json.Unmarshal([]byte(removeOut), &removeResult); err != nil {
		t.Fatal(err)
	}
	if removeResult.Status != "removed" || removeResult.Count != 1 {
		t.Fatalf("remove result: %s", removeOut)
	}

	list = readList()
	if len(list.Items) != 2 {
		t.Fatalf("expected 2 remaining items, got %d", len(list.Items))
	}
}

func TestReadListScopedToChat(t *testing.T) {
	ctx := context.Background()
	store := storage.OpenTestDB(t, "secret")
	defer store.Close()
	registry := NewToolRegistry(store, llm.StaticEmbedder{Vector: []float32{0.1, 0.2, 0.3}}, nil)

	now := time.Now()
	list := storage.List{
		ID:        "l1",
		ChatID:    "chat-a",
		Kind:      storage.ListKindList,
		Title:     "Shopping",
		UpdatedAt: now,
	}
	if err := store.UpsertList(ctx, list, nil); err != nil {
		t.Fatal(err)
	}

	args, _ := json.Marshal(map[string]string{"list_id": "l1"})
	_, err := registry.ExecuteTool(ctx, "chat-b", "user-1", "memory_read_list", string(args))
	if err == nil {
		t.Fatal("expected read from another chat to fail")
	}

	out, err := registry.ExecuteTool(ctx, "chat-a", "user-1", "memory_read_list", string(args))
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil || got.ID != "l1" {
		t.Fatalf("read list: out=%s err=%v", out, err)
	}
}

func TestCompleteListItemsRequiresExistingList(t *testing.T) {
	ctx := context.Background()
	store := storage.OpenTestDB(t, "secret")
	defer store.Close()
	registry := NewToolRegistry(store, llm.StaticEmbedder{Vector: []float32{0.1, 0.2, 0.3}}, nil)

	args, _ := json.Marshal(map[string]any{
		"list_title": "Missing",
		"items":      []string{"one"},
	})
	_, err := registry.ExecuteTool(ctx, "chat-1", "user-1", "memory_complete_list_items", string(args))
	if err == nil {
		t.Fatal("expected error for missing list")
	}
}

func TestCancelReminderScopedToChat(t *testing.T) {
	ctx := context.Background()
	store := storage.OpenTestDB(t, "secret")
	defer store.Close()
	registry := NewToolRegistry(store, llm.StaticEmbedder{Vector: []float32{0.1, 0.2, 0.3}}, nil)

	now := time.Now()
	reminder := storage.Reminder{
		ID:          "r1",
		ChatID:      "chat-a",
		RequesterID: "user-1",
		DueAt:       now.Add(time.Hour),
		Text:        "test",
		Status:      storage.ReminderPending,
		CreatedAt:   now,
	}
	if err := store.UpsertReminder(ctx, reminder); err != nil {
		t.Fatal(err)
	}

	args, _ := json.Marshal(map[string]string{"reminder_id": "r1"})
	_, err := registry.ExecuteTool(ctx, "chat-b", "user-1", "memory_cancel_reminder", string(args))
	if err == nil {
		t.Fatal("expected cancel from another chat to fail")
	}

	pending, err := store.ListReminders(ctx, "chat-a", storage.ReminderPending)
	if err != nil || len(pending) != 1 {
		t.Fatalf("reminder should stay pending: len=%d err=%v", len(pending), err)
	}

	_, err = registry.ExecuteTool(ctx, "chat-a", "user-1", "memory_cancel_reminder", string(args))
	if err != nil {
		t.Fatalf("cancel in same chat: %v", err)
	}
	pending, err = store.ListReminders(ctx, "chat-a", storage.ReminderPending)
	if err != nil || len(pending) != 0 {
		t.Fatalf("reminder should be cancelled: len=%d err=%v", len(pending), err)
	}
}

func TestSetReminderUsesProfileTimezoneForNaiveDueAt(t *testing.T) {
	ctx := context.Background()
	store := storage.OpenTestDB(t, "secret")
	defer store.Close()
	registry := NewToolRegistry(store, llm.StaticEmbedder{Vector: []float32{0.1, 0.2, 0.3}}, nil)

	if err := store.UpsertProfile(ctx, storage.ParticipantProfile{
		ID:       "user-1",
		Timezone: "Europe/Moscow",
	}); err != nil {
		t.Fatal(err)
	}

	args, _ := json.Marshal(map[string]string{
		"text":   "call mom",
		"due_at": "2026-06-01T09:00:00",
	})
	out, err := registry.ExecuteTool(ctx, "chat-1", "user-1", "memory_set_reminder", string(args))
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		DueAt string `json:"due_at"`
	}
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatal(err)
	}
	parsed, err := time.Parse(time.RFC3339, result.DueAt)
	if err != nil {
		t.Fatal(err)
	}
	loc, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 6, 1, 9, 0, 0, 0, loc)
	if !parsed.Equal(want) {
		t.Fatalf("due_at = %v, want %v", parsed, want)
	}
}
