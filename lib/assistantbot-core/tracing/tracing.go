// Package tracing carries a per-run trace identifier through context and injects it
// into every slog record, so logs across the app, reply, memory and llm layers can be
// correlated even though each layer holds its own logger.
package tracing

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"strconv"
	"time"
)

type ctxKey int

const (
	traceIDKey ctxKey = iota
	parentTraceIDKey
)

// NewID returns a short random hex identifier for a processing run.
func NewID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 16)
	}
	return hex.EncodeToString(b[:])
}

// WithTraceID returns a context carrying the given trace ID.
func WithTraceID(ctx context.Context, id string) context.Context {
	if id == "" {
		return ctx
	}
	return context.WithValue(ctx, traceIDKey, id)
}

// TraceID returns the trace ID stored in ctx, or "" if none.
func TraceID(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	id, _ := ctx.Value(traceIDKey).(string)
	return id
}

// WithParentTraceID links a derived run (e.g. a background task) to the run that spawned it.
func WithParentTraceID(ctx context.Context, id string) context.Context {
	if id == "" {
		return ctx
	}
	return context.WithValue(ctx, parentTraceIDKey, id)
}

// ParentTraceID returns the parent trace ID stored in ctx, or "" if none.
func ParentTraceID(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	id, _ := ctx.Value(parentTraceIDKey).(string)
	return id
}

// NewHandler wraps a slog.Handler so that trace_id and parent_trace_id from the
// context are added to every record emitted through *Context log methods.
func NewHandler(inner slog.Handler) slog.Handler {
	return &handler{inner: inner}
}

type handler struct {
	inner slog.Handler
}

func (h *handler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

func (h *handler) Handle(ctx context.Context, rec slog.Record) error {
	if id := TraceID(ctx); id != "" {
		rec.AddAttrs(slog.String("trace_id", id))
	}
	if pid := ParentTraceID(ctx); pid != "" {
		rec.AddAttrs(slog.String("parent_trace_id", pid))
	}
	return h.inner.Handle(ctx, rec)
}

func (h *handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &handler{inner: h.inner.WithAttrs(attrs)}
}

func (h *handler) WithGroup(name string) slog.Handler {
	return &handler{inner: h.inner.WithGroup(name)}
}
