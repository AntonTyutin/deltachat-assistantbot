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

	"github.com/AntonTyutin/assistantbot-core/metrics"
)

// Embedding purpose labels for metrics and logs (attach with ContextWithEmbeddingPurpose).
const (
	EmbeddingPurposeMessage      = "message"
	EmbeddingPurposeTopicSummary = "topic_summary"
	EmbeddingPurposeList         = "list"
)

type embeddingPurposeContextKey struct{}

// ContextWithEmbeddingPurpose attaches a metrics/logging purpose to ctx for [OpenAICompatibleEmbedder].
func ContextWithEmbeddingPurpose(ctx context.Context, purpose string) context.Context {
	return context.WithValue(ctx, embeddingPurposeContextKey{}, strings.TrimSpace(purpose))
}

func embeddingPurposeFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	purpose, _ := ctx.Value(embeddingPurposeContextKey{}).(string)
	return strings.TrimSpace(purpose)
}

type Embedder interface {
	Embed(ctx context.Context, texts ...string) ([][]float32, error)
}

type OpenAICompatibleEmbedder struct {
	baseURL    string
	apiKey     string
	model      string
	dimensions int
	httpClient *http.Client
	logger     *slog.Logger
	recorder   metrics.Recorder
}

func NewOpenAICompatibleEmbedder(baseURL, apiKey, model string, dimensions int, timeout time.Duration, opts ...EmbedderOption) *OpenAICompatibleEmbedder {
	if dimensions <= 0 {
		dimensions = 1536
	}
	e := &OpenAICompatibleEmbedder{
		baseURL:    strings.TrimRight(baseURL, "/"),
		apiKey:     apiKey,
		model:      model,
		dimensions: dimensions,
		httpClient: &http.Client{Timeout: timeout},
		logger:     slog.Default(),
	}
	for _, o := range opts {
		o(e)
	}
	return e
}

func (e *OpenAICompatibleEmbedder) mrec() metrics.Recorder {
	if e != nil && e.recorder != nil {
		return e.recorder
	}
	return metrics.Noop
}

type embeddingRequest struct {
	Model      string   `json:"model"`
	Input      []string `json:"input"`
	Dimensions int      `json:"dimensions,omitempty"`
}

type embeddingResponse struct {
	Data []struct {
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
	Usage struct {
		PromptTokens int `json:"prompt_tokens"`
		TotalTokens  int `json:"total_tokens"`
	} `json:"usage"`
}

func (e *OpenAICompatibleEmbedder) Embed(ctx context.Context, texts ...string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	purpose := embeddingPurposeFromContext(ctx)
	e.debugLogEmbeddingRequest(ctx, purpose, texts)
	start := time.Now()
	body, err := json.Marshal(embeddingRequest{
		Model:      e.model,
		Input:      texts,
		Dimensions: e.dimensions,
	})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.baseURL+"/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+e.apiKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := e.httpClient.Do(req)
	dur := time.Since(start)
	if err != nil {
		e.mrec().RecordEmbedding(purpose, e.model, dur, err, "", 0, 0)
		e.logEmbeddingFailure(ctx, purpose, dur, err)
		return nil, err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		e.mrec().RecordEmbedding(purpose, e.model, dur, err, "", 0, 0)
		e.logEmbeddingFailure(ctx, purpose, dur, err)
		return nil, err
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		apiErr := fmt.Errorf("embeddings http %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
		e.mrec().RecordEmbedding(purpose, e.model, dur, apiErr, "", 0, 0)
		e.logEmbeddingFailure(ctx, purpose, dur, apiErr)
		return nil, apiErr
	}
	var out embeddingResponse
	if err := json.Unmarshal(respBody, &out); err != nil {
		decodeErr := fmt.Errorf("decode embeddings response: %w", err)
		e.mrec().RecordEmbedding(purpose, e.model, dur, nil, "decode_error", 0, 0)
		e.logEmbeddingFailure(ctx, purpose, dur, decodeErr)
		return nil, decodeErr
	}
	if len(out.Data) != len(texts) {
		countErr := fmt.Errorf("embeddings count mismatch: got %d want %d", len(out.Data), len(texts))
		e.mrec().RecordEmbedding(purpose, e.model, dur, nil, "count_mismatch", out.Usage.PromptTokens, out.Usage.TotalTokens)
		e.logEmbeddingFailure(ctx, purpose, dur, countErr)
		return nil, countErr
	}
	vectors := make([][]float32, len(out.Data))
	for i, item := range out.Data {
		vectors[i] = item.Embedding
	}
	e.mrec().RecordEmbedding(purpose, e.model, dur, nil, "success", out.Usage.PromptTokens, out.Usage.TotalTokens)
	e.debugLogEmbeddingResponse(ctx, purpose, dur, len(texts), out.Usage.PromptTokens, out.Usage.TotalTokens)
	return vectors, nil
}

func (e *OpenAICompatibleEmbedder) debugEnabled(ctx context.Context) bool {
	return e.logger != nil && e.logger.Enabled(ctx, slog.LevelDebug)
}

func (e *OpenAICompatibleEmbedder) debugLogEmbeddingRequest(ctx context.Context, purpose string, texts []string) {
	if !e.debugEnabled(ctx) {
		return
	}
	e.logger.DebugContext(ctx, "embedding request",
		"purpose", purpose,
		"model", e.model,
		"input_count", len(texts),
		"input_bytes", embeddingInputBytes(texts),
	)
}

func (e *OpenAICompatibleEmbedder) debugLogEmbeddingResponse(ctx context.Context, purpose string, dur time.Duration, inputCount, promptTokens, totalTokens int) {
	if !e.debugEnabled(ctx) {
		return
	}
	e.logger.DebugContext(ctx, "embedding response",
		"purpose", purpose,
		"model", e.model,
		"duration_ms", dur.Milliseconds(),
		"input_count", inputCount,
		"prompt_tokens", promptTokens,
		"total_tokens", totalTokens,
	)
}

func (e *OpenAICompatibleEmbedder) logEmbeddingFailure(ctx context.Context, purpose string, dur time.Duration, err error) {
	if e.logger == nil || err == nil {
		return
	}
	e.logger.WarnContext(ctx, "embedding request failed",
		"purpose", purpose,
		"model", e.model,
		"duration_ms", dur.Milliseconds(),
		"error", err,
	)
}

func embeddingInputBytes(texts []string) int {
	total := 0
	for _, text := range texts {
		total += len(text)
	}
	return total
}

type StaticEmbedder struct {
	Vector []float32
}

func (s StaticEmbedder) Embed(_ context.Context, texts ...string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i := range texts {
		out[i] = append([]float32(nil), s.Vector...)
	}
	return out, nil
}

// TextKeyedEmbedder returns a distinct vector per input text; useful in tests.
type TextKeyedEmbedder struct {
	Default []float32
	ByText  map[string][]float32
}

func (e TextKeyedEmbedder) Embed(_ context.Context, texts ...string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, text := range texts {
		if vec, ok := e.ByText[text]; ok {
			out[i] = append([]float32(nil), vec...)
			continue
		}
		out[i] = append([]float32(nil), e.Default...)
	}
	return out, nil
}

func MessageEmbeddingText(message string, parentText string) string {
	parentText = strings.TrimSpace(parentText)
	message = strings.TrimSpace(message)
	if parentText == "" {
		return message
	}
	return parentText + "\n" + message
}
