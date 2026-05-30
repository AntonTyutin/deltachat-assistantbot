package app

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/AntonTyutin/assistantbot-core/llm"
	"github.com/AntonTyutin/assistantbot-core/llm/prompts"
	"github.com/AntonTyutin/assistantbot-core/memory"
	"github.com/AntonTyutin/assistantbot-core/reply"
	"github.com/AntonTyutin/assistantbot-core/storage"
	"github.com/AntonTyutin/assistantbot-core/transport"
)

func TestPrepareForReplyWaitsForChatMemoryLock(t *testing.T) {
	ctx := context.Background()
	store, err := storage.Open(ctx, filepath.Join(t.TempDir(), "test.db"), "test-secret")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	app := New(
		&someMessengerClient{},
		store,
		memory.NewPipeline(store, llm.StaticClient{}, nil, prompts.FixedTestRegistry()),
		reply.NewService(store, llm.StaticClient{}, prompts.FixedTestRegistry(), nil, nil, nil, nil),
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		nil,
	)

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

func TestUpdateDailySummaryWaitsForChatMemoryLock(t *testing.T) {
	ctx := context.Background()
	store, err := storage.Open(ctx, filepath.Join(t.TempDir(), "test.db"), "test-secret")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	llmClient := llm.StaticClient{Responses: map[string]json.RawMessage{
		"daily_summary": json.RawMessage(`{"summary":"ok"}`),
	}}
	app := New(
		&someMessengerClient{},
		store,
		memory.NewPipeline(store, llmClient, nil, prompts.FixedTestRegistry()),
		reply.NewService(store, llmClient, prompts.FixedTestRegistry(), nil, nil, nil, nil),
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		nil,
	)

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

	summaryStarted := make(chan struct{})
	summaryDone := make(chan struct{})
	go func() {
		close(summaryStarted)
		_ = app.UpdateDailySummary(ctx, "chat-1", time.Date(2026, 4, 27, 0, 0, 0, 0, time.UTC))
		close(summaryDone)
	}()
	<-summaryStarted
	select {
	case <-summaryDone:
		t.Fatal("summary should block while chat memory lock is held")
	case <-time.After(100 * time.Millisecond):
	}
	close(gate)
	select {
	case <-summaryDone:
	case <-time.After(2 * time.Second):
		t.Fatal("summary did not finish after lock release")
	}
}

func TestDecideDoesNotWaitOnChatMemoryLock(t *testing.T) {
	ctx := context.Background()
	store, err := storage.Open(ctx, filepath.Join(t.TempDir(), "test.db"), "test-secret")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	llmClient := llm.StaticClient{Responses: map[string]json.RawMessage{
		"generate_chat_reply": json.RawMessage(`{"reply":"ok"}`),
	}}
	app := New(
		&someMessengerClient{},
		store,
		memory.NewPipeline(store, llmClient, nil, prompts.FixedTestRegistry()),
		reply.NewService(store, llmClient, prompts.FixedTestRegistry(), []string{"чатик"}, nil, nil, nil),
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		nil,
	)

	gate := make(chan struct{})
	holdDone := make(chan struct{})
	go func() {
		_ = app.chatMemory.runBackground(ctx, "10", func(context.Context) error {
			close(holdDone)
			<-gate
			return nil
		})
	}()
	<-holdDone

	start := time.Now()
	_, _, err = app.replies.Decide(ctx, botAddressedMessage(), storage.Topic{ID: "topic-1", ChatID: "10", Summary: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed > 200*time.Millisecond {
		t.Fatalf("Decide should not wait on chat memory queue, took %v", elapsed)
	}
	close(gate)
}
