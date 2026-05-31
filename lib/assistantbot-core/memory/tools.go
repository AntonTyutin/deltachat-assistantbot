package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	openai "github.com/sashabaranov/go-openai"

	"github.com/AntonTyutin/assistantbot-core/llm"
	"github.com/AntonTyutin/assistantbot-core/metrics"
	"github.com/AntonTyutin/assistantbot-core/storage"
)

type ToolRegistry struct {
	store  *storage.Store
	lists  *ListIndexer
	logger *slog.Logger
}

func NewToolRegistry(store *storage.Store, embedder llm.Embedder, logger *slog.Logger) *ToolRegistry {
	return &ToolRegistry{store: store, lists: NewListIndexer(store, embedder), logger: logger}
}

func (r *ToolRegistry) reindexList(ctx context.Context, listID string) error {
	if r.lists == nil {
		return nil
	}
	return r.lists.Reindex(ctx, listID)
}

func (r *ToolRegistry) ToolsForTask(task string) []llm.ToolDefinition {
	if task != llm.TaskGenerateChatReply {
		return nil
	}
	return []llm.ToolDefinition{
		functionTool("memory_add_note", "Create or append a note in the current chat", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"title": map[string]any{"type": "string"},
				"text":  map[string]any{"type": "string"},
			},
			"required": []string{"title", "text"},
		}),
		functionTool("memory_add_list_items", "Add one or more items to a named todo/shopping list in the current chat", listItemsToolSchema(
			"Item texts to add",
			true,
		)),
		functionTool("memory_remove_list_items", "Remove items from a named list by text and/or item_ids from memory_read_lists", listItemsToolSchema(
			"Item texts to remove (matches all items with the same text, case-insensitive)",
			false,
		)),
		functionTool("memory_complete_list_items", "Mark list items as done by text and/or item_ids", listItemsToolSchema(
			"Item texts to mark done",
			false,
		)),
		functionTool("memory_uncomplete_list_items", "Clear done flag on list items by text and/or item_ids", listItemsToolSchema(
			"Item texts to mark not done",
			false,
		)),
		functionTool("memory_read_lists", "Read lists and notes for the current chat", map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		}),
		functionTool("memory_set_reminder", "Schedule a reminder in the current chat", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"text":   map[string]any{"type": "string"},
				"due_at": map[string]any{"type": "string", "description": "RFC3339 timestamp"},
			},
			"required": []string{"text", "due_at"},
		}),
		functionTool("memory_list_reminders", "List pending reminders for the current chat", map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		}),
		functionTool("memory_cancel_reminder", "Cancel a reminder by id", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"reminder_id": map[string]any{"type": "string"},
			},
			"required": []string{"reminder_id"},
		}),
	}
}

func (r *ToolRegistry) HasToolsForTask(task string) bool {
	return len(r.ToolsForTask(task)) > 0
}

func (r *ToolRegistry) SystemPromptAppendForTask(task string) string {
	if task != llm.TaskGenerateChatReply {
		return ""
	}
	return strings.TrimSpace(`You have memory tools for notes, lists, and reminders in the current chat.
When the user mentions something worth remembering, suggest to add it to an appropriate note/list o create a new note/list.
If the user agrees, use the appropriate tool to save the item.
For lists (todo/shopping), use memory_read_lists to see item ids, then batch tools: memory_add_list_items, memory_remove_list_items, memory_complete_list_items, memory_uncomplete_list_items. Match items by text and/or item_ids.
For reminders, parse due_at as RFC3339. If timezone is unknown, ask the user and store timezone in profile when provided.
Use the chat language to store notes, list items, and reminders.`)
}

func (r *ToolRegistry) ExecuteTool(ctx context.Context, chatID, requesterID, toolName, argumentsJSON string) (result string, err error) {
	start := time.Now()
	defer func() {
		llm.LogToolCall(ctx, r.logger, metrics.ToolSourceMemory, toolName, argumentsJSON, result, err, time.Since(start),
			"chat_id", chatID, "requester_id", requesterID)
	}()

	switch toolName {
	case "memory_add_note":
		return r.addNote(ctx, chatID, requesterID, argumentsJSON)
	case "memory_add_list_items":
		return r.addListItems(ctx, chatID, requesterID, argumentsJSON)
	case "memory_remove_list_items":
		return r.removeListItems(ctx, chatID, argumentsJSON)
	case "memory_complete_list_items":
		return r.completeListItems(ctx, chatID, argumentsJSON)
	case "memory_uncomplete_list_items":
		return r.uncompleteListItems(ctx, chatID, argumentsJSON)
	case "memory_read_lists":
		return r.readLists(ctx, chatID)
	case "memory_set_reminder":
		return r.setReminder(ctx, chatID, requesterID, argumentsJSON)
	case "memory_list_reminders":
		return r.listReminders(ctx, chatID)
	case "memory_cancel_reminder":
		return r.cancelReminder(ctx, chatID, argumentsJSON)
	default:
		return "", fmt.Errorf("unknown memory tool %q", toolName)
	}
}

