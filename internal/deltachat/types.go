package deltachat

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

type MessageEventKind string

const (
	MessageEventNew            MessageEventKind = "new"
	MessageEventUpdated        MessageEventKind = "updated"
	MessageEventDeleted        MessageEventKind = "deleted"
	MessageEventLocationUpdate MessageEventKind = "location_update"
)

type MessageEvent struct {
	Kind          MessageEventKind
	Message       Message
	ChatID        string
	MessageID     string
	ParticipantID string
	Latitude      *float64
	Longitude     *float64
}

type EventHandler func(ctx context.Context, event MessageEvent) error

type Client interface {
	Run(ctx context.Context, handler EventHandler) error
	SendText(ctx context.Context, message OutboundMessage) (string, error)
}
