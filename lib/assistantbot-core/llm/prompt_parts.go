package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/AntonTyutin/assistantbot-core/metrics"
)

type promptPartsContextKey struct{}

// PromptParts holds UTF-8 byte sizes of the initial prompt components for one LLM call.
type PromptParts struct {
	System           int
	Tools            int // system_prompt_append from MCP and memory tools combined
	ToolsDefinitions int // serialized tool definitions (MCP + memory)
	User             int
}

// NewPromptParts measures prompt component sizes. baseSystem is the task prompt from the
// prompts registry; mcpAppend and memoryAppend are system guidance from tool integrations.
func NewPromptParts(baseSystem, mcpAppend, memoryAppend, user string, toolDefs []ToolDefinition) PromptParts {
	return PromptParts{
		System:           len(baseSystem),
		Tools:            toolSystemAppendByteSize(mcpAppend, memoryAppend),
		ToolsDefinitions: ToolDefinitionsByteSize(toolDefs),
		User:             len(user),
	}
}

func toolSystemAppendByteSize(mcpAppend, memoryAppend string) int {
	var parts []string
	if text := strings.TrimSpace(mcpAppend); text != "" {
		parts = append(parts, text)
	}
	if text := strings.TrimSpace(memoryAppend); text != "" {
		parts = append(parts, text)
	}
	if len(parts) == 0 {
		return 0
	}
	return len(strings.Join(parts, "\n"))
}

// ContextWithPromptParts attaches sizes to ctx for OpenRouterClient to record on the first HTTP request.
func ContextWithPromptParts(ctx context.Context, parts PromptParts) context.Context {
	return context.WithValue(ctx, promptPartsContextKey{}, parts)
}

// PromptPartsFromContext returns sizes attached with ContextWithPromptParts.
func PromptPartsFromContext(ctx context.Context) (PromptParts, bool) {
	if ctx == nil {
		return PromptParts{}, false
	}
	parts, ok := ctx.Value(promptPartsContextKey{}).(PromptParts)
	return parts, ok
}

// Record emits prompt part size histograms for the given task.
func (p PromptParts) Record(rec metrics.Recorder, task string) {
	if rec == nil {
		return
	}
	if p.System > 0 {
		rec.RecordPromptPartBytes(task, metrics.PromptPartSystem, p.System)
	}
	if p.Tools > 0 {
		rec.RecordPromptPartBytes(task, metrics.PromptPartTools, p.Tools)
	}
	if p.ToolsDefinitions > 0 {
		rec.RecordPromptPartBytes(task, metrics.PromptPartToolsDefinitions, p.ToolsDefinitions)
	}
	if p.User > 0 {
		rec.RecordPromptPartBytes(task, metrics.PromptPartUser, p.User)
	}
}

// ToolDefinitionsByteSize sums serialized JSON size of each tool definition as sent to the API.
func ToolDefinitionsByteSize(tools []ToolDefinition) int {
	total := 0
	for _, t := range tools {
		raw, err := json.Marshal(t)
		if err != nil {
			continue
		}
		total += len(raw)
	}
	return total
}

// CompleteJSONUserPrompt builds the user message for CompleteJSON calls.
func CompleteJSONUserPrompt(task, schema string, inputJSON []byte) string {
	return fmt.Sprintf("Task: %s\nSchema: %s\nInput: %s", task, schema, string(inputJSON))
}

// PromptPartsForCompleteJSON builds sizes for a CompleteJSON request.
func PromptPartsForCompleteJSON(systemPrompt, userPrompt string) PromptParts {
	return PromptParts{
		System: len(systemPrompt),
		User:   len(userPrompt),
	}
}