func (r *ToolRegistry) addNote(ctx context.Context, chatID, requesterID, argumentsJSON string) (string, error) {
	var args struct {
		Title string `json:"title"`
		Text  string `json:"text"`
	}
	if err := json.Unmarshal([]byte(argumentsJSON), &args); err != nil {
		return "", err
	}
	now := time.Now()
	list, ok, err := r.store.FindListByTitle(ctx, chatID, args.Title, storage.ListKindNote)
	if err != nil {
		return "", err
	}
	if !ok {
		list = storage.List{
			ID:        storage.NewListID(chatID, now),
			ChatID:    chatID,
			Kind:      storage.ListKindNote,
			Title:     args.Title,
			UpdatedAt: now,
		}
		if err := r.store.UpsertList(ctx, list, nil); err != nil {
			return "", err
		}
	}
	item := storage.ListItem{
		ID:        storage.NewListItemID(list.ID, now),
		ListID:    list.ID,
		Text:      args.Text,
		CreatedBy: requesterID,
		CreatedAt: now,
	}
	if err := r.store.AddListItem(ctx, item); err != nil {
		return "", err
	}
	if err := r.reindexList(ctx, list.ID); err != nil {
		return "", err
	}
	return fmt.Sprintf(`{"status":"saved","list_id":%q,"item_id":%q}`, list.ID, item.ID), nil
}

func (r *ToolRegistry) readLists(ctx context.Context, chatID string) (string, error) {
	lists, err := r.store.ListLists(ctx, chatID, "")
	if err != nil {
		return "", err
	}
	out := make([]map[string]any, 0, len(lists))
	for _, list := range lists {
		items, err := r.store.ListItems(ctx, list.ID)
		if err != nil {
			return "", err
		}
		out = append(out, map[string]any{
			"id":    list.ID,
			"kind":  list.Kind,
			"title": list.Title,
			"items": items,
		})
	}
	raw, err := json.Marshal(out)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func (r *ToolRegistry) setReminder(ctx context.Context, chatID, requesterID, argumentsJSON string) (string, error) {
	var args struct {
		Text  string `json:"text"`
		DueAt string `json:"due_at"`
	}
	if err := json.Unmarshal([]byte(argumentsJSON), &args); err != nil {
		return "", err
	}
	dueAt, err := r.parseReminderDueAt(ctx, requesterID, args.DueAt)
	if err != nil {
		return "", err
	}
	now := time.Now()
	reminder := storage.Reminder{
		ID:          storage.NewReminderID(chatID, now),
		ChatID:      chatID,
		RequesterID: requesterID,
		DueAt:       dueAt,
		Text:        args.Text,
		Status:      storage.ReminderPending,
		CreatedAt:   now,
	}
	if err := r.store.UpsertReminder(ctx, reminder); err != nil {
		return "", err
	}
	return fmt.Sprintf(`{"status":"scheduled","reminder_id":%q,"due_at":%q}`, reminder.ID, reminder.DueAt.Format(time.RFC3339)), nil
}

func (r *ToolRegistry) listReminders(ctx context.Context, chatID string) (string, error) {
	reminders, err := r.store.ListReminders(ctx, chatID, storage.ReminderPending)
	if err != nil {
		return "", err
	}
	raw, err := json.Marshal(reminders)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func (r *ToolRegistry) cancelReminder(ctx context.Context, chatID, argumentsJSON string) (string, error) {
	var args struct {
		ReminderID string `json:"reminder_id"`
	}
	if err := json.Unmarshal([]byte(argumentsJSON), &args); err != nil {
		return "", err
	}
	if err := r.store.CancelReminder(ctx, chatID, args.ReminderID); err != nil {
		return "", err
	}
	return `{"status":"cancelled"}`, nil
}

func (r *ToolRegistry) parseReminderDueAt(ctx context.Context, requesterID, dueAt string) (time.Time, error) {
	dueAt = strings.TrimSpace(dueAt)
	if t, err := time.Parse(time.RFC3339, dueAt); err == nil {
		return t, nil
	}
	loc := time.UTC
	if requesterID != "" {
		profile, ok, err := r.store.GetProfile(ctx, requesterID)
		if err != nil {
			return time.Time{}, err
		}
		if ok {
			if tz := strings.TrimSpace(profile.Timezone); tz != "" {
				if loaded, err := time.LoadLocation(tz); err == nil {
					loc = loaded
				}
			}
		}
	}
	for _, layout := range []string{"2006-01-02T15:04:05", "2006-01-02 15:04:05"} {
		if t, err := time.ParseInLocation(layout, dueAt, loc); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("due_at must be RFC3339 or a local datetime when the requester profile has timezone")
}

func functionTool(name, description string, parameters map[string]any) llm.ToolDefinition {
	return llm.ToolDefinition{
		Type: string(openai.ToolTypeFunction),
		Function: &openai.FunctionDefinition{
			Name:        name,
			Description: description,
			Parameters:  parameters,
		},
	}
}
