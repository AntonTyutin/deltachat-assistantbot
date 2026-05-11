package app

import (
	"context"
	"log/slog"
	"time"

	"assistantbot/internal/deltachat"
	"assistantbot/internal/memory"
	"assistantbot/internal/reply"
	"assistantbot/internal/storage"
)

type App struct {
	delta   deltachat.Client
	store   *storage.Store
	memory  *memory.Pipeline
	replies *reply.Service
	logger  *slog.Logger
}

func New(delta deltachat.Client, store *storage.Store, memoryPipeline *memory.Pipeline, replyService *reply.Service, logger *slog.Logger) *App {
	return &App{
		delta:   delta,
		store:   store,
		memory:  memoryPipeline,
		replies: replyService,
		logger:  logger,
	}
}

func (a *App) Run(ctx context.Context) error {
	return a.delta.Run(ctx, a.HandleEvent)
}

func (a *App) HandleEvent(ctx context.Context, event deltachat.MessageEvent) error {
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

func (a *App) HandleMessage(ctx context.Context, message deltachat.Message) error {
	if message.SentAt.IsZero() {
		message.SentAt = time.Now()
	}
	if err := a.store.UpsertChat(ctx, storage.Chat{
		ID:        message.ChatID,
		IsGroup:   message.IsGroup,
		UpdatedAt: time.Now(),
	}); err != nil {
		return err
	}
	topic, err := a.memory.ProcessMessage(ctx, message)
	if err != nil {
		return err
	}
	outbound, classification, err := a.replies.Decide(ctx, message, topic)
	if err != nil {
		return err
	}
	a.logger.Info("message classified", "chat_id", message.ChatID, "message_id", message.ID, "intent", classification.Intent, "reason", classification.Reason)
	if outbound == nil {
		return nil
	}
	replyID, err := a.delta.SendText(ctx, *outbound)
	if err != nil {
		return err
	}
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
