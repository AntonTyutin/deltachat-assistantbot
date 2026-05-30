package main

import (
	"io"
	"log/slog"
	"os"

	"github.com/AntonTyutin/assistantbot-core/tracing"
	"github.com/mattn/go-isatty"

	"assistantbot/internal/config"
	"assistantbot/internal/version"
)

// Application logs go to stderr in service mode (no TTY) so Docker captures them
// immediately. Interactive CLI commands keep stderr for user-facing errors only.
func newLogger() *slog.Logger {
	level := slog.LevelInfo
	if config.AppDebug() {
		level = slog.LevelDebug
	}
	var w io.Writer = io.Discard
	if serviceLogging() || config.AppDebug() {
		w = syncWriter{os.Stderr}
	}
	handler := tracing.NewHandler(slog.NewJSONHandler(w, &slog.HandlerOptions{
		Level: level,
	}))
	return slog.New(handler).With("version", version.Version)
}

func serviceLogging() bool {
	return !isatty.IsTerminal(os.Stdin.Fd()) && !isatty.IsTerminal(os.Stdout.Fd())
}

type syncWriter struct {
	f *os.File
}

func (w syncWriter) Write(p []byte) (int, error) {
	n, err := w.f.Write(p)
	if err == nil {
		_ = w.f.Sync()
	}
	return n, err
}
