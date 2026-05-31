package memory

import (
	"context"
	"time"

	"github.com/AntonTyutin/assistantbot-core/llm"
	"github.com/AntonTyutin/assistantbot-core/storage"
)

type ListIndexer struct {
	store    *storage.Store
	embedder llm.Embedder
}

func NewListIndexer(store *storage.Store, embedder llm.Embedder) *ListIndexer {
	return &ListIndexer{store: store, embedder: embedder}
}

func (i *ListIndexer) Reindex(ctx context.Context, listID string) error {
	if i == nil || i.store == nil || i.embedder == nil || listID == "" {
		return nil
	}
	list, ok, err := i.store.GetList(ctx, listID)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	items, err := i.store.ListItems(ctx, listID)
	if err != nil {
		return err
	}
	text := storage.ListEmbeddingText(list.Kind, list.Title, items)
	vectors, err := i.embedder.Embed(ctx, text)
	if err != nil {
		return err
	}
	var embedding []float32
	if len(vectors) > 0 {
		embedding = vectors[0]
	}
	list.UpdatedAt = time.Now().UTC()
	return i.store.UpsertList(ctx, list, embedding)
}
