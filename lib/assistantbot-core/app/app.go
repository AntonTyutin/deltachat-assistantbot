package app

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/AntonTyutin/assistantbot-core/memory"
	"github.com/AntonTyutin/assistantbot-core/metrics"
	"github.com/AntonTyutin/assistantbot-core/reply"
	"github.com/AntonTyutin/assistantbot-core/storage"
	"github.com/AntonTyutin/assistantbot-core/tracing"
	"github.com/AntonTyutin/assistantbot-core/transport"
)

type App struct {
	messenger  transport.Messenger
	store      *storage.Store
	memory     *memory.Pipeline
	replies    *reply.Service
	logger     *slog.Logger
	recorder   metrics.Recorder
	processor  *messageProcessor
	chatMemory *chatMemoryLocker
}

func New(messenger transport.Messenger, store *storage.Store, memoryPipeline *memory.Pipeline, replyService *reply.Service, logger *slog.Logger, recorder metrics.Recorder) *App {
	if recorder == nil {
		recorder = metrics.Noop
	}
	return &App{
		messenger:  messenger,
		store:      store,
		memory:     memoryPipeline,
		replies:    replyService,
		logger:     logger,
		recorder:   recorder,
		processor:  newMessageProcessor(),
		chatMemory: newChatMemoryLocker(recorder),
	}
}

// RunChatMemory enqueues fn on the per-chat prepare FIFO queue (fast path before reply).
func (a *App) RunChatMemory(ctx context.Context, chatID string, fn func(context.Context) error) error {
	return a.chatMemory.runPrepare(ctx, chatID, fn)
}

// UpdateDailySummary runs on the per-chat background FIFO queue.
func (a *App) UpdateDailySummary(ctx context.Context, chatID string, date time.Time) error {
	ctx = tracing.WithTraceID(ctx, tracing.NewID())
	return a.chatMemory.runBackground(ctx, chatID, func(ctx context.Context) error {
		return a.memory.UpdateDailySummary(ctx, chatID, date)
	})
}

func (a *App) Run(ctx context.Context) error {
	return a.messenger.Run(ctx, transport.EventHandlers{
		OnNewMessage: func(ctx context.Context, message transport.Message) error {
			a.startMessageProcessing(ctx, message)
			return nil
		},
		OnMessageUpdated: func(ctx context.Context, message transport.Message) error {
			if err := a.handleMessageUpdated(ctx, message); err != nil {
				a.logger.Error("message update handler failed", "chat_id", message.ChatID, "message_id", message.ID, "error", err)
			}
			return nil
		},
		OnMessageDeleted: func(ctx context.Context, chatID, messageID string) error {
			if err := a.handleMessageDeleted(ctx, chatID, messageID); err != nil {
				a.logger.Error("message delete handler failed", "chat_id", chatID, "message_id", messageID, "error", err)
			}
			return nil
		},
		OnLocationUpdated: func(ctx context.Context, participantID string, latitude, longitude float64) error {
			if err := a.handleLocationUpdated(ctx, participantID, latitude, longitude); err != nil {
				a.logger.Error("location update handler failed", "participant_id", participantID, "error", err)
			}
			return nil
		},
	})
}

func (a *App) startMessageProcessing(parent context.Context, message transport.Message) {
	go func() {
		procCtx, release, state := a.processor.begin(parent, message)
		defer release()
		if err := a.handleMessageProcessing(procCtx, state, message, false); err != nil && !isProcessingCancelled(err) {
			a.logger.ErrorContext(procCtx, "message processing failed", "chat_id", message.ChatID, "message_id", message.ID, "error", err)
		}
	}()
}

func (a *App) handleMessageUpdated(_ context.Context, message transport.Message) error {
	a.processor.onUpdated(message)
	a.logger.Info("message update scheduled", "chat_id", message.ChatID, "message_id", message.ID)
	return nil
}

func (a *App) handleMessageDeleted(_ context.Context, chatID, messageID string) error {
	a.processor.onDeleted(chatID, messageID)
	a.logger.Info("message delete scheduled", "chat_id", chatID, "message_id", messageID)
	return nil
}

func (a *App) handleLocationUpdated(ctx context.Context, participantID string, latitude, longitude float64) error {
	if participantID == "" {
		return nil
	}
	if err := a.memory.UpdateParticipantLocationFromCoordinates(ctx, participantID, latitude, longitude); err != nil {
		return err
	}
	a.logger.Info("participant location updated from coordinates", "participant_id", participantID)
	return nil
}

// HandleMessage runs the full inbound pipeline synchronously. Tests and direct callers use it;
// the transport path uses startMessageProcessing instead.
func (a *App) HandleMessage(ctx context.Context, message transport.Message) error {
	procCtx, release, state := a.processor.begin(ctx, message)
	defer release()
	return a.handleMessageProcessing(procCtx, state, message, true)
}

