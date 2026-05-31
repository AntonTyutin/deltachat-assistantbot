package app

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/AntonTyutin/assistantbot-core/llm"
	"github.com/AntonTyutin/assistantbot-core/storage"
	"github.com/AntonTyutin/assistantbot-core/transport"
)

func TestPrepareForReplyWaitsForChatMemoryLock(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	defer store.Close()

	client := llm.StaticClient{Responses: map[string]json.RawMessage{
		llm.TaskClassifyMessageTopic: json.RawMessage(`{"is_new_topic":true,"title":"hello","summary":"hello"}`),
	}}
	messenger := &someMessengerClient{}
	app := buildTestApp(messenger, store, client, nil)

	gate := make(chan struct{})
	holdDone := make(chan struct{})
	go func() {
		_ = app.chatMemory.runPrepare(ctx, "chat-1", func(context.Context) error {
			close(holdDone)
			<-gate
			return nil
		})
	}()
	<-holdDone

	msg := transport.Message{
		ID:       "1",
		ChatID:   "chat-1",
		SenderID: "user-1",
		Text:     "hello",
		IsGroup:  true,
		SentAt:   time.Now(),
	}
	prepDone := make(chan error, 1)
	go func() {
		err := app.RunChatMemory(ctx, "chat-1", func(ctx context.Context) error {
			_, err := app.memory.PrepareForReply(ctx, msg)
			return err
		})
		prepDone <- err
	}()
	select {
	case err := <-prepDone:
		t.Fatalf("prepare should block on chat lock, got err=%v", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(gate)
	select {
	case err := <-prepDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("prepare did not finish after lock release")
	}
}

func TestDeliverDueRemindersDoesNotBlockOnPrepareLock(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	defer store.Close()

	messenger := &someMessengerClient{}
	app := buildTestApp(messenger, store, llm.StaticClient{}, nil)

	gate := make(chan struct{})
	holdDone := make(chan struct{})
	go func() {
		_ = app.chatMemory.runBackground(ctx, "chat-1", func(context.Context) error {
			close(holdDone)
			<-gate
			return nil
		})
	}()
	<-holdDone

	reminderDone := make(chan error, 1)
	go func() {
		reminderDone <- app.DeliverDueReminders(ctx, time.Now())
	}()
	select {
	case err := <-reminderDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("reminder delivery blocked on prepare lock")
	}
	close(gate)
}

func TestDecideDoesNotWaitOnChatMemoryLock(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	defer store.Close()

	llmClient := llm.StaticClient{Responses: map[string]json.RawMessage{
		llm.TaskGenerateChatReply: json.RawMessage(`{"reply":"ok"}`),
	}}
	messenger := &someMessengerClient{}
	app := buildTestApp(messenger, store, llmClient, []string{"чатик"})

	gate := make(chan struct{})
	holdDone := make(chan struct{})
	go func() {
		_ = app.chatMemory.runPrepare(ctx, "chat-1", func(context.Context) error {
			close(holdDone)
			<-gate
			return nil
		})
	}()
	<-holdDone

	decideDone := make(chan error, 1)
	go func() {
		_, _, err := app.replies.Decide(ctx, botAddressedMessage(), storage.Topic{ID: "topic-1", ChatID: "10", Summary: "test"})
		decideDone <- err
	}()
	select {
	case err := <-decideDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("decide blocked on chat memory lock")
	}
	close(gate)
}
