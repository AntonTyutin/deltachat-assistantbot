package storage

import (
	"time"

	"github.com/AntonTyutin/assistantbot-core/transport"
)

type ParticipantProfile struct {
	ID        string            `json:"id"`
	Names     map[string]string `json:"names,omitempty"`
	City      string            `json:"city,omitempty"`
	Address   string            `json:"address,omitempty"`
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

type Message = transport.Message

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

type DailySummary struct {
	ChatID    string    `json:"chat_id"`
	Date      string    `json:"date"`
	Summary   string    `json:"summary"`
	CreatedAt time.Time `json:"created_at"`
}