func (a *App) handleMessageProcessing(procCtx context.Context, state *messageState, message transport.Message, waitBackground bool) (err error) {
	if message.SentAt.IsZero() {
		message.SentAt = time.Now()
	}
	handleStart := time.Now()
	var sentReply bool
	var background sync.WaitGroup
	defer func() {
		if waitBackground {
			background.Wait()
		}
		if isProcessingCancelled(err) || isContextCancelled(procCtx) {
			err = nil
		}
		result := metrics.ResultError
		if err == nil {
			if sentReply {
				result = metrics.ResultReplied
			} else {
				result = metrics.ResultNoReply
			}
		}
		a.recorder.RecordInboundMessageHandle(result, time.Since(handleStart))
	}()

	if _, _, deleted := a.processor.snapshot(state); deleted {
		return errMessageDeleted
	}

	if err := a.store.UpsertChat(procCtx, storage.Chat{
		ID:        message.ChatID,
		IsGroup:   message.IsGroup,
		UpdatedAt: time.Now(),
	}); err != nil {
		return err
	}

	var outbound *transport.OutboundMessage
	var classification reply.Classification
	var topic storage.Topic
	var revision uint64
	var deleted bool
	for {
		message, revision, deleted = a.processor.snapshot(state)
		if deleted {
			return errMessageDeleted
		}

		revBefore := revision
		err = a.RunChatMemory(procCtx, message.ChatID, func(ctx context.Context) error {
			var prepErr error
			topic, prepErr = a.memory.PrepareForReply(ctx, message)
			return prepErr
		})
		if err != nil {
			return err
		}

		replyCtx := a.processor.replyContext(procCtx, state)
		t1 := time.Now()
		outbound, classification, err = a.replies.Decide(replyCtx, message, topic)
		a.recorder.RecordMessagePhase(metrics.PhaseReply, time.Since(t1))
		if err != nil {
			if isContextCancelled(replyCtx) {
				message, revision, deleted = a.processor.snapshot(state)
				if deleted {
					return errMessageDeleted
				}
				if revision != revBefore {
					continue
				}
			}
			return err
		}

		message, revision, deleted = a.processor.snapshot(state)
		if deleted {
			return errMessageDeleted
		}
		if revision == revBefore {
			break
		}
	}

	a.logger.InfoContext(procCtx, "message classified", "chat_id", message.ChatID, "message_id", message.ID, "intent", classification.Intent, "reason", classification.Reason)

	var replyID string
	if outbound != nil {
		if _, _, deleted = a.processor.snapshot(state); deleted {
			return errMessageDeleted
		}
		if isContextCancelled(procCtx) {
			return context.Cause(procCtx)
		}

		t2 := time.Now()
		replyID, err = a.messenger.SendText(procCtx, *outbound)
		if err != nil {
			return err
		}
		sentReply = true
		a.recorder.RecordMessagePhase(metrics.PhaseSend, time.Since(t2))

		message, _, deleted = a.processor.snapshot(state)
		if deleted {
			return errMessageDeleted
		}
		a.logger.InfoContext(procCtx, "reply sent", "chat_id", outbound.ChatID, "message_id", replyID, "reply_to_id", outbound.ReplyToID)
	}

	background.Add(1)
	go func() {
		defer background.Done()
		a.finishMessageMemory(procCtx, state, outbound, replyID, !waitBackground)
	}()

	return nil
}

func (a *App) finishMessageMemory(procCtx context.Context, state *messageState, outbound *transport.OutboundMessage, replyID string, async bool) {
	message, _, deleted := a.processor.snapshot(state)
	if deleted {
		return
	}
	chatID := message.ChatID
	if chatID == "" && outbound != nil {
		chatID = outbound.ChatID
	}
	bgCtx := tracing.WithParentTraceID(tracing.WithTraceID(procCtx, tracing.NewID()), tracing.TraceID(procCtx))
	run := func() {
		_ = a.chatMemory.runBackground(bgCtx, chatID, func(ctx context.Context) error {
			a.finishMessageMemoryLocked(ctx, state, outbound, replyID)
			return nil
		})
	}
	if async {
		go run()
		return
	}
	run()
}

func (a *App) finishMessageMemoryLocked(procCtx context.Context, state *messageState, outbound *transport.OutboundMessage, replyID string) {
	for {
		message, revision, deleted := a.processor.snapshot(state)
		if deleted {
			return
		}

		revBefore := revision
		memoryCtx := a.processor.memoryContext(procCtx, state)
		t0 := time.Now()
		err := a.memory.ProcessMessageUpdate(memoryCtx, message)
		a.recorder.RecordMessagePhase(metrics.PhaseMemory, time.Since(t0))
		if err != nil {
			if isContextCancelled(memoryCtx) {
				message, revision, deleted = a.processor.snapshot(state)
				if deleted {
					return
				}
				if revision != revBefore {
					continue
				}
			}
			a.logger.ErrorContext(procCtx, "background memory update failed",
				"chat_id", message.ChatID,
				"message_id", message.ID,
				"error", err,
			)
			return
		}

		message, revision, deleted = a.processor.snapshot(state)
		if deleted {
			return
		}
		if revision != revBefore {
			continue
		}
		break
	}

	if replyID == "" || outbound == nil {
		return
	}

	message, _, deleted := a.processor.snapshot(state)
	if deleted {
		return
	}
	if _, err := a.memory.ProcessMessage(procCtx, transport.Message{
		ID:         replyID,
		ChatID:     outbound.ChatID,
		SenderID:   "self",
		Sender:     "Me",
		Text:       outbound.Text,
		IsGroup:    message.IsGroup,
		IsFromSelf: true,
		ReplyToID:  outbound.ReplyToID,
		SentAt:     time.Now(),
	}); err != nil {
		a.logger.ErrorContext(procCtx, "background outbound message storage failed",
			"chat_id", outbound.ChatID,
			"message_id", replyID,
			"error", err,
		)
	}
}
