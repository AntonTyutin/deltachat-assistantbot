package storage

import (
	"context"
	"testing"
	"time"
)

func TestNearestListsWithVectors(t *testing.T) {
	ctx := context.Background()
	store := OpenTestDB(t, "secret")
	defer store.Close()

	now := time.Now()
	shopping := List{
		ID: "list-shopping", ChatID: "c1", Kind: ListKindList, Title: "Shopping", UpdatedAt: now,
	}
	recipe := List{
		ID: "list-recipe", ChatID: "c1", Kind: ListKindNote, Title: "Recipe", UpdatedAt: now,
	}
	shoppingVec := []float32{1, 0, 0}
	recipeVec := []float32{0, 1, 0}
	if err := store.UpsertList(ctx, shopping, shoppingVec); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertList(ctx, recipe, recipeVec); err != nil {
		t.Fatal(err)
	}

	got, err := store.NearestLists(ctx, "c1", shoppingVec, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) == 0 || got[0].List.ID != shopping.ID {
		t.Fatalf("expected shopping list first, got %+v", got)
	}
}

func TestListEmbeddingTextIncludesDoneMarker(t *testing.T) {
	text := ListEmbeddingText(ListKindList, "Shopping", []ListItem{
		{Text: "milk", Done: true},
		{Text: "bread"},
	})
	if text != "list: Shopping\n[done] milk\nbread" {
		t.Fatalf("unexpected embedding text: %q", text)
	}
}
