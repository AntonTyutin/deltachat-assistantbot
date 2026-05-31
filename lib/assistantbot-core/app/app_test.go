package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	openai "github.com/sashabaranov/go-openai"

	"github.com/AntonTyutin/assistantbot-core/llm"
	"github.com/AntonTyutin/assistantbot-core/transport"
)

func TestHandleMessageSendsFallbackWhenLLMExhausted(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	defer store.Close()

	llmClient := taskFailClient{
		StaticClient: llm.StaticClient{Responses: map[string]json.RawMessage{
			llm.TaskUpdateProfile:        json.RawMessage(`{"names":{"chat":"Anton"}}`),
			llm.TaskClassifyMessageTopic: json.RawMessage(`{"is_new_topic":true,"title":"date","summary":"date question"}`),
			llm.TaskUpdateTopic:          json.RawMessage(`{"title":"date","summary":"date question","active_participants":["user-1"]}`),
		}},
		failTask: llm.TaskGenerateChatReply,
	}
	messenger := &someMessengerClient{sentID: "25"}
	app := buildTestApp(messenger, store, llmClient, []string{"чатик"})

	err := app.HandleMessage(ctx, botAddressedMessage())
	if err != nil {
		t.Fatal(err)
	}
	if len(messenger.sent) != 1 {
		t.Fatalf("expected one outbound message, got %d", len(messenger.sent))
	}
	if messenger.sent[0].Text != "😵" {
		t.Fatalf("expected fallback reply %q, got %q", "😵", messenger.sent[0].Text)
	}
	if messenger.sent[0].ReplyToID != "24" {
		t.Fatalf("expected reply_to_id 24, got %q", messenger.sent[0].ReplyToID)
	}
}

func TestHandleMessageRepliesDespiteBackgroundMemoryError(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	defer store.Close()

	llmClient := taskFailClient{
		StaticClient: llm.StaticClient{Responses: map[string]json.RawMessage{
			llm.TaskClassifyMessageTopic: json.RawMessage(`{"is_new_topic":true,"title":"date","summary":"date question"}`),
			llm.TaskGenerateChatReply:    json.RawMessage(`{"reply":"ok despite memory error"}`),
			llm.TaskUpdateProfile:        json.RawMessage(`{"city":"","address":"","style":"","verbosity":"","expertise":{},"interests":[]}`),
		}},
		failTask: llm.TaskUpdateProfile,
	}
	messenger := &someMessengerClient{sentID: "25"}
	app := buildTestApp(messenger, store, llmClient, []string{"чатик"})

	err := app.HandleMessage(ctx, botAddressedMessage())
	if err != nil {
		t.Fatal(err)
	}
	if len(messenger.sent) != 1 {
		t.Fatalf("expected one outbound message despite background memory error, got %d", len(messenger.sent))
	}
	if messenger.sent[0].Text != "ok despite memory error" {
		t.Fatalf("unexpected reply text: %q", messenger.sent[0].Text)
	}
}

