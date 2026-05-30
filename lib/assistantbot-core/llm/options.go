package llm

import (
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
