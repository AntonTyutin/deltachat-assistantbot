package transport

import (
	"context"
	"time"
)

type Message struct {
	ID         string    `json:"id"`
	ChatID     string    `json:"chat_id"`
	SenderID   string    `json:"sender_id"`
	Sender     string    `json:"sender"`
	Text       string    `json:"text"`
	IsGroup    bool      `json:"is_group"`
	IsFromSelf bool      `json:"is_from_self"`
	ReplyToID  string    `json:"reply_to_id,omitempty"`
	SentAt     time.Time `json:"sent_at"`
}

type OutboundMessage struct {
	ChatID    string `json:"chat_id"`
	Text      string `json:"text"`
	ReplyToID string `json:"reply_to_id,omitempty"`
}

type MessageEdit struct {
	ChatID    string `json:"chat_id"`
	MessageID string `json:"message_id"`
	Text      string `json:"text"`
}

type MessageReaction struct {
	ChatID    string `json:"chat_id"`
	MessageID string `json:"message_id"`
	Emoji     string `json:"emoji"`
}

type TypingState struct {
	ChatID    string `json:"chat_id"`
	IsTyping  bool   `json:"is_typing"`
	ExpiresAt *time.Time
}

type MediaMessage struct {
	ChatID     string `json:"chat_id"`
	Caption    string `json:"caption,omitempty"`
	MediaPath  string `json:"media_path,omitempty"`
	MediaURL   string `json:"media_url,omitempty"`
	ReplyToID  string `json:"reply_to_id,omitempty"`
	MediaType  string `json:"media_type,omitempty"`
	MimeType   string `json:"mime_type,omitempty"`
	FileName   string `json:"file_name,omitempty"`
	ExternalID string `json:"external_id,omitempty"`
}

type NewMessageHandler func(ctx context.Context, message Message) error
type MessageUpdatedHandler func(ctx context.Context, message Message) error
type MessageDeletedHandler func(ctx context.Context, chatID, messageID string) error
type LocationUpdatedHandler func(ctx context.Context, participantID string, latitude, longitude float64) error

type EventHandlers struct {
	OnNewMessage      NewMessageHandler
	OnMessageUpdated  MessageUpdatedHandler
	OnMessageDeleted  MessageDeletedHandler
	OnLocationUpdated LocationUpdatedHandler
}

type Messenger interface {
	Run(ctx context.Context, handlers EventHandlers) error

	SendText(ctx context.Context, message OutboundMessage) (string, error)
	EditMessage(ctx context.Context, edit MessageEdit) error
	DeleteMessage(ctx context.Context, chatID, messageID string) error
	React(ctx context.Context, reaction MessageReaction) error
	SetTyping(ctx context.Context, state TypingState) error
	SendMedia(ctx context.Context, media MediaMessage) (string, error)
}
