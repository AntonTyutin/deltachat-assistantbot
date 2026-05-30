package llm

import (
	"encoding/json"
	"strings"

	openai "github.com/sashabaranov/go-openai"
)

// OpenRouterToolTypePrefix marks OpenRouter server tool types (e.g. "openrouter:web_search").
const OpenRouterToolTypePrefix = "openrouter:"

// IsOpenRouterToolType reports whether toolType is an OpenRouter server tool type.
func IsOpenRouterToolType(toolType string) bool {
	return strings.HasPrefix(toolType, OpenRouterToolTypePrefix)
}

// ToolDefinition is a chat completion tool entry: MCP function or OpenRouter server tool.
type ToolDefinition struct {
	Type       string
	Function   *openai.FunctionDefinition
	Parameters map[string]any
}

// MarshalJSON serializes function tools and openrouter:* server tools for the OpenRouter API.
func (t ToolDefinition) MarshalJSON() ([]byte, error) {
	if IsOpenRouterToolType(t.Type) {
		body := map[string]any{"type": t.Type}
		if len(t.Parameters) > 0 {
			body["parameters"] = t.Parameters
		}
		return json.Marshal(body)
	}
	return json.Marshal(struct {
		Type     string                     `json:"type"`
		Function *openai.FunctionDefinition `json:"function"`
	}{
		Type:     string(openai.ToolTypeFunction),
		Function: t.Function,
	})
}

// FunctionToolDefinition builds a ToolDefinition from an OpenAI function tool.
func FunctionToolDefinition(t openai.Tool) ToolDefinition {
	return ToolDefinition{
		Type:     string(openai.ToolTypeFunction),
		Function: t.Function,
	}
}
