package llm

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/AntonTyutin/assistantbot-core/metrics"
)

type recordingEmbeddingMetrics struct {
	calls []struct {
		purpose      string
		model        string
		apiErr       error
		outcome      string
		promptTokens int
		totalTokens  int
		recordedDur  bool
	}
}

func (r *recordingEmbeddingMetrics) RecordChatCompletion(string, string, string, time.Duration, error, string, int, int, int) {
}
func (r *recordingEmbeddingMetrics) RecordLLMLogicalFailure(string, string, string, string) {}
func (r *recordingEmbeddingMetrics) RecordMCPTool(string, string, string, time.Duration)    {}
func (r *recordingEmbeddingMetrics) RecordReplyGenerate(string, time.Duration)              {}
func (r *recordingEmbeddingMetrics) RecordMessagePhase(string, time.Duration)               {}
func (r *recordingEmbeddingMetrics) RecordInboundMessageHandle(string, time.Duration)       {}
func (r *recordingEmbeddingMetrics) RecordChatMemoryQueueDepth(string, int)                 {}
func (r *recordingEmbeddingMetrics) RecordChatMemoryQueueWait(string, time.Duration)        {}
func (r *recordingEmbeddingMetrics) RecordChatMemoryTaskDuration(string, time.Duration)     {}
func (r *recordingEmbeddingMetrics) RecordReplyToolCall(string, string, string, time.Duration) {
}
func (r *recordingEmbeddingMetrics) RecordInboundMessageToolCallCount(string, int) {}
func (r *recordingEmbeddingMetrics) RecordPromptPartBytes(string, string, int)     {}
func (r *recordingEmbeddingMetrics) RecordEmbedding(purpose, model string, dur time.Duration, apiErr error, responseOutcome string, promptTokens, totalTokens int) {
	r.calls = append(r.calls, struct {
		purpose      string
		model        string
		apiErr       error
		outcome      string
		promptTokens int
		totalTokens  int
		recordedDur  bool
	}{
		purpose:      purpose,
		model:        model,
		apiErr:       apiErr,
		outcome:      responseOutcome,
		promptTokens: promptTokens,
		totalTokens:  totalTokens,
		recordedDur:  dur > 0,
	})
}

func TestOpenAICompatibleEmbedderRecordsMetrics(t *testing.T) {
	rec := &recordingEmbeddingMetrics{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/embeddings" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{
				{"embedding": []float32{0.1, 0.2}},
			},
			"usage": map[string]any{
				"prompt_tokens": 12,
				"total_tokens":  12,
			},
		})
	}))
	t.Cleanup(srv.Close)

	embedder := NewOpenAICompatibleEmbedder(
		srv.URL,
		"test-key",
		"embed-model",
		2,
		time.Second,
		WithEmbedderRecorder(rec),
	)
	ctx := ContextWithEmbeddingPurpose(t.Context(), EmbeddingPurposeMessage)
	vectors, err := embedder.Embed(ctx, "hello")
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(vectors) != 1 || len(vectors[0]) != 2 {
		t.Fatalf("vectors = %#v", vectors)
	}
	if len(rec.calls) != 1 {
		t.Fatalf("metric calls = %d", len(rec.calls))
	}
	call := rec.calls[0]
	if call.purpose != EmbeddingPurposeMessage || call.model != "embed-model" || call.outcome != "success" {
		t.Fatalf("call = %#v", call)
	}
	if call.promptTokens != 12 || call.totalTokens != 12 || !call.recordedDur {
		t.Fatalf("tokens/dur = %#v", call)
	}
}

func TestOpenAICompatibleEmbedderRecordsHTTPFailure(t *testing.T) {
	rec := &recordingEmbeddingMetrics{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte("rate limited"))
	}))
	t.Cleanup(srv.Close)

	embedder := NewOpenAICompatibleEmbedder(srv.URL, "test-key", "embed-model", 2, time.Second, WithEmbedderRecorder(rec))
	_, err := embedder.Embed(t.Context(), "hello")
	if err == nil {
		t.Fatal("expected error")
	}
	if len(rec.calls) != 1 || rec.calls[0].apiErr == nil {
		t.Fatalf("calls = %#v", rec.calls)
	}
}

func TestEmbeddingPurposeFromContextDefaultsEmpty(t *testing.T) {
	rec := &recordingEmbeddingMetrics{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{{"embedding": []float32{0.1}}},
		})
	}))
	t.Cleanup(srv.Close)

	embedder := NewOpenAICompatibleEmbedder(srv.URL, "test-key", "embed-model", 1, time.Second, WithEmbedderRecorder(rec))
	if _, err := embedder.Embed(t.Context(), "x"); err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if rec.calls[0].purpose != "" {
		t.Fatalf("purpose = %q, want empty (Prometheus maps to unknown)", rec.calls[0].purpose)
	}
}

func TestContextWithEmbeddingPurpose(t *testing.T) {
	ctx := ContextWithEmbeddingPurpose(t.Context(), EmbeddingPurposeList)
	if got := embeddingPurposeFromContext(ctx); got != EmbeddingPurposeList {
		t.Fatalf("purpose = %q", got)
	}
}

var _ metrics.Recorder = (*recordingEmbeddingMetrics)(nil)
