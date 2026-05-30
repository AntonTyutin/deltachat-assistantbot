package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	openai "github.com/sashabaranov/go-openai"

	"github.com/AntonTyutin/assistantbot-core/llm/prompts"
	"github.com/AntonTyutin/assistantbot-core/metrics"
)

type ToolExecutorFunc func(ctx context.Context, toolName string, argumentsJSON string) (string, error)

type Client interface {
	CompleteJSON(ctx context.Context, task string, input any, schema string) (json.RawMessage, error)
	ChatWithTools(ctx context.Context, task string, messages []openai.ChatCompletionMessage, tools []ToolDefinition, exec ToolExecutorFunc) (string, error)
}

type OpenRouterClient struct {
	defaultModels          []string
	taskModels             map[string][]string
	taskCompletionTokens   map[string]int
	maxCompletionTokens    int
	retryBackoffMultiplier float64
	baseURL                string
	apiKey                 string
	httpClient             *http.Client
	client                 *openai.Client
	logger                 *slog.Logger
	recorder               metrics.Recorder
	prompts                *prompts.Registry
}

func NewOpenRouterClient(baseURL, apiKey, defaultModel string, taskModels map[string]string, taskCompletionTokens map[string]int, timeout time.Duration, maxCompletionTokens int, logger *slog.Logger, opts ...OpenRouterOption) *OpenRouterClient {
	if logger == nil {
		logger = slog.Default()
	}
	if maxCompletionTokens <= 0 {
		maxCompletionTokens = 2048
	}
	config := openai.DefaultConfig(apiKey)
	trimmedURL := strings.TrimRight(baseURL, "/")
	config.BaseURL = trimmedURL
	httpClient := &http.Client{Timeout: timeout}
	config.HTTPClient = httpClient

	c := &OpenRouterClient{
		defaultModels:          parseModelList(defaultModel),
		taskModels:             cloneTaskModels(taskModels),
		taskCompletionTokens:   cloneTaskCompletionTokens(taskCompletionTokens),
		maxCompletionTokens:    maxCompletionTokens,
		retryBackoffMultiplier: defaultRetryBackoffMultiplier,
		baseURL:                trimmedURL,
		apiKey:                 apiKey,
		httpClient:             httpClient,
		client:                 openai.NewClientWithConfig(config),
		logger:                 logger,
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
func (c *OpenRouterClient) ChatWithTools(ctx context.Context, task string, messages []openai.ChatCompletionMessage, tools []ToolDefinition, exec ToolExecutorFunc) (string, error) {
	if len(tools) == 0 {
		return "", fmt.Errorf("no tools provided for chat with tools")
	}
	if exec == nil {
		return "", fmt.Errorf("nil tool executor")
	}
	maxTokens := c.maxCompletionTokensForTask(task)
	var text string
	err := c.withModelRetry(ctx, task, func(ctx context.Context, model string) error {
		var attemptErr error
		text, attemptErr = c.chatWithToolsForModel(ctx, task, model, maxTokens, messages, tools, exec)
		return attemptErr
	})
	if err != nil {
		return "", err
	}
	return text, nil
}

type chatCompletionRequestBody struct {
	Model               string                         `json:"model"`
	Messages            []openai.ChatCompletionMessage `json:"messages"`
	Tools               []ToolDefinition               `json:"tools,omitempty"`
	Temperature         float32                        `json:"temperature"`
	MaxCompletionTokens int                            `json:"max_completion_tokens,omitempty"`
}

func (c *OpenRouterClient) createChatCompletionWithTools(ctx context.Context, body chatCompletionRequestBody) (openai.ChatCompletionResponse, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return openai.ChatCompletionResponse{}, fmt.Errorf("marshal chat completion request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(raw))
	if err != nil {
		return openai.ChatCompletionResponse{}, err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return openai.ChatCompletionResponse{}, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return openai.ChatCompletionResponse{}, err
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return openai.ChatCompletionResponse{}, fmt.Errorf("chat completion http %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	var out openai.ChatCompletionResponse
	if err := json.Unmarshal(respBody, &out); err != nil {
		return openai.ChatCompletionResponse{}, fmt.Errorf("decode chat completion response: %w", err)
	}
	return out, nil
}

func (c *OpenRouterClient) chatWithToolsForModel(ctx context.Context, task, model string, maxTokens int, base []openai.ChatCompletionMessage, tools []ToolDefinition, exec ToolExecutorFunc) (string, error) {
	msgs := append([]openai.ChatCompletionMessage{}, base...)

	for step := 0; step < maxToolOrChatSteps; step++ {
		body := chatCompletionRequestBody{
			Model:               model,
			Messages:            msgs,
			Tools:               tools,
			Temperature:         0.2,
			MaxCompletionTokens: maxTokens,
		}
		c.debugLogToolChatRequest(ctx, task, model, metrics.MethodChatCompletion, step, body)
		start := time.Now()
		resp, err := c.createChatCompletionWithTools(ctx, body)
		dur := time.Since(start)
		if err != nil {
			c.mrec().RecordChatCompletion(task, model, metrics.MethodChatCompletion, dur, err, "", 0, 0, 0)
			return "", err
		}
		c.debugLogChatResponse(ctx, task, model, metrics.MethodChatCompletion, step, resp)
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

	var raw json.RawMessage
	err = c.withModelRetry(ctx, task, func(ctx context.Context, model string) error {
		var attemptErr error
		raw, attemptErr = c.completeJSONWithModel(ctx, model, task, schema, inputJSON, c.maxCompletionTokensForTask(task))
		return attemptErr
	})
	if err != nil {
		return nil, err
	}
	return raw, nil
}

func (c *OpenRouterClient) completeJSONWithModel(ctx context.Context, model, task, schema string, inputJSON []byte, maxCompletionTokens int) (json.RawMessage, error) {
	if c.prompts == nil {
		return nil, fmt.Errorf("llm prompts not configured")
	}
	req := openai.ChatCompletionRequest{
		Model: model,
		Messages: []openai.ChatCompletionMessage{
			{
				Role:    openai.ChatMessageRoleSystem,
				Content: c.prompts.SystemPrompt(task),
			},
			{
				Role:    openai.ChatMessageRoleUser,
				Content: fmt.Sprintf("Task: %s\nSchema: %s\nInput: %s", task, schema, inputJSON),
			},
		},
		Temperature:         0.2,
		MaxCompletionTokens: maxCompletionTokens,
	}
	c.debugLogChatRequest(ctx, task, model, metrics.MethodCompleteJSON, 0, req)
	start := time.Now()
	resp, err := c.client.CreateChatCompletion(ctx, req)
	dur := time.Since(start)
	if err != nil {
		c.mrec().RecordChatCompletion(task, model, metrics.MethodCompleteJSON, dur, err, "", 0, 0, 0)
		return nil, err
	}
	c.debugLogChatResponse(ctx, task, model, metrics.MethodCompleteJSON, 0, resp)
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
