package deltachat

import (
	"encoding/json"
	"testing"

	chatmail "github.com/chatmail/rpc-client-go/v2/deltachat"
)

// Documents rpc-client-go v2.49.0: Message.UnmarshalJSON calls
// unmarshalMessageQuote(raw.Quote, s.Quote) with nil s.Quote and panics on replies.
func TestChatmailMessageUnmarshalWithQuotePanics(t *testing.T) {
	t.Parallel()

	const payload = `{
		"chatId": 42,
		"fromId": 10,
		"id": 189,
		"parentId": 100,
		"quote": {
			"kind": "WithMessage",
			"authorDisplayColor": "#6d4aff",
			"authorDisplayName": "Alice",
			"chatId": 42,
			"isForwarded": false,
			"messageId": 100,
			"text": "hello bot",
			"viewType": "Text"
		},
		"sender": {"id": 10, "name": "Alice"},
		"text": "thanks",
		"timestamp": 1710000000,
		"receivedTimestamp": 1710000001,
		"state": 0,
		"viewType": "Text"
	}`

	var msg chatmail.Message
	var panicked any
	func() {
		defer func() {
			panicked = recover()
		}()
		if err := json.Unmarshal([]byte(payload), &msg); err != nil {
			t.Fatalf("unmarshal failed: %v", err)
		}
	}()
	if panicked == nil {
		t.Fatal("expected chatmail.Message unmarshal to panic on quoted replies")
	}
}

func TestRPCMessagePayloadUnmarshalWithQuote(t *testing.T) {
	t.Parallel()

	const payload = `{
		"chatId": 42,
		"fromId": 10,
		"id": 189,
		"parentId": 100,
		"quote": {
			"kind": "WithMessage",
			"authorDisplayName": "Alice",
			"chatId": 42,
			"messageId": 100,
			"text": "hello bot",
			"viewType": "Text"
		},
		"sender": {"id": 10, "name": "Alice"},
		"text": "thanks",
		"timestamp": 1710000000,
		"receivedTimestamp": 1710000001,
		"state": 0
	}`

	var msg rpcMessagePayload
	if err := json.Unmarshal([]byte(payload), &msg); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if msg.ParentId == nil || *msg.ParentId != 100 {
		t.Fatalf("expected parentId 100, got %+v", msg.ParentId)
	}
	if msg.Text != "thanks" || msg.FromId != 10 {
		t.Fatalf("unexpected message: %+v", msg)
	}
}
