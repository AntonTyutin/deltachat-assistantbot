package app

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	openai "github.com/sashabaranov/go-openai"

	"github.com/AntonTyutin/assistantbot-core/llm"
)

func TestStartMessageProcessingIsAsync(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	defer store.Close()

	gate := make(chan struct{})
	replyStarted := make(chan struct{}, 1)
	messenger := &someMessengerClient{sentID: "25", sentDone: make(chan struct{}, 1)}
	llmClient := &gatedLLM{
		StaticClient: llm.StaticClient{Responses: map[string]json.RawMessage{
			llm.TaskUpdateProfile:        json.RawMessage(`{"names":{"chat":"Anton"}}`),
			llm.TaskClassifyMessageTopic: json.RawMessage(`{"is_new_topic":true,"title":"date","summary":"date question"}`),
			llm.TaskUpdateTopic:          json.RawMessage(`{"title":"date","summary":"date question","active_participants":["user-1"]}`),
			llm.TaskGenerateChatReply:    json.RawMessage(`{"reply":"ok"}`),
		}},
		gate:         gate,
		replyStarted: replyStarted,
	}
	app := buildTestApp(messenger, store, llmClient, []string{"чатик"})

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
	store := openTestStore(t)
	defer store.Close()

	gate := make(chan struct{})
	replyStarted := make(chan struct{}, 1)
	llmClient := &gatedLLM{
		StaticClient: llm.StaticClient{Responses: map[string]json.RawMessage{
			llm.TaskUpdateProfile:        json.RawMessage(`{"names":{"chat":"Anton"}}`),
			llm.TaskClassifyMessageTopic: json.RawMessage(`{"is_new_topic":true,"title":"date","summary":"date question"}`),
			llm.TaskUpdateTopic:          json.RawMessage(`{"title":"date","summary":"date question","active_participants":["user-1"]}`),
			llm.TaskGenerateChatReply:    json.RawMessage(`{"reply":"should not send"}`),
		}},
		gate:         gate,
		replyStarted: replyStarted,
	}
	messenger := &someMessengerClient{sentID: "99"}
	app := buildTestApp(messenger, store, llmClient, []string{"чатик"})

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
	store := openTestStore(t)
	defer store.Close()

	var generateCalls atomic.Int32
	gate := make(chan struct{})
	replyStarted := make(chan struct{}, 2)
	gated := &gatedLLM{
		StaticClient: llm.StaticClient{Responses: map[string]json.RawMessage{
			llm.TaskUpdateProfile:        json.RawMessage(`{"names":{"chat":"Anton"}}`),
			llm.TaskClassifyMessageTopic: json.RawMessage(`{"is_new_topic":true,"title":"date","summary":"date question"}`),
			llm.TaskUpdateTopic:          json.RawMessage(`{"title":"date","summary":"date question","active_participants":["user-1"]}`),
			llm.TaskGenerateChatReply:    json.RawMessage(`{"reply":"first"}`),
		}},
		gate:         gate,
		replyStarted: replyStarted,
	}
	gated.onGenerate = func() {
		if generateCalls.Add(1) == 2 {
			gated.Responses[llm.TaskGenerateChatReply] = json.RawMessage(`{"reply":"second"}`)
		}
	}
	llmClient := gated
	messenger := &someMessengerClient{sentID: "99"}
	app := buildTestApp(messenger, store, llmClient, []string{"чатик"})

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
	if task == llm.TaskGenerateChatReply {
		if err := c.waitForGenerateGate(ctx); err != nil {
			return nil, err
		}
	}
	return c.StaticClient.CompleteJSON(ctx, task, input, schema)
}

func (c *gatedLLM) ChatWithTools(ctx context.Context, task string, messages []openai.ChatCompletionMessage, tools []llm.ToolDefinition, exec llm.ToolExecutorFunc) (string, error) {
	if task == llm.TaskGenerateChatReply {
		if err := c.waitForGenerateGate(ctx); err != nil {
			return "", err
		}
	}
	return c.StaticClient.ChatWithTools(ctx, task, messages, tools, exec)
}

func (c *gatedLLM) waitForGenerateGate(ctx context.Context) error {
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
		return ctx.Err()
	case <-c.gate:
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return nil
}
