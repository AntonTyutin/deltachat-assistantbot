package llm

import (
	"log/slog"

	"github.com/AntonTyutin/assistantbot-core/llm/prompts"
	"github.com/AntonTyutin/assistantbot-core/metrics"
)

// OpenRouterOption configures [OpenRouterClient].
type OpenRouterOption func(*OpenRouterClient)

// WithRecorder attaches a metrics recorder (e.g. [metrics.Prometheus]). Nil is ignored.
func WithRecorder(r metrics.Recorder) OpenRouterOption {
	return func(c *OpenRouterClient) {
		if r != nil {
			c.recorder = r
		}
	}
}

// WithRetryBackoffMultiplier sets the exponential backoff factor between LLM retry attempts.
// Values below 1 are ignored; the client uses 2.0 when unset.
func WithRetryBackoffMultiplier(multiplier float64) OpenRouterOption {
	return func(c *OpenRouterClient) {
		if multiplier >= 1 {
			c.retryBackoffMultiplier = multiplier
		}
	}
}

// WithPrompts sets the system prompt registry (required for production use).
func WithPrompts(reg *prompts.Registry) OpenRouterOption {
	return func(c *OpenRouterClient) {
		if reg != nil {
			c.prompts = reg
		}
	}
}

// EmbedderOption configures [OpenAICompatibleEmbedder].
type EmbedderOption func(*OpenAICompatibleEmbedder)

// WithEmbedderRecorder attaches a metrics recorder (e.g. [metrics.Prometheus]). Nil is ignored.
func WithEmbedderRecorder(r metrics.Recorder) EmbedderOption {
	return func(e *OpenAICompatibleEmbedder) {
		if r != nil {
			e.recorder = r
		}
	}
}

// WithEmbedderLogger sets the logger for embedding requests. Nil is ignored.
func WithEmbedderLogger(logger *slog.Logger) EmbedderOption {
	return func(e *OpenAICompatibleEmbedder) {
		if logger != nil {
			e.logger = logger
		}
	}
}
