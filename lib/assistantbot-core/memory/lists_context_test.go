package memory

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/AntonTyutin/assistantbot-core/llm"
	"github.com/AntonTyutin/assistantbot-core/storage"
	"github.com/AntonTyutin/assistantbot-core/transport"
)

func TestListsContextUsesKNNLists(t *testing.T) {
	ctx := context.Background()
	store := storage.OpenTestDB(t, "secret")
	defer store.Close()

	now := time.Now()
	shoppingText := storage.ListEmbeddingText(storage.ListKindList, "Shopping", []storage.ListItem{{Text: "milk"}})
	recipeText := storage.ListEmbeddingText(storage.ListKindNote, "Recipe", []storage.ListItem{{Text: "borscht"}})
	embedder := llm.TextKeyedEmbedder{
		Default: []float32{0, 0, 0},
		ByText: map[string][]float32{
			"what to buy": {1, 0, 0},
			shoppingText:  {1, 0, 0},
			recipeText:    {0, 1, 0},
		},
	}
	indexer := NewListIndexer(store, embedder)
	for _, list := range []storage.List{
		{ID: "l1", ChatID: "c1", Kind: storage.ListKindList, Title: "Shopping", UpdatedAt: now},
		{ID: "l2", ChatID: "c1", Kind: storage.ListKindNote, Title: "Recipe", UpdatedAt: now},
	} {
		if err := store.UpsertList(ctx, list, nil); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.AddListItem(ctx, storage.ListItem{ID: "i1", ListID: "l1", Text: "milk", CreatedBy: "u1", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.AddListItem(ctx, storage.ListItem{ID: "i2", ListID: "l2", Text: "borscht", CreatedBy: "u1", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := indexer.Reindex(ctx, "l1"); err != nil {
		t.Fatal(err)
	}
	if err := indexer.Reindex(ctx, "l2"); err != nil {
		t.Fatal(err)
	}

	pipeline := NewPipeline(store, llm.StaticClient{}, embedder, nil, nil, Config{ListKNNLists: 1})
	out, err := pipeline.ListsContext(ctx, "c1", transport.Message{
		ChatID: "c1",
		Text:   "what to buy",
	})
	if err != nil {
		t.Fatal(err)
	}
	listsRaw, ok := out["lists"].([]map[string]any)
	if !ok {
		raw, _ := json.Marshal(out)
		t.Fatalf("unexpected lists payload: %s", raw)
	}
	if len(listsRaw) != 1 {
		raw, _ := json.Marshal(out)
		t.Fatalf("expected one relevant list, got %s", raw)
	}
	if listsRaw[0]["title"] != "Shopping" {
		t.Fatalf("expected shopping list, got %#v", listsRaw[0]["title"])
	}
}
