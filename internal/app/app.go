package app

import (
	"context"
	"log/slog"
	"time"

	"assistantbot/internal/deltachat"
	"assistantbot/internal/memory"
	"assistantbot/internal/metrics"
	"assistantbot/internal/reply"
	"assistantbot/internal/storage"
)

type App struct {
	delta    deltachat.Client
	store    *storage.Store
	memory   *memory.Pipeline
	replies  *reply.Service
	logger   *slog.Logger
	recorder metrics.Recorder
}

func New(delta deltachat.Client, store *storage.Store, memoryPipeline *memory.Pipeline, replyService *reply.Service, logger *slog.Logger, recorder metrics.Recorder) *App {
	if recorder == nil {
		recorder = metrics.Noop
	}
	return &App{
		delta:    delta,
		store:    store,
		memory:   memoryPipeline,
		replies:  replyService,
		logger:   logger,
		recorder: recorder,
	}
}

func (a *App) Run(ctx context.Context) error {
	return a.delta.Run(ctx, a.HandleEvent)
}

func (a *App) HandleEvent(ctx context.Context, event deltachat.MessageEvent) (err error) {
	defer func() {
		if err == nil {
			return
		}
		a.logEventHandlerError(event, err)
		err = nil
	}()

	switch event.Kind {
	case deltachat.MessageEventNew:
		return a.HandleMessage(ctx, event.Message)
	case deltachat.MessageEventUpdated:
		if err := a.memory.ProcessMessageUpdate(ctx, event.Message); err != nil {
			return err
		}
		a.logger.Info("message memory refreshed", "chat_id", event.ChatID, "message_id", event.MessageID)
		return nil
	case deltachat.MessageEventDeleted:
		if err := a.memory.ProcessMessageDelete(ctx, event.ChatID, event.MessageID); err != nil {
			return err
		}
		a.logger.Info("message memory deleted", "chat_id", event.ChatID, "message_id", event.MessageID)
		return nil
	case deltachat.MessageEventLocationUpdate:
		if event.ParticipantID == "" || event.Latitude == nil || event.Longitude == nil {
			return nil
		}
		if err := a.memory.UpdateParticipantLocationFromCoordinates(ctx, event.ParticipantID, *event.Latitude, *event.Longitude); err != nil {
			return err
		}
		a.logger.Info("participant location updated from coordinates", "participant_id", event.ParticipantID)
		return nil
	default:
		a.logger.Warn("unknown message event kind", "kind", event.Kind, "chat_id", event.ChatID, "message_id", event.MessageID)
		return nil
	}
}

func (a *App) logEventHandlerError(event deltachat.MessageEvent, err error) {
	args := []any{
		"event_kind", event.Kind,
		"error", err,
	}
	if event.ChatID != "" {
		args = append(args, "chat_id", event.ChatID)
	}
	if event.MessageID != "" {
		args = append(args, "message_id", event.MessageID)
	}
	if event.ParticipantID != "" {
		args = append(args, "participant_id", event.ParticipantID)
	}
	a.logger.Error("message event handler failed", args...)
}

func (a *App) HandleMessage(ctx context.Context, message deltachat.Message) (err error) {
	if message.SentAt.IsZero() {
		message.SentAt = time.Now()
	}
	handleStart := time.Now()
	var sentReply bool
	defer func() {
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

	if err := a.store.UpsertChat(ctx, storage.Chat{
		ID:        message.ChatID,
		IsGroup:   message.IsGroup,
		UpdatedAt: time.Now(),
	}); err != nil {
		return err
	}
	t0 := time.Now()
	topic, err := a.memory.ProcessMessage(ctx, message)
	if err != nil {
		return err
	}
	a.recorder.RecordMessagePhase(metrics.PhaseMemory, time.Since(t0))

	t1 := time.Now()
	outbound, classification, err := a.replies.Decide(ctx, message, topic)
	if err != nil {
		return err
	}
	a.recorder.RecordMessagePhase(metrics.PhaseReply, time.Since(t1))

	a.logger.Info("message classified", "chat_id", message.ChatID, "message_id", message.ID, "intent", classification.Intent, "reason", classification.Reason)
	if outbound == nil {
		return nil
	}
	t2 := time.Now()
	replyID, err := a.delta.SendText(ctx, *outbound)
	if err != nil {
		return err
	}
	sentReply = true
	a.recorder.RecordMessagePhase(metrics.PhaseSend, time.Since(t2))
	if replyID != "" {
		if _, err := a.memory.ProcessMessage(ctx, deltachat.Message{
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
			return err
		}
	}
	a.logger.Info("reply sent", "chat_id", outbound.ChatID, "message_id", replyID, "reply_to_id", outbound.ReplyToID)
	return nil
}
