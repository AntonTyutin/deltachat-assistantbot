package app

import (
	"context"
	"testing"
	"time"

	"github.com/AntonTyutin/assistantbot-core/transport"
)

func TestMessageProcessorUpdateCancelsInFlight(t *testing.T) {
	p := newMessageProcessor()
	msg := transport.Message{ID: "1", ChatID: "10", Text: "hello"}
	runCtx, release, st := p.begin(context.Background(), msg)
	defer release()

	replyCtx := p.replyContext(runCtx, st)
	done := make(chan struct{})
	go func() {
		<-replyCtx.Done()
		close(done)
	}()

	updated := transport.Message{ID: "1", ChatID: "10", Text: "hello world"}
	p.onUpdated(updated)

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("expected reply context to be cancelled on update")
	}
	if runCtx.Err() != nil {
		t.Fatal("update should not cancel the overall processing context")
	}

	got, rev, deleted := p.snapshot(st)
	if deleted {
		t.Fatal("message should not be marked deleted")
	}
	if got.Text != "hello world" {
		t.Fatalf("expected updated text, got %q", got.Text)
	}
	if rev != 1 {
		t.Fatalf("expected revision 1, got %d", rev)
	}
}

func TestMessageProcessorDeleteCancelsInFlight(t *testing.T) {
	p := newMessageProcessor()
	msg := transport.Message{ID: "1", ChatID: "10", Text: "hello"}
	ctx, release, _ := p.begin(context.Background(), msg)
	defer release()

	done := make(chan struct{})
	go func() {
		<-ctx.Done()
		close(done)
	}()

	p.onDeleted("10", "1")

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("expected in-flight context to be cancelled on delete")
	}
	if !errorsIsDeleted(context.Cause(ctx)) {
		t.Fatalf("expected delete cause, got %v", context.Cause(ctx))
	}
}

func errorsIsDeleted(err error) bool {
	return err == errMessageDeleted
}

func TestMessageProcessorUpdateCancelsMemoryAttempt(t *testing.T) {
	p := newMessageProcessor()
	msg := transport.Message{ID: "1", ChatID: "10", Text: "hello"}
	runCtx, release, st := p.begin(context.Background(), msg)
	defer release()

	memoryCtx := p.memoryContext(runCtx, st)
	done := make(chan struct{})
	go func() {
		<-memoryCtx.Done()
		close(done)
	}()

	updated := transport.Message{ID: "1", ChatID: "10", Text: "edited"}
	p.onUpdated(updated)

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("expected memory context to be cancelled on update")
	}
	if runCtx.Err() != nil {
		t.Fatal("update should not cancel the overall processing context")
	}

	got, rev, _ := p.snapshot(st)
	if got.Text != "edited" || rev != 1 {
		t.Fatalf("update should apply after memory cancellation: %+v rev=%d", got, rev)
	}
}
