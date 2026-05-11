package main

import (
	"log/slog"
	"os"
)

// Application logs go to stderr so Docker shows them immediately (stdout is block-buffered
// in many container setups and is reserved for CLI output).
func newLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(syncWriter{os.Stderr}, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
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
