package memory

import (
	"context"
	"fmt"
	"strings"

	"github.com/AntonTyutin/assistantbot-core/llm"
)

type CompositeToolRuntime struct {
	mcp    mcpToolRuntime
	memory *ToolRegistry
	chatID string
	userID string
}

func NewCompositeToolRuntime(mcp mcpToolRuntime, memory *ToolRegistry, chatID, userID string) *CompositeToolRuntime {
	return &CompositeToolRuntime{mcp: mcp, memory: memory, chatID: chatID, userID: userID}
}

func (c *CompositeToolRuntime) ToolsForTask(task string) []llm.ToolDefinition {
	var tools []llm.ToolDefinition
	if c.mcp != nil {
		tools = append(tools, c.mcp.ToolsForTask(task)...)
	}
	if c.memory != nil {
		tools = append(tools, c.memory.ToolsForTask(task)...)
	}
	return tools
}

func (c *CompositeToolRuntime) HasToolsForTask(task string) bool {
	return len(c.ToolsForTask(task)) > 0
}

func (c *CompositeToolRuntime) SystemPromptAppendForTask(task string) string {
	var parts []string
	if c.mcp != nil {
		if text := strings.TrimSpace(c.mcp.SystemPromptAppendForTask(task)); text != "" {
			parts = append(parts, text)
		}
	}
	if c.memory != nil {
		if text := strings.TrimSpace(c.memory.SystemPromptAppendForTask(task)); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n")
}

func (c *CompositeToolRuntime) ExecuteTool(ctx context.Context, toolName string, argumentsJSON string) (string, error) {
	if strings.HasPrefix(toolName, "memory_") {
		if c.memory == nil {
			return "", fmt.Errorf("memory tools unavailable")
		}
		return c.memory.ExecuteTool(ctx, c.chatID, c.userID, toolName, argumentsJSON)
	}
	if c.mcp == nil {
		return "", fmt.Errorf("unknown tool %q", toolName)
	}
	return c.mcp.ExecuteTool(ctx, toolName, argumentsJSON)
}
