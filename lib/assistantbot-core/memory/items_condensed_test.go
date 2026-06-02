package memory

import (
	"testing"
	"time"

	"github.com/AntonTyutin/assistantbot-core/storage"
)

func TestCondenseListItems(t *testing.T) {
	now := time.Now()
	items := []storage.ListItem{
		{Text: "milk", CreatedAt: now},
		{Text: "bread", Done: true, CreatedAt: now},
		{Text: "  ", CreatedAt: now},
	}
	got := CondenseListItems(items)
	want := "milk\nbread [done]"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if CondenseListItems(nil) != "" {
		t.Fatal("expected empty string for nil items")
	}
}
