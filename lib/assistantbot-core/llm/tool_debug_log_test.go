package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestLogToolCallDebugSuccess(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	LogToolCall(context.Background(), logger, "memory", "memory_add_note",
		`{"title":"shopping","text":"milk"}`,
		`{"status":"saved","list_id":"list-1"}`,
		nil, 12*time.Millisecond,
		"chat_id", "chat-1", "requester_id", "user-1",
	)

	line := strings.TrimSpace(buf.String())
	if line == "" {
		t.Fatal("expected debug log line")
	}
	var entry map[string]any
	if err := json.Unmarshal([]byte(line), &entry); err != nil {
		t.Fatal(err)
	}
	if entry["level"] != "DEBUG" || entry["msg"] != "tool call" {
		t.Fatalf("unexpected log entry: %#v", entry)
	}
	if !strings.Contains(line, "memory_add_note") || !strings.Contains(line, "shopping") || !strings.Contains(line, "saved") {
		t.Fatalf("expected tool data in log: %s", line)
	}
}

func TestLogToolCallErrorAlwaysWithoutArguments(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))

	LogToolCall(context.Background(), logger, "mcp", "mcp__web__search",
		`{"query":"secret"}`, "", errors.New("upstream unavailable"), time.Millisecond,
		"server", "web",
	)

	line := strings.TrimSpace(buf.String())
	if line == "" {
		t.Fatal("expected error log line")
	}
	if strings.Contains(line, "secret") {
		t.Fatalf("error log must not include tool arguments: %s", line)
	}
	var entry map[string]any
	if err := json.Unmarshal([]byte(line), &entry); err != nil {
		t.Fatal(err)
	}
	if entry["level"] != "ERROR" || entry["msg"] != "tool call failed" {
		t.Fatalf("unexpected log entry: %#v", entry)
	}
}

func TestLogToolCallErrorDebugIncludesArguments(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	LogToolCall(context.Background(), logger, "memory", "memory_add_note",
		`{"title":"shopping"}`, "", errors.New("db locked"), time.Millisecond)

	output := buf.String()
	if strings.Count(output, "tool call failed") != 1 {
		t.Fatalf("expected one error log line, got: %s", output)
	}
	if !strings.Contains(output, "tool call details") || !strings.Contains(output, "shopping") {
		t.Fatalf("expected debug details with arguments: %s", output)
	}
}

func TestLogToolCallSuccessSkippedWhenLevelInfo(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))

	LogToolCall(context.Background(), logger, "memory", "memory_read_lists", `{}`, `[]`, nil, time.Millisecond)
	if buf.Len() != 0 {
		t.Fatalf("expected no logs at info level, got %s", buf.String())
	}
}
