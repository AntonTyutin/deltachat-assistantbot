package llm

import (
	"context"
	"encoding/json"
	"fmt"

	openai "github.com/sashabaranov/go-openai"
)

type StaticClient struct {
	Responses map[string]json.RawMessage
}

func (c StaticClient) CompleteJSON(_ context.Context, task string, _ any, _ string) (json.RawMessage, error) {
	if value, ok := c.Responses[task]; ok {
		return value, nil
	}
	return json.RawMessage(`{}`), nil
}

func (StaticClient) ChatWithTools(context.Context, string, []openai.ChatCompletionMessage, []ToolDefinition, ToolExecutorFunc) (string, error) {
	return "", fmt.Errorf("ChatWithTools not supported by StaticClient")
}
