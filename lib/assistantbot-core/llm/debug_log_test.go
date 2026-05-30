package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	openai "github.com/sashabaranov/go-openai"
)

func TestDebugLogChatRequestAndResponse(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	client := &OpenRouterClient{logger: logger}

	req := openai.ChatCompletionRequest{
		Model: "test-model",
		Messages: []openai.ChatCompletionMessage{
			{Role: openai.ChatMessageRoleUser, Content: "hello"},
		},
		MaxCompletionTokens: 100,
	}
	client.debugLogChatRequest(context.Background(), "generate_chat_reply", "test-model", "chat_completion", 0, req)

	resp := openai.ChatCompletionResponse{
		Choices: []openai.ChatCompletionChoice{
			{Message: openai.ChatCompletionMessage{Role: openai.ChatMessageRoleAssistant, Content: `{"ok":true}`}},
		},
		Usage: openai.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
	}
	client.debugLogChatResponse(context.Background(), "generate_chat_reply", "test-model", "complete_json", 0, resp)

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 log lines, got %d: %s", len(lines), buf.String())
	}
	for i, line := range lines {
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("line %d: %v", i, err)
		}
		if entry["level"] != "DEBUG" {
			t.Fatalf("line %d: expected DEBUG, got %v", i, entry["level"])
		}
	}
	if !strings.Contains(buf.String(), "llm request") || !strings.Contains(buf.String(), "llm response") {
		t.Fatalf("expected request/response messages in log: %s", buf.String())
	}
	if !strings.Contains(buf.String(), "hello") || !strings.Contains(buf.String(), "ok") {
		t.Fatalf("expected message bodies in log: %s", buf.String())
	}
}

func TestDebugLogSkippedWhenLevelInfo(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	client := &OpenRouterClient{logger: logger}

	client.debugLogChatRequest(context.Background(), "task", "model", "chat_completion", 0, openai.ChatCompletionRequest{})
	if buf.Len() != 0 {
		t.Fatalf("expected no logs at info level, got %s", buf.String())
	}
	if client.logger.Enabled(context.Background(), slog.LevelDebug) {
		t.Fatal("expected debug disabled")
	}
}
