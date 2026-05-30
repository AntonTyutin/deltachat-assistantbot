package llm

import (
	"encoding/json"
	"testing"

	openai "github.com/sashabaranov/go-openai"
)

func TestToolDefinitionMarshalFunction(t *testing.T) {
	t.Parallel()
	def := ToolDefinition{
		Type: string(openai.ToolTypeFunction),
		Function: &openai.FunctionDefinition{
			Name:        "weather__get_weather",
			Description: "Get weather",
			Parameters:  map[string]any{"type": "object"},
		},
	}
	raw, err := json.Marshal(def)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got["type"] != "function" {
		t.Fatalf("type: %v", got["type"])
	}
	fn, ok := got["function"].(map[string]any)
	if !ok || fn["name"] != "weather__get_weather" {
		t.Fatalf("function: %v", got["function"])
	}
}

func TestToolDefinitionMarshalOpenRouter(t *testing.T) {
	t.Parallel()
	def := ToolDefinition{
		Type: "openrouter:web_search",
		Parameters: map[string]any{
			"max_total_results": float64(3),
		},
	}
	raw, err := json.Marshal(def)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"parameters":{"max_total_results":3},"type":"openrouter:web_search"}`
	if string(raw) != want {
		t.Fatalf("got %s want %s", raw, want)
	}
}

func TestToolDefinitionMarshalOpenRouterNoParameters(t *testing.T) {
	t.Parallel()
	def := ToolDefinition{Type: "openrouter:datetime"}
	raw, err := json.Marshal(def)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `{"type":"openrouter:datetime"}` {
		t.Fatalf("got %s", raw)
	}
}
