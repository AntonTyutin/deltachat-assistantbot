package llm

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	openai "github.com/sashabaranov/go-openai"
)

func TestCreateChatCompletionWithToolsMixedTypes(t *testing.T) {
	t.Parallel()
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("path: %s", r.URL.Path)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		gotBody = string(body)
		_, _ = w.Write([]byte(`{
			"choices":[{"message":{"role":"assistant","content":"done"}}],
			"usage":{"prompt_tokens":1,"completion_tokens":2,"total_tokens":3}
		}`))
	}))
	t.Cleanup(srv.Close)

	client := NewOpenRouterClient(srv.URL, "test-key", "test-model", nil, nil, 0, 128, nil)
	resp, err := client.createChatCompletionWithTools(t.Context(), chatCompletionRequestBody{
		Model: "test-model",
		Messages: []openai.ChatCompletionMessage{
			{Role: openai.ChatMessageRoleUser, Content: "hi"},
		},
		Tools: []ToolDefinition{
			{Type: "openrouter:web_search"},
			{
				Type: string(openai.ToolTypeFunction),
				Function: &openai.FunctionDefinition{
					Name:       "weather__get_weather",
					Parameters: map[string]any{"type": "object"},
				},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(resp.Choices[0].Message.Content) != "done" {
		t.Fatalf("content: %q", resp.Choices[0].Message.Content)
	}
	if !strings.Contains(gotBody, `"type":"openrouter:web_search"`) {
		t.Fatalf("missing openrouter tool: %s", gotBody)
	}
	if !strings.Contains(gotBody, `"name":"weather__get_weather"`) {
		t.Fatalf("missing function tool: %s", gotBody)
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(gotBody), &parsed); err != nil {
		t.Fatal(err)
	}
	tools, ok := parsed["tools"].([]any)
	if !ok || len(tools) != 2 {
		t.Fatalf("tools: %v", parsed["tools"])
	}
}
