package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type Embedder interface {
	Embed(ctx context.Context, texts ...string) ([][]float32, error)
}

type OpenAICompatibleEmbedder struct {
	baseURL    string
	apiKey     string
	model      string
	dimensions int
	httpClient *http.Client
}

func NewOpenAICompatibleEmbedder(baseURL, apiKey, model string, dimensions int, timeout time.Duration) *OpenAICompatibleEmbedder {
	if dimensions <= 0 {
		dimensions = 1536
	}
	return &OpenAICompatibleEmbedder{
		baseURL:    strings.TrimRight(baseURL, "/"),
		apiKey:     apiKey,
		model:      model,
		dimensions: dimensions,
		httpClient: &http.Client{Timeout: timeout},
	}
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
}

func (e *OpenAICompatibleEmbedder) Embed(ctx context.Context, texts ...string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
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
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("embeddings http %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	var out embeddingResponse
	if err := json.Unmarshal(respBody, &out); err != nil {
		return nil, fmt.Errorf("decode embeddings response: %w", err)
	}
	if len(out.Data) != len(texts) {
		return nil, fmt.Errorf("embeddings count mismatch: got %d want %d", len(out.Data), len(texts))
	}
	vectors := make([][]float32, len(out.Data))
	for i, item := range out.Data {
		vectors[i] = item.Embedding
	}
	return vectors, nil
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
