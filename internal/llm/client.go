package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	openai "github.com/sashabaranov/go-openai"

	"assistantbot/internal/metrics"
)

type ToolExecutorFunc func(ctx context.Context, toolName string, argumentsJSON string) (string, error)

type Client interface {
	CompleteJSON(ctx context.Context, task string, input any, schema string) (json.RawMessage, error)
	ChatWithTools(ctx context.Context, task string, messages []openai.ChatCompletionMessage, tools []openai.Tool, exec ToolExecutorFunc) (string, error)
}

type OpenRouterClient struct {
	defaultModels        []string
	taskModels           map[string][]string
	taskCompletionTokens map[string]int
	maxCompletionTokens  int
	client               *openai.Client
	logger               *slog.Logger
	recorder             metrics.Recorder
}

func NewOpenRouterClient(baseURL, apiKey, defaultModel string, taskModels map[string]string, taskCompletionTokens map[string]int, timeout time.Duration, maxCompletionTokens int, logger *slog.Logger, opts ...OpenRouterOption) *OpenRouterClient {
	if logger == nil {
		logger = slog.Default()
	}
	if maxCompletionTokens <= 0 {
		maxCompletionTokens = 2048
	}
	config := openai.DefaultConfig(apiKey)
	config.BaseURL = strings.TrimRight(baseURL, "/")
	config.HTTPClient = &http.Client{
		Timeout: timeout,
	}

	c := &OpenRouterClient{
		defaultModels:        parseModelList(defaultModel),
		taskModels:           cloneTaskModels(taskModels),
		taskCompletionTokens: cloneTaskCompletionTokens(taskCompletionTokens),
		maxCompletionTokens:  maxCompletionTokens,
		client:               openai.NewClientWithConfig(config),
		logger:               logger,
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

func (c *OpenRouterClient) mrec() metrics.Recorder {
	if c != nil && c.recorder != nil {
		return c.recorder
	}
	return metrics.Noop
}

const maxToolOrChatSteps = 12

// ChatWithTools runs a multi-turn chat with tool calling until the model returns a final
// text message with no tool calls, or the step limit is hit.
func (c *OpenRouterClient) ChatWithTools(ctx context.Context, task string, messages []openai.ChatCompletionMessage, tools []openai.Tool, exec ToolExecutorFunc) (string, error) {
	if len(tools) == 0 {
		return "", fmt.Errorf("no tools provided for chat with tools")
	}
	if exec == nil {
		return "", fmt.Errorf("nil tool executor")
	}
	models := c.modelsForTask(task)
	if len(models) == 0 {
		return "", fmt.Errorf("no llm model configured for task %q", task)
	}
	var errs []error
	maxTokens := c.maxCompletionTokensForTask(task)
	for _, model := range models {
		c.logger.Info("llm chat+tools request started", "task", task, "model", model)
		text, err := c.chatWithToolsForModel(ctx, task, model, maxTokens, messages, tools, exec)
		if err == nil {
			c.logger.Info("llm chat+tools request succeeded", "task", task, "model", model)
			return text, nil
		}
		c.logger.Warn("llm chat+tools failed, trying fallback if available", "task", task, "model", model, "error", err)
		errs = append(errs, fmt.Errorf("%s: %w", model, err))
	}
	return "", fmt.Errorf("all llm model attempts failed for task %q: %w", task, errors.Join(errs...))
}

func (c *OpenRouterClient) chatWithToolsForModel(ctx context.Context, task, model string, maxTokens int, base []openai.ChatCompletionMessage, tools []openai.Tool, exec ToolExecutorFunc) (string, error) {
	msgs := append([]openai.ChatCompletionMessage{}, base...)

	for step := 0; step < maxToolOrChatSteps; step++ {
		start := time.Now()
		resp, err := c.client.CreateChatCompletion(ctx, openai.ChatCompletionRequest{
			Model:               model,
			Messages:            msgs,
			Tools:               tools,
			Temperature:         0.2,
			MaxCompletionTokens: maxTokens,
		})
		dur := time.Since(start)
		if err != nil {
			c.mrec().RecordChatCompletion(task, model, metrics.MethodChatCompletion, dur, err, "", 0, 0, 0)
			return "", err
		}
		u := resp.Usage
		if len(resp.Choices) == 0 {
			c.mrec().RecordChatCompletion(task, model, metrics.MethodChatCompletion, dur, nil, "empty_choices", u.PromptTokens, u.CompletionTokens, u.TotalTokens)
			return "", fmt.Errorf("llm response has no choices")
		}

		choice := resp.Choices[0].Message
		if len(choice.ToolCalls) == 0 {
			c.mrec().RecordChatCompletion(task, model, metrics.MethodChatCompletion, dur, nil, "success", u.PromptTokens, u.CompletionTokens, u.TotalTokens)
			return strings.TrimSpace(choice.Content), nil
		}

		c.mrec().RecordChatCompletion(task, model, metrics.MethodChatCompletion, dur, nil, "success", u.PromptTokens, u.CompletionTokens, u.TotalTokens)

		msgs = append(msgs, choice)

		for _, tc := range choice.ToolCalls {
			args := tc.Function.Arguments
			result, err := exec(ctx, tc.Function.Name, args)
			if err != nil {
				result = fmt.Sprintf("error calling tool: %v", err)
			}
			msgs = append(msgs, openai.ChatCompletionMessage{
				Role:       openai.ChatMessageRoleTool,
				ToolCallID: tc.ID,
				Name:       tc.Function.Name,
				Content:    result,
			})
		}
	}
	c.mrec().RecordLLMLogicalFailure(task, model, metrics.MethodChatCompletion, metrics.OutcomeToolLoopLimit)
	return "", fmt.Errorf("tool loop exceeded %d steps", maxToolOrChatSteps)
}

func (c *OpenRouterClient) CompleteJSON(ctx context.Context, task string, input any, schema string) (json.RawMessage, error) {
	inputJSON, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}

	models := c.modelsForTask(task)
	if len(models) == 0 {
		return nil, fmt.Errorf("no llm model configured for task %q", task)
	}
	var errs []error
	for _, model := range models {
		c.logger.Info("llm request started", "task", task, "model", model)
		raw, err := c.completeJSONWithModel(ctx, model, task, schema, inputJSON, c.maxCompletionTokensForTask(task))
		if err == nil {
			c.logger.Info("llm request succeeded", "task", task, "model", model)
			return raw, nil
		}
		c.logger.Warn("llm request failed, trying fallback if available", "task", task, "model", model, "error", err)
		errs = append(errs, fmt.Errorf("%s: %w", model, err))
	}
	finalErr := fmt.Errorf("all llm model attempts failed for task %q: %w", task, errors.Join(errs...))
	c.logger.Error("llm request failed for all configured models", "task", task, "models", models, "error", finalErr)
	return nil, finalErr
}

func (c *OpenRouterClient) completeJSONWithModel(ctx context.Context, model, task, schema string, inputJSON []byte, maxCompletionTokens int) (json.RawMessage, error) {
	start := time.Now()
	resp, err := c.client.CreateChatCompletion(ctx, openai.ChatCompletionRequest{
		Model: model,
		Messages: []openai.ChatCompletionMessage{
			{
				Role:    openai.ChatMessageRoleSystem,
				Content: "You are a group chat memory assistant. Return only valid JSON. When generating natural-language text, use the language of the chat context, including Russian when the chat is in Russian.",
			},
			{
				Role:    openai.ChatMessageRoleUser,
				Content: fmt.Sprintf("Task: %s\nSchema: %s\nInput: %s", task, schema, inputJSON),
			},
		},
		Temperature:         0.2,
		MaxCompletionTokens: maxCompletionTokens,
	})
	dur := time.Since(start)
	if err != nil {
		c.mrec().RecordChatCompletion(task, model, metrics.MethodCompleteJSON, dur, err, "", 0, 0, 0)
		return nil, err
	}
	u := resp.Usage
	if len(resp.Choices) == 0 {
		c.mrec().RecordChatCompletion(task, model, metrics.MethodCompleteJSON, dur, nil, "empty_choices", u.PromptTokens, u.CompletionTokens, u.TotalTokens)
		return nil, fmt.Errorf("llm response has no choices")
	}
	content := strings.TrimSpace(resp.Choices[0].Message.Content)
	content = strings.TrimPrefix(content, "```json")
	content = strings.TrimPrefix(content, "```")
	content = strings.TrimSuffix(content, "```")
	content = strings.TrimSpace(content)
	if !json.Valid([]byte(content)) {
		c.mrec().RecordChatCompletion(task, model, metrics.MethodCompleteJSON, dur, nil, "invalid_json", u.PromptTokens, u.CompletionTokens, u.TotalTokens)
		return nil, fmt.Errorf("llm returned invalid JSON")
	}
	c.mrec().RecordChatCompletion(task, model, metrics.MethodCompleteJSON, dur, nil, "success", u.PromptTokens, u.CompletionTokens, u.TotalTokens)
	return json.RawMessage(content), nil
}

func (c *OpenRouterClient) modelsForTask(task string) []string {
	if models := c.taskModels[strings.TrimSpace(task)]; len(models) > 0 {
		return append([]string(nil), models...)
	}
	return append([]string(nil), c.defaultModels...)
}

func cloneTaskModels(taskModels map[string]string) map[string][]string {
	cloned := make(map[string][]string, len(taskModels))
	for task, model := range taskModels {
		task = strings.TrimSpace(task)
		models := parseModelList(model)
		if task != "" && len(models) > 0 {
			cloned[task] = models
		}
	}
	return cloned
}

func cloneTaskCompletionTokens(taskCompletionTokens map[string]int) map[string]int {
	cloned := make(map[string]int, len(taskCompletionTokens))
	for task, tokens := range taskCompletionTokens {
		task = strings.TrimSpace(task)
		if task == "" || tokens <= 0 {
			continue
		}
		cloned[task] = tokens
	}
	return cloned
}

func (c *OpenRouterClient) maxCompletionTokensForTask(task string) int {
	if tokens := c.taskCompletionTokens[strings.TrimSpace(task)]; tokens > 0 {
		return tokens
	}
	return c.maxCompletionTokens
}

func parseModelList(raw string) []string {
	fields := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\n' || r == '\t'
	})
	models := make([]string, 0, len(fields))
	seen := map[string]struct{}{}
	for _, field := range fields {
		model := strings.TrimSpace(field)
		if model == "" {
			continue
		}
		if _, ok := seen[model]; ok {
			continue
		}
		seen[model] = struct{}{}
		models = append(models, model)
	}
	return models
}
