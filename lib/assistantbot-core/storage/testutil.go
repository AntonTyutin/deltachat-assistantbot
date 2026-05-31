package storage

import (
	"context"
	"path/filepath"
	"testing"
)

func OpenTestDB(t testing.TB, secret string) *Store {
	t.Helper()
	store, err := Open(context.Background(), filepath.Join(t.TempDir(), "test.db"), secret, Options{
		RecentMessagesLimit: 20,
		EmbeddingDimensions: 3,
	})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	return store
}
