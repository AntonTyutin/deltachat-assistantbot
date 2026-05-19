package main

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"testing"

	"assistantbot/internal/version"
)

func TestLoggerIncludesVersionField(t *testing.T) {
	orig := version.Version
	version.Version = "2026.3.1"
	t.Cleanup(func() { version.Version = orig })

	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil)).With("version", version.Version)
	logger.Info("test message", "key", "value")

	var record map[string]any
	if err := json.Unmarshal(buf.Bytes(), &record); err != nil {
		t.Fatalf("unmarshal log record: %v", err)
	}
	if got, _ := record["version"].(string); got != "2026.3.1" {
		t.Fatalf("version = %q, want 2026.3.1", got)
	}
}
