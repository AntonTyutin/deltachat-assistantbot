package llm

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	openai "github.com/sashabaranov/go-openai"
)

func TestChatWithToolsRetriesAfterEmptyFinalMessage(t *testing.T) {
	t.Parallel()
	var reqNum int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqNum++
		_, _ = io.ReadAll(r.Body)
		switch reqNum {
		case 1:
			_, _ = w.Write([]byte(`{
				"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"c1","type":"function","function":{"name":"memory_read_list","arguments":"{}"}}]}}],
				"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}
			}`))
		case 2:
			_, _ = w.Write([]byte(`{
				"choices":[{"message":{"role":"assistant","content":""}}],
				"usage":{"prompt_tokens":0,"completion_tokens":0,"total_tokens":0}
			}`))
		case 3:
			_, _ = w.Write([]byte(`{
				"choices":[{"message":{"role":"assistant","content":"{\"reply\":\"ok\"}"}}],
				"usage":{"prompt_tokens":12,"completion_tokens":3,"total_tokens":15}
			}`))
		default:
			t.Fatalf("unexpected request %d", reqNum)
		}
	}))
	t.Cleanup(srv.Close)

	client := NewOpenRouterClient(srv.URL, "test-key", "test-model", nil, nil, 0, 128, nil)
	text, err := client.ChatWithTools(t.Context(), TaskGenerateChatReply, []openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleUser, Content: "hi"},
	}, []ToolDefinition{
		{
			Type: string(openai.ToolTypeFunction),
			Function: &openai.FunctionDefinition{
				Name:       "memory_read_list",
				Parameters: map[string]any{"type": "object"},
			},
		},
	}, func(context.Context, string, string) (string, error) {
		return `{"lists":[]}`, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(text) != `{"reply":"ok"}` {
		t.Fatalf("content: %q", text)
	}
	if reqNum != 3 {
		t.Fatalf("requests: got %d want 3", reqNum)
	}
}

func TestChatWithToolsEmptyFinalMessageFails(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{
			"choices":[{"message":{"role":"assistant","content":""}}],
			"usage":{"prompt_tokens":0,"completion_tokens":0,"total_tokens":0}
		}`))
	}))
	t.Cleanup(srv.Close)

	client := NewOpenRouterClient(srv.URL, "test-key", "test-model", nil, nil, 0, 128, nil)
	_, err := client.ChatWithTools(t.Context(), TaskGenerateChatReply, []openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleUser, Content: "hi"},
	}, []ToolDefinition{
		{
			Type:     string(openai.ToolTypeFunction),
			Function: &openai.FunctionDefinition{Name: "memory_read_list"},
		},
	}, func(context.Context, string, string) (string, error) { return "{}", nil })
	if err == nil {
		t.Fatal("expected error")
	}
}
