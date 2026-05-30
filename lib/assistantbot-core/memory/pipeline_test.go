package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	openai "github.com/sashabaranov/go-openai"

	"github.com/AntonTyutin/assistantbot-core/llm"
	"github.com/AntonTyutin/assistantbot-core/llm/prompts"
	"github.com/AntonTyutin/assistantbot-core/storage"
	"github.com/AntonTyutin/assistantbot-core/transport"
)

func TestProcessMessageUpdatesProfileAndTopic(t *testing.T) {
	ctx := context.Background()
	store, err := storage.Open(ctx, filepath.Join(t.TempDir(), "test.db"), "secret")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	pipeline := NewPipeline(store, llm.StaticClient{Responses: map[string]json.RawMessage{
		llm.TaskUpdateProfile: json.RawMessage(`{"city":"Тбилиси","address":"Tbilisi, Vake","style":"calm","verbosity":"short","expertise":{"go":"medium"},"interests":["llm"]}`),
		llm.TaskUpdateTopic:   json.RawMessage(`{"title":"Go и LLM","summary":"Обсуждают Go-бота с LLM","decisions":["писать на Go"],"open_questions":[],"active_participants":["user-1"]}`),
	}}, nil, prompts.FixedTestRegistry())

	topic, err := pipeline.ProcessMessage(ctx, transport.Message{
		ID:       "msg-1",
		ChatID:   "chat-1",
		SenderID: "user-1",
		Sender:   "Алексей",
		Text:     "Давайте писать бота на Go с LLM",
		IsGroup:  true,
		SentAt:   time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if topic.Title != "Go и LLM" {
		t.Fatalf("unexpected topic title: %q", topic.Title)
	}
	profile, ok, err := store.GetProfile(ctx, "user-1")
	if err != nil || !ok {
		t.Fatalf("profile missing: ok=%v err=%v", ok, err)
	}
	if profile.Expertise["go"] != "medium" {
		t.Fatalf("expected go expertise, got %#v", profile.Expertise)
	}
	if profile.City != "Тбилиси" {
		t.Fatalf("expected city from profile patch, got %q", profile.City)
	}
	if profile.Address != "Tbilisi, Vake" {
		t.Fatalf("expected address from profile patch, got %q", profile.Address)
	}
	name, ok, err := store.ChatName(ctx, "chat-1", "user-1")
	if err != nil || !ok || name != "Алексей" {
		t.Fatalf("unexpected chat name: name=%q ok=%v err=%v", name, ok, err)
	}
}

func TestPrepareForReplyCreatesTopicWithoutLLM(t *testing.T) {
	ctx := context.Background()
	store, err := storage.Open(ctx, filepath.Join(t.TempDir(), "test.db"), "secret")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	pipeline := NewPipeline(store, llm.StaticClient{Responses: map[string]json.RawMessage{}}, nil, prompts.FixedTestRegistry())
	message := transport.Message{
		ID:       "msg-1",
		ChatID:   "chat-1",
		SenderID: "user-1",
		Sender:   "Alex",
		Text:     "Давайте писать бота на Go",
		IsGroup:  true,
		SentAt:   time.Now(),
	}
	topic, err := pipeline.PrepareForReply(ctx, message)
	if err != nil {
		t.Fatal(err)
	}
	if topic.Title == "" {
		t.Fatal("expected heuristic topic title")
	}
	if topic.Summary != message.Text {
		t.Fatalf("expected summary from message text, got %q", topic.Summary)
	}
	topicID, ok, err := store.TopicIDForMessage(ctx, "chat-1", "msg-1")
	if err != nil || !ok || topicID != topic.ID {
		t.Fatalf("message not attached to topic: topicID=%q ok=%v err=%v", topicID, ok, err)
	}
	stored, ok, err := store.GetMessage(ctx, "chat-1", "msg-1")
	if err != nil || !ok || stored.Text != message.Text {
		t.Fatalf("message not stored: ok=%v err=%v text=%q", ok, err, stored.Text)
	}
}

func TestPrepareForReplyReturnsExistingTopicWithoutRebuild(t *testing.T) {
	ctx := context.Background()
	store, err := storage.Open(ctx, filepath.Join(t.TempDir(), "test.db"), "secret")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	pipeline := NewPipeline(store, llm.StaticClient{Responses: map[string]json.RawMessage{
		llm.TaskUpdateProfile: json.RawMessage(`{}`),
		llm.TaskUpdateTopic:   json.RawMessage(`{"title":"LLM topic","summary":"LLM summary","active_participants":["user-1"]}`),
	}}, nil, prompts.FixedTestRegistry())
	message := transport.Message{
		ID:       "msg-1",
		ChatID:   "chat-1",
		SenderID: "user-1",
		Text:     "original",
		IsGroup:  true,
		SentAt:   time.Now(),
	}
	if _, err := pipeline.ProcessNewMessage(ctx, message); err != nil {
		t.Fatal(err)
	}

	message.Text = "edited without LLM"
	topic, err := pipeline.PrepareForReply(ctx, message)
	if err != nil {
		t.Fatal(err)
	}
	if topic.Title != "LLM topic" {
		t.Fatalf("expected existing topic unchanged, got title %q", topic.Title)
	}
	stored, ok, err := store.GetMessage(ctx, "chat-1", "msg-1")
	if err != nil || !ok || stored.Text != "edited without LLM" {
		t.Fatalf("expected updated message text in store, got ok=%v text=%q", ok, stored.Text)
	}
}

func TestProcessMessageUpdateRebuildsTopicFromEditedText(t *testing.T) {
	ctx := context.Background()
	store, err := storage.Open(ctx, filepath.Join(t.TempDir(), "test.db"), "secret")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	pipeline := NewPipeline(store, rebuildClient{}, nil, prompts.FixedTestRegistry())
	message := transport.Message{
		ID:       "msg-1",
		ChatID:   "chat-1",
		SenderID: "user-1",
		Sender:   "Alex",
		Text:     "old text",
		IsGroup:  true,
		SentAt:   time.Now(),
	}
	topic, err := pipeline.ProcessNewMessage(ctx, message)
	if err != nil {
		t.Fatal(err)
	}

	message.Text = "new edited text"
	if err := pipeline.ProcessMessageUpdate(ctx, message); err != nil {
		t.Fatal(err)
	}
	rebuilt, ok, err := store.GetTopic(ctx, topic.ID)
	if err != nil || !ok {
		t.Fatalf("topic missing: ok=%v err=%v", ok, err)
	}
	if !strings.Contains(rebuilt.Summary, "new edited text") {
		t.Fatalf("summary does not include edited text: %q", rebuilt.Summary)
	}
	if strings.Contains(rebuilt.Summary, "old text") {
		t.Fatalf("summary still includes old text: %q", rebuilt.Summary)
	}
}

func TestProcessMessageDeleteRebuildsTopicAndProfile(t *testing.T) {
	ctx := context.Background()
	store, err := storage.Open(ctx, filepath.Join(t.TempDir(), "test.db"), "secret")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	pipeline := NewPipeline(store, rebuildClient{}, nil, prompts.FixedTestRegistry())
	message := transport.Message{
		ID:       "msg-1",
		ChatID:   "chat-1",
		SenderID: "user-1",
		Sender:   "Alex",
		Text:     "Alex is expert in rockets",
		IsGroup:  true,
		SentAt:   time.Now(),
	}
	topic, err := pipeline.ProcessNewMessage(ctx, message)
	if err != nil {
		t.Fatal(err)
	}
	if err := pipeline.ProcessMessageDelete(ctx, "chat-1", "msg-1"); err != nil {
		t.Fatal(err)
	}

	rebuilt, ok, err := store.GetTopic(ctx, topic.ID)
	if err != nil || !ok {
		t.Fatalf("topic missing: ok=%v err=%v", ok, err)
	}
	if strings.Contains(rebuilt.Summary, "rockets") {
		t.Fatalf("summary still includes deleted text: %q", rebuilt.Summary)
	}
	profile, ok, err := store.GetProfile(ctx, "user-1")
	if err != nil || !ok {
		t.Fatalf("profile missing: ok=%v err=%v", ok, err)
	}
	if len(profile.Expertise) != 0 {
		t.Fatalf("deleted source should not keep expertise: %#v", profile.Expertise)
	}
}

type rebuildClient struct{}

func (rebuildClient) CompleteJSON(_ context.Context, task string, input any, _ string) (json.RawMessage, error) {
	payload, _ := json.Marshal(input)
	var decoded map[string]json.RawMessage
	_ = json.Unmarshal(payload, &decoded)

	switch task {
	case llm.TaskUpdateProfile:
		return json.RawMessage(`{"city":"Воронеж","address":"Воронеж, юг","style":"direct","verbosity":"short","expertise":{"rockets":"high"},"interests":["rockets"]}`), nil
	case llm.TaskUpdateTopic, llm.TaskRebuildTopic:
		var messages []storage.Message
		_ = json.Unmarshal(decoded["messages"], &messages)
		if len(messages) == 0 {
			_ = json.Unmarshal(decoded["recent"], &messages)
		}
		summaryParts := make([]string, 0, len(messages))
		participants := make([]string, 0, len(messages))
		for _, message := range messages {
			if message.Text != "" {
				summaryParts = append(summaryParts, message.Text)
			}
			if message.SenderID != "" && !stringSliceContains(participants, message.SenderID) {
				participants = append(participants, message.SenderID)
			}
		}
		response, _ := json.Marshal(map[string]any{
			"title":               "rebuilt",
			"summary":             strings.Join(summaryParts, "\n"),
			"decisions":           []string{},
			"open_questions":      []string{},
			"active_participants": participants,
		})
		return response, nil
	case llm.TaskRebuildProfile:
		var messages []storage.Message
		_ = json.Unmarshal(decoded["messages"], &messages)
		expertise := map[string]string{}
		interests := []string{}
		for _, message := range messages {
			if strings.Contains(message.Text, "rockets") {
				expertise["rockets"] = "high"
				interests = append(interests, "rockets")
			}
		}
		response, _ := json.Marshal(map[string]any{
			"names":     map[string]string{"self": "Alex"},
			"city":      "Воронеж",
			"address":   "Воронеж, юг",
			"style":     "direct",
			"verbosity": "short",
			"expertise": expertise,
			"interests": interests,
		})
		return response, nil
	default:
		return json.RawMessage(`{}`), nil
	}
}

func (rebuildClient) ChatWithTools(context.Context, string, []openai.ChatCompletionMessage, []llm.ToolDefinition, llm.ToolExecutorFunc) (string, error) {
	return "", fmt.Errorf("unexpected ChatWithTools")
}

func stringSliceContains(items []string, needle string) bool {
	for _, item := range items {
		if item == needle {
			return true
		}
	}
	return false
}

func TestUpdateParticipantLocationFromCoordinates(t *testing.T) {
	ctx := context.Background()
	store, err := storage.Open(ctx, filepath.Join(t.TempDir(), "test.db"), "secret")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	pipeline := NewPipeline(store, locationAwareClient{}, fakeMCPTools{}, prompts.FixedTestRegistry())
	err = pipeline.UpdateParticipantLocationFromCoordinates(ctx, "user-geo", 41.7151, 44.8271)
	if err != nil {
		t.Fatal(err)
	}

	profile, ok, err := store.GetProfile(ctx, "user-geo")
	if err != nil || !ok {
		t.Fatalf("profile missing: ok=%v err=%v", ok, err)
	}
	if profile.City != "Тбилиси" {
		t.Fatalf("expected city from coordinates, got %q", profile.City)
	}
	if profile.Address != "Тбилиси, Грузия" {
		t.Fatalf("expected address from coordinates, got %q", profile.Address)
	}
}

type locationAwareClient struct{}

func (locationAwareClient) CompleteJSON(context.Context, string, any, string) (json.RawMessage, error) {
	return json.RawMessage(`{"style":"calm","verbosity":"short"}`), nil
}

func (locationAwareClient) ChatWithTools(_ context.Context, task string, _ []openai.ChatCompletionMessage, tools []llm.ToolDefinition, _ llm.ToolExecutorFunc) (string, error) {
	if task != llm.TaskChatWithTools {
		return "", fmt.Errorf("unexpected task: %s", task)
	}
	if len(tools) == 0 {
		return "", fmt.Errorf("expected MCP tools for location resolve")
	}
	return `{"city":"Тбилиси","address":"Тбилиси, Грузия"}`, nil
}

type fakeMCPTools struct{}

func (fakeMCPTools) ToolsForTask(task string) []llm.ToolDefinition {
	if task != llm.TaskChatWithTools {
		return nil
	}
	return []llm.ToolDefinition{{
		Type: string(openai.ToolTypeFunction),
		Function: &openai.FunctionDefinition{
			Name:       "mcp__example_tool",
			Parameters: map[string]any{"type": "object"},
		},
	}}
}

func (fakeMCPTools) HasToolsForTask(task string) bool {
	return len(fakeMCPTools{}.ToolsForTask(task)) > 0
}

func (fakeMCPTools) ExecuteTool(context.Context, string, string) (string, error) {
	return "", nil
}

func (fakeMCPTools) SystemPromptAppendForTask(string) string {
	return ""
}
