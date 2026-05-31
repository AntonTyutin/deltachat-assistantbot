package storage

import (
	"context"
	"testing"
	"time"
)

func TestDeleteAndSetListItemsDone(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	defer store.Close()

	now := time.Now()
	list := List{
		ID:        "list-1",
		ChatID:    "chat-1",
		Kind:      ListKindList,
		Title:     "todo",
		UpdatedAt: now,
	}
	if err := store.UpsertList(ctx, list, nil); err != nil {
		t.Fatal(err)
	}
	items := []ListItem{
		{ID: "i1", ListID: list.ID, Text: "a", CreatedBy: "u1", CreatedAt: now},
		{ID: "i2", ListID: list.ID, Text: "b", CreatedBy: "u1", CreatedAt: now},
	}
	for _, item := range items {
		if err := store.AddListItem(ctx, item); err != nil {
			t.Fatal(err)
		}
	}

	n, err := store.SetListItemsDone(ctx, list.ID, []string{"i1", "i2"}, true)
	if err != nil || n != 2 {
		t.Fatalf("set done: n=%d err=%v", n, err)
	}
	got, err := store.ListItems(ctx, list.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range got {
		if !item.Done {
			t.Fatalf("expected done item %q", item.ID)
		}
	}

	n, err = store.DeleteListItems(ctx, list.ID, []string{"i1"})
	if err != nil || n != 1 {
		t.Fatalf("delete: n=%d err=%v", n, err)
	}
	got, err = store.ListItems(ctx, list.ID)
	if err != nil || len(got) != 1 || got[0].ID != "i2" {
		t.Fatalf("remaining items: %#v err=%v", got, err)
	}
}
