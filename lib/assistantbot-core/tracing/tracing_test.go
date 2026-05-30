package tracing

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"
)

func TestNewIDUnique(t *testing.T) {
	seen := map[string]struct{}{}
	for i := 0; i < 1000; i++ {
		id := NewID()
		if id == "" {
			t.Fatal("empty id")
		}
		if _, ok := seen[id]; ok {
			t.Fatalf("duplicate id %q", id)
		}
		seen[id] = struct{}{}
	}
}

func TestContextRoundTrip(t *testing.T) {
	ctx := context.Background()
	if TraceID(ctx) != "" || ParentTraceID(ctx) != "" {
		t.Fatal("expected empty on bare context")
	}
	ctx = WithTraceID(ctx, "abc")
	ctx = WithParentTraceID(ctx, "parent")
	if got := TraceID(ctx); got != "abc" {
		t.Fatalf("trace id: got %q", got)
	}
	if got := ParentTraceID(ctx); got != "parent" {
		t.Fatalf("parent trace id: got %q", got)
	}
	if WithTraceID(ctx, "") != ctx {
		t.Fatal("empty trace id should not allocate a new context")
	}
}

func TestHandlerInjectsTraceIDs(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(NewHandler(slog.NewJSONHandler(&buf, nil)))

	ctx := WithParentTraceID(WithTraceID(context.Background(), "run-1"), "parent-1")
	logger.InfoContext(ctx, "hello", "chat_id", "c1")

	var entry map[string]any
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if entry["trace_id"] != "run-1" {
		t.Fatalf("trace_id: got %v", entry["trace_id"])
	}
	if entry["parent_trace_id"] != "parent-1" {
		t.Fatalf("parent_trace_id: got %v", entry["parent_trace_id"])
	}
	if entry["chat_id"] != "c1" {
		t.Fatalf("chat_id: got %v", entry["chat_id"])
	}
}

func TestHandlerOmitsWhenAbsent(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(NewHandler(slog.NewJSONHandler(&buf, nil)))

	logger.InfoContext(context.Background(), "hello")

	var entry map[string]any
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := entry["trace_id"]; ok {
		t.Fatalf("unexpected trace_id: %v", entry["trace_id"])
	}
	if _, ok := entry["parent_trace_id"]; ok {
		t.Fatalf("unexpected parent_trace_id: %v", entry["parent_trace_id"])
	}
}
