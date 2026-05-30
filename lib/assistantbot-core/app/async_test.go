package app

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AntonTyutin/assistantbot-core/llm"
	"github.com/AntonTyutin/assistantbot-core/llm/prompts"
	"github.com/AntonTyutin/assistantbot-core/memory"
	"github.com/AntonTyutin/assistantbot-core/reply"
	"github.com/AntonTyutin/assistantbot-core/storage"
)

func TestStartMessageProcessingIsAsync(t *testing.T) {
	ctx := context.Background()
	store, err := storage.Open(ctx, filepath.Join(t.TempDir(), "test.db"), "test-secret")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	gate := make(chan struct{})
	replyStarted := make(chan struct{}, 1)
	messenger := &someMessengerClient{sentID: "25", sentDone: make(chan struct{}, 1)}
	llmClient := &gatedLLM{
		StaticClient: llm.StaticClient{Responses: map[string]json.RawMessage{
			"update_participant_profile": json.RawMessage(`{"names":{"chat":"Anton"}}`),
			"update_chat_topic":          json.RawMessage(`{"title":"date","summary":"date question","active_participants":["user-1"]}`),
			"generate_chat_reply":        json.RawMessage(`{"reply":"ok"}`),
		}},
		gate:         gate,
		replyStarted: replyStarted,
	}
	app := New(
		messenger,
		store,
		memory.NewPipeline(store, llmClient, nil, prompts.FixedTestRegistry()),
		reply.NewService(store, llmClient, prompts.FixedTestRegistry(), []string{"чатик"}, nil, nil, nil),
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		nil,
	)

	start := time.Now()
	app.startMessageProcessing(ctx, botAddressedMessage())
	if elapsed := time.Since(start); elapsed > 50*time.Millisecond {
		t.Fatalf("expected async start, took %v", elapsed)
	}

	select {
	case <-replyStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("reply generation did not start")
	}

	close(gate)

	select {
	case <-messenger.sentDone:
	case <-time.After(2 * time.Second):
		t.Fatal("processing did not finish")
	}
}

func TestDeleteDuringReplySkipsSend(t *testing.T) {
	ctx := context.Background()
	store, err := storage.Open(ctx, filepath.Join(t.TempDir(), "test.db"), "test-secret")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	gate := make(chan struct{})
	replyStarted := make(chan struct{}, 1)
	llmClient := &gatedLLM{
		StaticClient: llm.StaticClient{Responses: map[string]json.RawMessage{
			"update_participant_profile": json.RawMessage(`{"names":{"chat":"Anton"}}`),
			"update_chat_topic":          json.RawMessage(`{"title":"date","summary":"date question","active_participants":["user-1"]}`),
			"generate_chat_reply":        json.RawMessage(`{"reply":"should not send"}`),
		}},
		gate:         gate,
		replyStarted: replyStarted,
	}
	messenger := &someMessengerClient{sentID: "99"}
	app := New(
		messenger,
		store,
		memory.NewPipeline(store, llmClient, nil, prompts.FixedTestRegistry()),
		reply.NewService(store, llmClient, prompts.FixedTestRegistry(), []string{"чатик"}, nil, nil, nil),
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		nil,
	)

	msg := botAddressedMessage()
	procCtx, release, state := app.processor.begin(ctx, msg)
	defer release()
	done := make(chan error, 1)
	go func() {
		done <- app.handleMessageProcessing(procCtx, state, msg, true)
	}()

	select {
	case <-replyStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("reply generation did not start")
	}

	if err := app.handleMessageDeleted(ctx, msg.ChatID, msg.ID); err != nil {
		t.Fatal(err)
	}
	close(gate)

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("processing: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("processing did not finish")
	}
	if len(messenger.sent) != 0 {
		t.Fatalf("expected no reply after delete, got %d sends", len(messenger.sent))
	}
}

func TestUpdateDuringReplyRetriesWithEditedText(t *testing.T) {
	ctx := context.Background()
	store, err := storage.Open(ctx, filepath.Join(t.TempDir(), "test.db"), "test-secret")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	var generateCalls atomic.Int32
	gate := make(chan struct{})
	replyStarted := make(chan struct{}, 2)
	gated := &gatedLLM{
		StaticClient: llm.StaticClient{Responses: map[string]json.RawMessage{
			"update_participant_profile": json.RawMessage(`{"names":{"chat":"Anton"}}`),
			"update_chat_topic":          json.RawMessage(`{"title":"date","summary":"date question","active_participants":["user-1"]}`),
			"generate_chat_reply":        json.RawMessage(`{"reply":"first"}`),
		}},
		gate:         gate,
		replyStarted: replyStarted,
	}
	gated.onGenerate = func() {
		if generateCalls.Add(1) == 2 {
			gated.Responses["generate_chat_reply"] = json.RawMessage(`{"reply":"second"}`)
		}
	}
	llmClient := gated
	messenger := &someMessengerClient{sentID: "99"}
	app := New(
		messenger,
		store,
		memory.NewPipeline(store, llmClient, nil, prompts.FixedTestRegistry()),
		reply.NewService(store, llmClient, prompts.FixedTestRegistry(), []string{"чатик"}, nil, nil, nil),
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		nil,
	)

	msg := botAddressedMessage()
	procCtx, release, state := app.processor.begin(ctx, msg)
	defer release()

	done := make(chan error, 1)
	go func() {
		done <- app.handleMessageProcessing(procCtx, state, msg, true)
	}()

	select {
	case <-replyStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("reply generation did not start")
	}

	edited := msg
	edited.Text = "Чатик, уточняю: какое завтра число?"
	if err := app.handleMessageUpdated(ctx, edited); err != nil {
		t.Fatal(err)
	}
	close(gate)

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("processing: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("processing did not finish")
	}
	if generateCalls.Load() < 2 {
		t.Fatalf("expected at least two reply generations, got %d", generateCalls.Load())
	}
	if len(messenger.sent) != 1 {
		t.Fatalf("expected one send, got %d", len(messenger.sent))
	}
	if messenger.sent[0].Text != "second" {
		t.Fatalf("expected retried reply %q, got %q", "second", messenger.sent[0].Text)
	}
}

type gatedLLM struct {
	llm.StaticClient
	gate         chan struct{}
	replyStarted chan struct{}
	onGenerate   func()
}

func (c *gatedLLM) CompleteJSON(ctx context.Context, task string, input any, schema string) (json.RawMessage, error) {
	if task == "generate_chat_reply" {
		if c.onGenerate != nil {
			c.onGenerate()
		}
		if c.replyStarted != nil {
			select {
			case c.replyStarted <- struct{}{}:
			default:
			}
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-c.gate:
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
	}
	return c.StaticClient.CompleteJSON(ctx, task, input, schema)
}
