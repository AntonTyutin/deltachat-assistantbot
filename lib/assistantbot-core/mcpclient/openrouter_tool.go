package mcpclient

import (
	"fmt"
	"strings"

	"github.com/AntonTyutin/assistantbot-core/llm"
)

var openRouterToolTypes = map[string]string{
	"web_search":       "openrouter:web_search",
	"datetime":         "openrouter:datetime",
	"web_fetch":        "openrouter:web_fetch",
	"image_generation": "openrouter:image_generation",
}

func normalizeOpenRouterTool(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	if name == "" {
		return "", fmt.Errorf("tool is required for openrouter_tool")
	}
	if llm.IsOpenRouterToolType(name) {
		for _, full := range openRouterToolTypes {
			if full == name {
				return name, nil
			}
		}
		return "", fmt.Errorf("unsupported openrouter tool type %q", name)
	}
	full, ok := openRouterToolTypes[name]
	if !ok {
		return "", fmt.Errorf("unsupported openrouter tool %q (supported: web_search, datetime, web_fetch, image_generation)", name)
	}
	return full, nil
}

func (e MCPServerEntry) openRouterToolDefinition() (llm.ToolDefinition, error) {
	toolType, err := normalizeOpenRouterTool(e.Tool)
	if err != nil {
		return llm.ToolDefinition{}, err
	}
	def := llm.ToolDefinition{Type: toolType}
	if len(e.Parameters) > 0 {
		def.Parameters = e.Parameters
	}
	return def, nil
}
