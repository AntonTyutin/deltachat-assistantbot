package main

import (
	"io"
	"log/slog"
	"os"

	"github.com/mattn/go-isatty"
)

// Application logs go to stderr in service mode (no TTY) so Docker captures them
// immediately. Interactive CLI commands keep stderr for user-facing errors only.
func newLogger() *slog.Logger {
	var w io.Writer = io.Discard
	if serviceLogging() {
		w = syncWriter{os.Stderr}
	}
	return slog.New(slog.NewJSONHandler(w, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
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
