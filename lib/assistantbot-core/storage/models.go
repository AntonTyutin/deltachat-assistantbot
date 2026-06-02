package storage

import (
	"time"

	"github.com/AntonTyutin/assistantbot-core/reminders"
	"github.com/AntonTyutin/assistantbot-core/transport"
)

type ParticipantProfile struct {
	ID        string            `json:"id"`
	Names     map[string]string `json:"names,omitempty"`
	City      string            `json:"city,omitempty"`
	Address   string            `json:"address,omitempty"`
	Timezone  string            `json:"timezone,omitempty"`
	Style     string            `json:"style,omitempty"`
	Verbosity string            `json:"verbosity,omitempty"`
	Expertise map[string]string `json:"expertise,omitempty"`
	Interests []string          `json:"interests,omitempty"`
	UpdatedAt time.Time         `json:"updated_at"`
}

type Chat struct {
	ID        string    `json:"id"`
	IsGroup   bool      `json:"is_group"`
	Title     string    `json:"title,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
}

type Message struct {
	ID         string    `json:"id"`
	ChatID     string    `json:"chat_id"`
	SenderID   string    `json:"sender_id"`
	Sender     string    `json:"sender"`
	Text       string    `json:"text"`
	IsGroup    bool      `json:"is_group"`
	IsFromSelf bool      `json:"is_from_self"`
	ReplyToID  string    `json:"reply_to_id,omitempty"`
	TopicID    string    `json:"topic_id,omitempty"`
	SentAt     time.Time `json:"sent_at"`
}

func MessageFromTransport(m transport.Message) Message {
	return Message{
		ID:         m.ID,
		ChatID:     m.ChatID,
		SenderID:   m.SenderID,
		Sender:     m.Sender,
		Text:       m.Text,
		IsGroup:    m.IsGroup,
		IsFromSelf: m.IsFromSelf,
		ReplyToID:  m.ReplyToID,
		SentAt:     m.SentAt,
	}
}

type Topic struct {
	ID                 string    `json:"id"`
	ChatID             string    `json:"chat_id"`
	Title              string    `json:"title"`
	Summary            string    `json:"summary"`
	Decisions          []string  `json:"decisions,omitempty"`
	OpenQuestions      []string  `json:"open_questions,omitempty"`
	ActiveParticipants []string  `json:"active_participants,omitempty"`
	UpdatedAt          time.Time `json:"updated_at"`
}

type ListKind string

const (
	ListKindList ListKind = "list"
	ListKindNote ListKind = "note"
)

type List struct {
	ID        string    `json:"id"`
	ChatID    string    `json:"chat_id"`
	Kind      ListKind  `json:"kind"`
	Title     string    `json:"title"`
	UpdatedAt time.Time `json:"updated_at"`
}

type ListItem struct {
	ID        string    `json:"id"`
	ListID    string    `json:"list_id"`
	Text      string    `json:"text"`
	CreatedBy string    `json:"created_by"`
	Done      bool      `json:"done"`
	CreatedAt time.Time `json:"created_at"`
}

type ReminderStatus string

const (
	ReminderPending   ReminderStatus = "pending"
	ReminderDelivered ReminderStatus = "delivered"
	ReminderCancelled ReminderStatus = "cancelled"
)

type ReminderMode string

const (
	ReminderModeText   ReminderMode = "text"
	ReminderModeAction ReminderMode = "action"
)

type Reminder struct {
	ID              string                    `json:"id"`
	ChatID          string                    `json:"chat_id"`
	RequesterID     string                    `json:"requester_id"`
	DueAt           time.Time                 `json:"due_at"`
	Text            string                    `json:"text"`
	Status          ReminderStatus            `json:"status"`
	CreatedAt       time.Time                 `json:"created_at"`
	AnchorAt        time.Time                 `json:"anchor_at,omitempty"`
	Recurrence      *reminders.RecurrenceRule `json:"recurrence,omitempty"`
	OccurrenceCount int                       `json:"occurrence_count,omitempty"`
	Mode            ReminderMode              `json:"mode,omitempty"`
	ActionPrompt    string                    `json:"action_prompt,omitempty"`
}

type NearestMessage struct {
	Message  Message
	Distance float64
}

type NearestTopic struct {
	Topic    Topic
	Distance float64
}

type NearestList struct {
	List     List
	Distance float64
}
