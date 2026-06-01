package reply

import (
	"context"
	"errors"
	"testing"

	"github.com/AntonTyutin/assistantbot-core/storage"
	"github.com/AntonTyutin/assistantbot-core/transport"
)

func TestParseModelReplyValidJSON(t *testing.T) {
	got, err := parseModelReply(`{"reply":"Да, список идей"}`)
	if err != nil {
		t.Fatal(err)
	}
	if got != "Да, список идей" {
		t.Fatalf("got %q", got)
	}
}

func TestParseModelReplyTruncatedJSON(t *testing.T) {
	got, err := parseModelReply(`{"reply":"Да, список идей для чат`)
	if err != nil {
		t.Fatal(err)
	}
	if got != "Да, список идей для чат" {
		t.Fatalf("got %q", got)
	}
}

func TestParseModelReplyPlainText(t *testing.T) {
	got, err := parseModelReply("Просто текст без JSON")
	if err != nil {
		t.Fatal(err)
	}
	if got != "Просто текст без JSON" {
		t.Fatalf("got %q", got)
	}
}

func TestParseModelReplyJSONFence(t *testing.T) {
	got, err := parseModelReply("```json\n{\"reply\":\"ok\"}\n```")
	if err != nil {
		t.Fatal(err)
	}
	if got != "ok" {
		t.Fatalf("got %q", got)
	}
}

func TestParseModelReplyEmptyField(t *testing.T) {
	_, err := parseModelReply(`{"reply":""}`)
	if !errors.Is(err, ErrEmptyModelReply) {
		t.Fatalf("expected ErrEmptyModelReply, got %v", err)
	}
}

func TestParseModelReplyUnparseableJSON(t *testing.T) {
	_, err := parseModelReply(`{"reply":`)
	if !errors.Is(err, ErrUnparseableModelReply) {
		t.Fatalf("expected ErrUnparseableModelReply, got %v", err)
	}
}

func TestDecideReturnsFailureReplyOnUnparseableModelJSON(t *testing.T) {
	service, _, cleanup := testReplyService(t, `{"reply":`)
	defer cleanup()

	outbound, _, err := service.Decide(context.Background(), transport.Message{
		ID:       "m1",
		ChatID:   "chat-1",
		SenderID: "user-1",
		Text:     "hello",
		IsGroup:  false,
	}, storage.Topic{})
	if err != nil {
		t.Fatal(err)
	}
	if outbound == nil {
		t.Fatal("expected outbound reply")
	}
	if outbound.Text != failureReply {
		t.Fatalf("expected %q, got %q", failureReply, outbound.Text)
	}
}