func TestHandleMessageStoresSentReplyInMemory(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	defer store.Close()

	llmClient := llm.StaticClient{Responses: map[string]json.RawMessage{
		llm.TaskUpdateProfile:        json.RawMessage(`{"names":{"chat":"Anton"}}`),
		llm.TaskClassifyMessageTopic: json.RawMessage(`{"is_new_topic":true,"title":"date","summary":"date question"}`),
		llm.TaskUpdateTopic:          json.RawMessage(`{"title":"date","summary":"date question","active_participants":["user-1"]}`),
		llm.TaskGenerateChatReply:    json.RawMessage(`{"reply":"Сегодня 27 апреля 2026 года."}`),
	}}
	messenger := &someMessengerClient{sentID: "25"}
	app := buildTestApp(messenger, store, llmClient, []string{"чатик"})

	err := app.HandleMessage(ctx, transport.Message{
		ID:       "24",
		ChatID:   "10",
		SenderID: "user-1",
		Sender:   "Anton",
		Text:     "Чатик, какое сегодня число?",
		IsGroup:  true,
		SentAt:   time.Date(2026, 4, 27, 17, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}

	stored, ok, err := store.GetMessage(ctx, "10", "25")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected sent reply to be stored")
	}
	if !stored.IsFromSelf {
		t.Fatal("expected sent reply to be marked as self")
	}
	if stored.ReplyToID != "24" {
		t.Fatalf("expected reply_to_id 24, got %q", stored.ReplyToID)
	}
	if stored.Text != "Сегодня 27 апреля 2026 года." {
		t.Fatalf("unexpected stored reply text: %q", stored.Text)
	}
}

func TestHandleMessageUpdateDuringReplyUsesLatestRevision(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	defer store.Close()

	llmClient := &scriptedLLMClient{
		blockFirstGenerate: true,
		repliesByText: map[string]string{
			"Чатик, первая версия?": "ответ на первую версию",
			"Чатик, вторая версия?": "ответ на вторую версию",
		},
		generateStarted: make(chan struct{}, 1),
	}
	messenger := &someMessengerClient{sentID: "25", sentDone: make(chan struct{}, 4)}
	app := buildTestApp(messenger, store, llmClient, []string{"чатик"})

	app.startMessageProcessing(ctx, transport.Message{
		ID:       "24",
		ChatID:   "10",
		SenderID: "user-1",
		Sender:   "Anton",
		Text:     "Чатик, первая версия?",
		IsGroup:  true,
		SentAt:   time.Date(2026, 4, 27, 17, 0, 0, 0, time.UTC),
	})
	waitForSignal(t, llmClient.generateStarted, "first generate start")

	if err := app.handleMessageUpdated(ctx, transport.Message{
		ID:       "24",
		ChatID:   "10",
		SenderID: "user-1",
		Sender:   "Anton",
		Text:     "Чатик, вторая версия?",
		IsGroup:  true,
		SentAt:   time.Date(2026, 4, 27, 17, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatal(err)
	}

	waitForSignal(t, messenger.sentDone, "reply send")
	sent := messenger.messages()
	if len(sent) != 1 {
		t.Fatalf("expected one outbound message, got %d", len(sent))
	}
	if sent[0].Text != "ответ на вторую версию" {
		t.Fatalf("expected latest revision reply, got %q", sent[0].Text)
	}
}

func TestHandleMessageDeleteDuringReplySkipsSend(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	defer store.Close()

	llmClient := &scriptedLLMClient{
		blockFirstGenerate: true,
		repliesByText: map[string]string{
			"Чатик, вопрос?": "ответ",
		},
		generateStarted: make(chan struct{}, 1),
	}
	messenger := &someMessengerClient{sentID: "25", sentDone: make(chan struct{}, 2)}
	app := buildTestApp(messenger, store, llmClient, []string{"чатик"})

	app.startMessageProcessing(ctx, transport.Message{
		ID:       "24",
		ChatID:   "10",
		SenderID: "user-1",
		Sender:   "Anton",
		Text:     "Чатик, вопрос?",
		IsGroup:  true,
		SentAt:   time.Date(2026, 4, 27, 17, 0, 0, 0, time.UTC),
	})
	waitForSignal(t, llmClient.generateStarted, "first generate start")

	if err := app.handleMessageDeleted(ctx, "10", "24"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	if got := len(messenger.messages()); got != 0 {
		t.Fatalf("expected no outbound messages after delete, got %d", got)
	}
}

func TestHandleMessageDeletedRemovesStoredMessage(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	defer store.Close()
	app := buildTestApp(&someMessengerClient{}, store, testLLMClient(nil), nil)

	msg := transport.Message{
		ID:       "24",
		ChatID:   "10",
		SenderID: "user-1",
		Sender:   "Anton",
		Text:     "hello",
		IsGroup:  true,
		SentAt:   time.Now(),
	}
	if _, err := app.memory.PrepareForReply(ctx, msg); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := store.GetMessage(ctx, "10", "24"); err != nil || !ok {
		t.Fatalf("message not stored: ok=%v err=%v", ok, err)
	}

	if err := app.handleMessageDeleted(ctx, "10", "24"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		_, ok, err := store.GetMessage(ctx, "10", "24")
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("message still present after delete")
}

func TestHandleMessageMultipleUpdatesSendsOnlyLatest(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	defer store.Close()

	llmClient := &scriptedLLMClient{
		blockFirstGenerate: true,
		repliesByText: map[string]string{
			"Чатик, версия 1?": "ответ 1",
			"Чатик, версия 2?": "ответ 2",
			"Чатик, версия 3?": "ответ 3",
		},
		generateStarted: make(chan struct{}, 1),
	}
	messenger := &someMessengerClient{sentID: "25", sentDone: make(chan struct{}, 4)}
	app := buildTestApp(messenger, store, llmClient, []string{"чатик"})

	app.startMessageProcessing(ctx, transport.Message{
		ID:       "24",
		ChatID:   "10",
		SenderID: "user-1",
		Sender:   "Anton",
		Text:     "Чатик, версия 1?",
		IsGroup:  true,
		SentAt:   time.Date(2026, 4, 27, 17, 0, 0, 0, time.UTC),
	})
	waitForSignal(t, llmClient.generateStarted, "first generate start")

	if err := app.handleMessageUpdated(ctx, transport.Message{
		ID:       "24",
		ChatID:   "10",
		SenderID: "user-1",
		Sender:   "Anton",
		Text:     "Чатик, версия 2?",
		IsGroup:  true,
		SentAt:   time.Date(2026, 4, 27, 17, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatal(err)
	}
	if err := app.handleMessageUpdated(ctx, transport.Message{
		ID:       "24",
		ChatID:   "10",
		SenderID: "user-1",
		Sender:   "Anton",
		Text:     "Чатик, версия 3?",
		IsGroup:  true,
		SentAt:   time.Date(2026, 4, 27, 17, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatal(err)
	}

	waitForSignal(t, messenger.sentDone, "reply send")
	sent := messenger.messages()
	if len(sent) != 1 {
		t.Fatalf("expected one outbound message, got %d", len(sent))
	}
	if sent[0].Text != "ответ 3" {
		t.Fatalf("expected latest reply text, got %q", sent[0].Text)
	}
}

func TestStartMessageProcessingDuplicateMessageIDKeepsLatestRun(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	defer store.Close()

	llmClient := &scriptedLLMClient{
		blockFirstGenerate: true,
		repliesByText: map[string]string{
			"Чатик, версия 1?": "ответ 1",
			"Чатик, версия 2?": "ответ 2",
		},
		generateStarted: make(chan struct{}, 1),
	}
	messenger := &someMessengerClient{sentID: "25", sentDone: make(chan struct{}, 4)}
	app := buildTestApp(messenger, store, llmClient, []string{"чатик"})

	app.startMessageProcessing(ctx, transport.Message{
		ID:       "24",
		ChatID:   "10",
		SenderID: "user-1",
		Sender:   "Anton",
		Text:     "Чатик, версия 1?",
		IsGroup:  true,
		SentAt:   time.Date(2026, 4, 27, 17, 0, 0, 0, time.UTC),
	})
	waitForSignal(t, llmClient.generateStarted, "first generate start")

	app.startMessageProcessing(ctx, transport.Message{
		ID:       "24",
		ChatID:   "10",
		SenderID: "user-1",
		Sender:   "Anton",
		Text:     "Чатик, версия 2?",
		IsGroup:  true,
		SentAt:   time.Date(2026, 4, 27, 17, 0, 0, 0, time.UTC),
	})

	waitForSignal(t, messenger.sentDone, "reply send")
	sent := messenger.messages()
	if len(sent) != 1 {
		t.Fatalf("expected one outbound message, got %d", len(sent))
	}
	if sent[0].Text != "ответ 2" {
		t.Fatalf("expected duplicate start to keep latest run, got %q", sent[0].Text)
	}
}

type scriptedLLMClient struct {
	mu                 sync.Mutex
	blockFirstGenerate bool
	generateBlocked    bool
	generateStarted    chan struct{}
	repliesByText      map[string]string
}

func (c *scriptedLLMClient) CompleteJSON(ctx context.Context, task string, input any, schema string) (json.RawMessage, error) {
	switch task {
	case llm.TaskUpdateProfile:
		return json.RawMessage(`{"city":"","address":"","style":"","verbosity":"","expertise":{},"interests":[]}`), nil
	case llm.TaskClassifyMessageTopic:
		return json.RawMessage(`{"is_new_topic":true,"title":"topic","summary":"summary"}`), nil
	case llm.TaskUpdateTopic:
		return json.RawMessage(`{"title":"topic","summary":"summary","decisions":[],"open_questions":[],"active_participants":["user-1"]}`), nil
	case llm.TaskGenerateChatReply:
		return c.generateReplyJSON(ctx, extractMessageText(input))
	default:
		return json.RawMessage(`{}`), nil
	}
}

func (c *scriptedLLMClient) ChatWithTools(ctx context.Context, task string, messages []openai.ChatCompletionMessage, _ []llm.ToolDefinition, _ llm.ToolExecutorFunc) (string, error) {
	if task != llm.TaskGenerateChatReply {
		return "", errors.New("not implemented")
	}
	raw, err := c.generateReplyJSON(ctx, extractMessageTextFromChat(messages))
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func (c *scriptedLLMClient) generateReplyJSON(ctx context.Context, text string) (json.RawMessage, error) {
	c.mu.Lock()
	shouldBlock := c.blockFirstGenerate && !c.generateBlocked
	if shouldBlock {
		c.generateBlocked = true
	}
	started := c.generateStarted
	c.mu.Unlock()
	if shouldBlock {
		if started != nil {
			select {
			case started <- struct{}{}:
			default:
			}
		}
		<-ctx.Done()
		return nil, ctx.Err()
	}
	replyText := c.replyForText(text)
	return json.RawMessage(fmt.Sprintf(`{"reply":%q}`, replyText)), nil
}

func (c *scriptedLLMClient) replyForText(text string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.repliesByText == nil {
		return "ok"
	}
	if replyText, ok := c.repliesByText[text]; ok {
		return replyText
	}
	return "ok"
}

func extractMessageTextFromChat(messages []openai.ChatCompletionMessage) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role != openai.ChatMessageRoleUser {
			continue
		}
		var payload map[string]json.RawMessage
		if err := json.Unmarshal([]byte(messages[i].Content), &payload); err != nil {
			continue
		}
		var message transport.Message
		if err := json.Unmarshal(payload["message"], &message); err != nil {
			continue
		}
		return message.Text
	}
	return ""
}

func extractMessageText(input any) string {
	payload, ok := input.(map[string]any)
	if !ok {
		return ""
	}
	message, ok := payload["message"].(transport.Message)
	if !ok {
		return ""
	}
	return message.Text
}

func waitForSignal(t *testing.T, ch <-chan struct{}, name string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(2 * time.Second):
		t.Fatalf("timeout waiting for %s", name)
	}
}

type taskFailClient struct {
	llm.StaticClient
	failTask string
}

func (c taskFailClient) CompleteJSON(ctx context.Context, task string, input any, schema string) (json.RawMessage, error) {
	if task == c.failTask {
		return nil, fmt.Errorf("%w for task %q: %w", llm.ErrAllModelsFailed, task, errors.New("429 Too Many Requests"))
	}
	return c.StaticClient.CompleteJSON(ctx, task, input, schema)
}

func (c taskFailClient) ChatWithTools(ctx context.Context, task string, messages []openai.ChatCompletionMessage, tools []llm.ToolDefinition, exec llm.ToolExecutorFunc) (string, error) {
	if task == c.failTask {
		return "", fmt.Errorf("%w for task %q: %w", llm.ErrAllModelsFailed, task, errors.New("429 Too Many Requests"))
	}
	return c.StaticClient.ChatWithTools(ctx, task, messages, tools, exec)
}

type someMessengerClient struct {
	mu       sync.Mutex
	sentID   string
	sent     []transport.OutboundMessage
	sentDone chan struct{}
}

func (c *someMessengerClient) Run(context.Context, transport.EventHandlers) error {
	return nil
}

func (c *someMessengerClient) SendText(_ context.Context, message transport.OutboundMessage) (string, error) {
	c.mu.Lock()
	c.sent = append(c.sent, message)
	c.mu.Unlock()
	if c.sentDone != nil {
		select {
		case c.sentDone <- struct{}{}:
		default:
		}
	}
	return c.sentID, nil
}

func (c *someMessengerClient) messages() []transport.OutboundMessage {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]transport.OutboundMessage, len(c.sent))
	copy(out, c.sent)
	return out
}

func (c *someMessengerClient) EditMessage(context.Context, transport.MessageEdit) error {
	return transport.UnsupportedCapabilityError{Capability: transport.CapabilityEditMessage, Transport: "test"}
}

func (c *someMessengerClient) DeleteMessage(context.Context, string, string) error {
	return transport.UnsupportedCapabilityError{Capability: transport.CapabilityDeleteMessage, Transport: "test"}
}

func (c *someMessengerClient) React(context.Context, transport.MessageReaction) error {
	return transport.UnsupportedCapabilityError{Capability: transport.CapabilityReact, Transport: "test"}
}

func (c *someMessengerClient) SetTyping(context.Context, transport.TypingState) error {
	return transport.UnsupportedCapabilityError{Capability: transport.CapabilityTyping, Transport: "test"}
}

func (c *someMessengerClient) SendMedia(context.Context, transport.MediaMessage) (string, error) {
	return "", transport.UnsupportedCapabilityError{Capability: transport.CapabilitySendMedia, Transport: "test"}
}

func botAddressedMessage() transport.Message {
	return transport.Message{
		ID:       "24",
		ChatID:   "10",
		SenderID: "user-1",
		Sender:   "Anton",
		Text:     "Чатик, какое сегодня число?",
		IsGroup:  true,
		SentAt:   time.Date(2026, 4, 27, 17, 0, 0, 0, time.UTC),
	}
}
