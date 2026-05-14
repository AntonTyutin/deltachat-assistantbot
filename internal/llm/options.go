package llm

import "assistantbot/internal/metrics"

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
