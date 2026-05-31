package llm

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"time"
)

// LogToolCall records tool failures at error level (without arguments) and, when debug
// logging is enabled, successful calls and failures with full arguments and results.
func LogToolCall(ctx context.Context, logger *slog.Logger, source, tool, argumentsJSON, result string, callErr error, dur time.Duration, extra ...any) {
	if logger == nil {
		return
	}
	if callErr != nil {
		attrs := []any{
			"source", source,
			"tool", tool,
			"error", callErr.Error(),
			"duration_ms", dur.Milliseconds(),
		}
		attrs = append(attrs, extra...)
		logger.ErrorContext(ctx, "tool call failed", attrs...)
	}
	if !logger.Enabled(ctx, slog.LevelDebug) {
		return
	}
	attrs := []any{
		"source", source,
		"tool", tool,
		"arguments", decodeToolLogPayload(argumentsJSON),
		"duration_ms", dur.Milliseconds(),
	}
	attrs = append(attrs, extra...)
	if strings.TrimSpace(result) != "" {
		attrs = append(attrs, "result", decodeToolLogPayload(result))
	}
	if callErr != nil {
		attrs = append(attrs, "error", callErr.Error())
		logger.DebugContext(ctx, "tool call details", attrs...)
		return
	}
	logger.DebugContext(ctx, "tool call", attrs...)
}

func decodeToolLogPayload(raw string) any {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return map[string]any{}
	}
	var decoded any
	if json.Unmarshal([]byte(raw), &decoded) == nil {
		return decoded
	}
	return raw
}
