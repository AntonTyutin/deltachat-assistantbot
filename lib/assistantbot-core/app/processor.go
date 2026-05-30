package app

import (
	"context"
	"errors"
	"sync"

	"github.com/AntonTyutin/assistantbot-core/tracing"
	"github.com/AntonTyutin/assistantbot-core/transport"
)

var (
	errMessageDeleted = errors.New("message deleted")
	errMessageUpdated = errors.New("message updated")
)

type messageProcessor struct {
	states sync.Map // string -> *messageState
}

type messageState struct {
	mu sync.Mutex

	message  transport.Message
	revision uint64
	deleted  bool
	runToken uint64

	runCancel    context.CancelCauseFunc
	memoryCancel context.CancelCauseFunc
	replyCancel  context.CancelCauseFunc
}

func newMessageProcessor() *messageProcessor {
	return &messageProcessor{}
}

func messageKey(chatID, messageID string) string {
	return chatID + "\x00" + messageID
}

// begin starts processing for a message and returns a cancellable context.
// The release function must be called when processing finishes.
func (p *messageProcessor) begin(parent context.Context, message transport.Message) (context.Context, func(), *messageState) {
	key := messageKey(message.ChatID, message.ID)
	raw, _ := p.states.LoadOrStore(key, &messageState{})
	st := raw.(*messageState)

	st.mu.Lock()
	st.message = message
	st.deleted = false
	st.runToken++
	runToken := st.runToken
	if st.runCancel != nil {
		st.runCancel(errMessageUpdated)
	}
	if st.memoryCancel != nil {
		st.memoryCancel(errMessageUpdated)
	}
	if st.replyCancel != nil {
		st.replyCancel(errMessageUpdated)
	}
	parent = tracing.WithTraceID(parent, tracing.NewID())
	runCtx, runCancel := context.WithCancelCause(parent)
	st.runCancel = runCancel
	st.memoryCancel = nil
	st.replyCancel = nil
	st.mu.Unlock()

	release := func() {
		st.mu.Lock()
		if st.runToken != runToken {
			st.mu.Unlock()
			return
		}
		if st.memoryCancel != nil {
			st.memoryCancel(errMessageUpdated)
			st.memoryCancel = nil
		}
		if st.replyCancel != nil {
			st.replyCancel(errMessageUpdated)
			st.replyCancel = nil
		}
		st.runCancel = nil
		st.mu.Unlock()
		p.states.CompareAndDelete(key, st)
	}
	return runCtx, release, st
}

func (p *messageProcessor) onUpdated(message transport.Message) {
	key := messageKey(message.ChatID, message.ID)
	raw, ok := p.states.Load(key)
	if !ok {
		return
	}
	st := raw.(*messageState)
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.deleted {
		return
	}
	st.message = message
	st.revision++
	if st.memoryCancel != nil {
		st.memoryCancel(errMessageUpdated)
		st.memoryCancel = nil
	}
	if st.replyCancel != nil {
		st.replyCancel(errMessageUpdated)
		st.replyCancel = nil
	}
}

func (p *messageProcessor) onDeleted(chatID, messageID string) {
	key := messageKey(chatID, messageID)
	raw, ok := p.states.Load(key)
	if !ok {
		return
	}
	st := raw.(*messageState)
	st.mu.Lock()
	defer st.mu.Unlock()
	st.deleted = true
	if st.memoryCancel != nil {
		st.memoryCancel(errMessageDeleted)
		st.memoryCancel = nil
	}
	if st.replyCancel != nil {
		st.replyCancel(errMessageDeleted)
		st.replyCancel = nil
	}
	if st.runCancel != nil {
		st.runCancel(errMessageDeleted)
	}
}

func (p *messageProcessor) snapshot(st *messageState) (transport.Message, uint64, bool) {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.message, st.revision, st.deleted
}

// memoryContext returns a context for one memory attempt. A message update cancels the
// previous memory context without stopping the overall processing context.
func (p *messageProcessor) memoryContext(parent context.Context, st *messageState) context.Context {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.deleted {
		ctx, cancel := context.WithCancelCause(parent)
		cancel(errMessageDeleted)
		return ctx
	}
	if st.memoryCancel != nil {
		st.memoryCancel(errMessageUpdated)
	}
	ctx, cancel := context.WithCancelCause(parent)
	st.memoryCancel = cancel
	return ctx
}

// replyContext returns a context for one reply/LLM attempt. A message update cancels the
// previous reply context without stopping the overall processing context.
func (p *messageProcessor) replyContext(parent context.Context, st *messageState) context.Context {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.deleted {
		ctx, cancel := context.WithCancelCause(parent)
		cancel(errMessageDeleted)
		return ctx
	}
	if st.replyCancel != nil {
		st.replyCancel(errMessageUpdated)
	}
	ctx, cancel := context.WithCancelCause(parent)
	st.replyCancel = cancel
	return ctx
}

func isProcessingCancelled(err error) bool {
	return errors.Is(err, errMessageDeleted) || errors.Is(err, errMessageUpdated) || errors.Is(err, context.Canceled)
}

func isContextCancelled(ctx context.Context) bool {
	cause := context.Cause(ctx)
	return errors.Is(cause, errMessageDeleted) || errors.Is(cause, errMessageUpdated) || errors.Is(ctx.Err(), context.Canceled)
}
