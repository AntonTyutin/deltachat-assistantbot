package llm

import (
	"context"
	"log/slog"

	openai "github.com/sashabaranov/go-openai"
)

// debugEnabled guards the request/response body logging below: when debug is off we
// skip building the log record (and the JSON serialization slog would do for the bodies).
func (c *OpenRouterClient) debugEnabled(ctx context.Context) bool {
	return c.logger != nil && c.logger.Enabled(ctx, slog.LevelDebug)
}

func (c *OpenRouterClient) debugLogChatRequest(ctx context.Context, task, model, method string, step int, req openai.ChatCompletionRequest) {
	if !c.debugEnabled(ctx) {
		return
	}
	attrs := []any{
		"task", task,
		"model", model,
		"method", method,
		"step", step,
		"max_completion_tokens", req.MaxCompletionTokens,
		"messages", req.Messages,
	}
	if len(req.Tools) > 0 {
		attrs = append(attrs, "tools", req.Tools)
	}
	c.logger.DebugContext(ctx, "llm request", attrs...)
}

func (c *OpenRouterClient) debugLogChatResponse(ctx context.Context, task, model, method string, step int, resp openai.ChatCompletionResponse) {
	if !c.debugEnabled(ctx) {
		return
	}
	attrs := []any{
		"task", task,
		"model", model,
		"method", method,
		"step", step,
		"prompt_tokens", resp.Usage.PromptTokens,
		"completion_tokens", resp.Usage.CompletionTokens,
		"total_tokens", resp.Usage.TotalTokens,
	}
	if len(resp.Choices) > 0 {
		choice := resp.Choices[0].Message
		attrs = append(attrs, "content", choice.Content)
		if len(choice.ToolCalls) > 0 {
			attrs = append(attrs, "tool_calls", choice.ToolCalls)
		}
	}
	c.logger.DebugContext(ctx, "llm response", attrs...)
}
