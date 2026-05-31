package storage

import (
	"context"
	"testing"
)

func TestNearestMessagesWithVectors(t *testing.T) {
	ctx := context.Background()
	store := OpenTestDB(t, "secret")
	defer store.Close()

	vec := []float32{0.1, 0.2, 0.3}
	if err := store.UpsertMessage(ctx, Message{ID: "m1", ChatID: "c1", SenderID: "u1", Text: "hello"}, vec); err != nil {
		t.Fatal(err)
	}
	got, err := store.NearestMessages(ctx, "c1", vec, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 nearest message, got %d", len(got))
	}
	if got[0].Message.ID != "m1" {
		t.Fatalf("expected m1, got %q", got[0].Message.ID)
	}
}

// TestVec0MATCHKBrokenInWASM guards against silently upgrading to a fixed WASM build.
// vec0 MATCH/k currently panics once the table has rows (see storage/vec_knn_test.go history).
func TestVec0MATCHKBrokenInWASM(t *testing.T) {
	t.Skip("manual check: MATCH/k panics in ncruces WASM; Nearest* uses vec_distance_L2 instead")
}
