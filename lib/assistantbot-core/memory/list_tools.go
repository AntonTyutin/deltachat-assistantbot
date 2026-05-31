package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/AntonTyutin/assistantbot-core/storage"
)

type listItemsArgs struct {
	ListTitle string   `json:"list_title"`
	Items     []string `json:"items"`
	ItemIDs   []string `json:"item_ids"`
}

func listItemsToolSchema(itemsDescription string, requireItems bool) map[string]any {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"list_title": map[string]any{"type": "string"},
			"items": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": itemsDescription,
			},
			"item_ids": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Optional item ids from memory_read_lists",
			},
		},
		"required": []string{"list_title"},
	}
	if requireItems {
		schema["required"] = []string{"list_title", "items"}
	}
	return schema
}

func (r *ToolRegistry) addListItems(ctx context.Context, chatID, requesterID, argumentsJSON string) (string, error) {
	var args listItemsArgs
	if err := json.Unmarshal([]byte(argumentsJSON), &args); err != nil {
		return "", err
	}
	texts := nonEmptyStrings(args.Items)
	if len(texts) == 0 {
		return "", fmt.Errorf("items must contain at least one non-empty string")
	}
	list, err := r.findOrCreateList(ctx, chatID, args.ListTitle)
	if err != nil {
		return "", err
	}
	now := time.Now()
	itemIDs := make([]string, 0, len(texts))
	for i, text := range texts {
		item := storage.ListItem{
			ID:        storage.NewListItemID(list.ID, now.Add(time.Duration(i)*time.Millisecond)),
			ListID:    list.ID,
			Text:      text,
			CreatedBy: requesterID,
			CreatedAt: now,
		}
		if err := r.store.AddListItem(ctx, item); err != nil {
			return "", err
		}
		itemIDs = append(itemIDs, item.ID)
	}
	list.UpdatedAt = now
	if err := r.reindexList(ctx, list.ID); err != nil {
		return "", err
	}
	return marshalListOpResult("added", list.ID, itemIDs, listOpNotFound{})
}

func (r *ToolRegistry) removeListItems(ctx context.Context, chatID, argumentsJSON string) (string, error) {
	return r.mutateListItems(ctx, chatID, argumentsJSON, "removed", func(ctx context.Context, list storage.List, itemIDs []string) (int, error) {
		return r.store.DeleteListItems(ctx, list.ID, itemIDs)
	})
}

func (r *ToolRegistry) completeListItems(ctx context.Context, chatID, argumentsJSON string) (string, error) {
	return r.mutateListItems(ctx, chatID, argumentsJSON, "completed", func(ctx context.Context, list storage.List, itemIDs []string) (int, error) {
		return r.store.SetListItemsDone(ctx, list.ID, itemIDs, true)
	})
}

func (r *ToolRegistry) uncompleteListItems(ctx context.Context, chatID, argumentsJSON string) (string, error) {
	return r.mutateListItems(ctx, chatID, argumentsJSON, "uncompleted", func(ctx context.Context, list storage.List, itemIDs []string) (int, error) {
		return r.store.SetListItemsDone(ctx, list.ID, itemIDs, false)
	})
}

type listItemMutator func(ctx context.Context, list storage.List, itemIDs []string) (int, error)

func (r *ToolRegistry) mutateListItems(ctx context.Context, chatID, argumentsJSON, status string, mutate listItemMutator) (string, error) {
	var args listItemsArgs
	if err := json.Unmarshal([]byte(argumentsJSON), &args); err != nil {
		return "", err
	}
	if len(nonEmptyStrings(args.Items)) == 0 && len(nonEmptyStrings(args.ItemIDs)) == 0 {
		return "", fmt.Errorf("items or item_ids must contain at least one entry")
	}
	list, ok, err := r.store.FindListByTitle(ctx, chatID, args.ListTitle, storage.ListKindList)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("list %q not found", args.ListTitle)
	}
	itemIDs, notFound, err := r.resolveListItemIDs(ctx, list.ID, args.Items, args.ItemIDs)
	if err != nil {
		return "", err
	}
	if len(itemIDs) == 0 {
		return "", fmt.Errorf("no matching list items found")
	}
	count, err := mutate(ctx, list, itemIDs)
	if err != nil {
		return "", err
	}
	if count != len(itemIDs) {
		return "", fmt.Errorf("expected to update %d items, updated %d", len(itemIDs), count)
	}
	if err := r.reindexList(ctx, list.ID); err != nil {
		return "", err
	}
	return marshalListOpResult(status, list.ID, itemIDs, notFound)
}

func (r *ToolRegistry) findOrCreateList(ctx context.Context, chatID, title string) (storage.List, error) {
	list, ok, err := r.store.FindListByTitle(ctx, chatID, title, storage.ListKindList)
	if err != nil {
		return storage.List{}, err
	}
	if ok {
		return list, nil
	}
	now := time.Now()
	list = storage.List{
		ID:        storage.NewListID(chatID, now),
		ChatID:    chatID,
		Kind:      storage.ListKindList,
		Title:     title,
		UpdatedAt: now,
	}
	if err := r.store.UpsertList(ctx, list, nil); err != nil {
		return storage.List{}, err
	}
	return list, nil
}

func (r *ToolRegistry) resolveListItemIDs(ctx context.Context, listID string, texts, itemIDs []string) ([]string, listOpNotFound, error) {
	existing, err := r.store.ListItems(ctx, listID)
	if err != nil {
		return nil, listOpNotFound{}, err
	}
	byID := make(map[string]storage.ListItem, len(existing))
	byText := make(map[string][]string)
	for _, item := range existing {
		byID[item.ID] = item
		key := normalizeListItemText(item.Text)
		byText[key] = append(byText[key], item.ID)
	}

	seen := make(map[string]struct{})
	var matched []string
	var notFound listOpNotFound

	for _, id := range nonEmptyStrings(itemIDs) {
		if _, ok := byID[id]; ok {
			if _, dup := seen[id]; !dup {
				seen[id] = struct{}{}
				matched = append(matched, id)
			}
			continue
		}
		notFound.ItemIDs = append(notFound.ItemIDs, id)
	}
	for _, text := range nonEmptyStrings(texts) {
		key := normalizeListItemText(text)
		ids := byText[key]
		if len(ids) == 0 {
			notFound.Texts = append(notFound.Texts, text)
			continue
		}
		for _, id := range ids {
			if _, dup := seen[id]; dup {
				continue
			}
			seen[id] = struct{}{}
			matched = append(matched, id)
		}
	}
	return matched, notFound, nil
}

type listOpNotFound struct {
	Texts   []string `json:"texts,omitempty"`
	ItemIDs []string `json:"item_ids,omitempty"`
}

type listOpResult struct {
	Status   string          `json:"status"`
	ListID   string          `json:"list_id"`
	ItemIDs  []string        `json:"item_ids"`
	Count    int             `json:"count"`
	NotFound *listOpNotFound `json:"not_found,omitempty"`
}

func marshalListOpResult(status, listID string, itemIDs []string, notFound listOpNotFound) (string, error) {
	result := listOpResult{
		Status:  status,
		ListID:  listID,
		ItemIDs: itemIDs,
		Count:   len(itemIDs),
	}
	if len(notFound.Texts) > 0 || len(notFound.ItemIDs) > 0 {
		result.NotFound = &notFound
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func nonEmptyStrings(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}

func normalizeListItemText(text string) string {
	return strings.ToLower(strings.TrimSpace(text))
}
