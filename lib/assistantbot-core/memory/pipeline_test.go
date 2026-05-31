package memory

import (
	"context"
	"encoding/json"
	"fmt"
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
	pipeline, store := openTestPipeline(t, llm.StaticClient{Responses: map[string]json.RawMessage{
		llm.TaskUpdateProfile:        json.RawMessage(`{"city":"Тбилиси","address":"Tbilisi, Vake","style":"calm","verbosity":"short","expertise":{"go":"medium"},"interests":["llm"]}`),
		llm.TaskClassifyMessageTopic: classifyNewTopicResponse("Go и LLM", "Обсуждают Go-бота с LLM"),
		llm.TaskUpdateTopic:          json.RawMessage(`{"title":"Go и LLM","summary":"Обсуждают Go-бота с LLM","decisions":["писать на Go"],"open_questions":[],"active_participants":["user-1"]}`),
	}})

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
}

func TestPrepareForReplyAssignsTopic(t *testing.T) {
	ctx := context.Background()
	pipeline, store := openTestPipeline(t, llm.StaticClient{Responses: map[string]json.RawMessage{
		llm.TaskClassifyMessageTopic: classifyNewTopicResponse("Go бот", "Давайте писать бота на Go"),
	}})
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
		t.Fatal("expected topic title")
	}
	if mustTopicID(t, store, "chat-1", "msg-1") != topic.ID {
		t.Fatal("message not attached to topic")
	}
}

func TestProcessMessageUpdatePatchesTopicFromEditedText(t *testing.T) {
	ctx := context.Background()
	pipeline, store := openTestPipeline(t, patchClient{})

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
}

type patchClient struct{}

func (patchClient) CompleteJSON(_ context.Context, task string, input any, _ string) (json.RawMessage, error) {
	payload, _ := json.Marshal(input)
	var decoded map[string]json.RawMessage
	_ = json.Unmarshal(payload, &decoded)

	switch task {
	case llm.TaskUpdateProfile:
		return json.RawMessage(`{"city":"Voronezh","address":"Voronezh, south","style":"direct","verbosity":"short","expertise":{"rockets":"high"},"interests":["rockets"]}`), nil
	case llm.TaskClassifyMessageTopic:
		return classifyNewTopicResponse("patched", "seed"), nil
	case llm.TaskUpdateTopic:
		var message storage.Message
		_ = json.Unmarshal(decoded["message"], &message)
		response, _ := json.Marshal(map[string]any{
			"title":               "patched",
			"summary":             message.Text,
			"decisions":           []string{},
			"open_questions":      []string{},
			"active_participants": []string{message.SenderID},
		})
		return response, nil
	default:
		return json.RawMessage(`{}`), nil
	}
}

func (patchClient) Embed(_ context.Context, texts ...string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i := range texts {
		out[i] = []float32{0.1, 0.2, 0.3}
	}
	return out, nil
}

func (patchClient) ChatWithTools(context.Context, string, []openai.ChatCompletionMessage, []llm.ToolDefinition, llm.ToolExecutorFunc) (string, error) {
	return "", fmt.Errorf("unexpected ChatWithTools")
}

func TestUpdateParticipantLocationFromCoordinates(t *testing.T) {
	ctx := context.Background()
	store := storage.OpenTestDB(t, "secret")
	defer store.Close()
	pipeline := NewPipeline(store, locationAwareClient{}, locationAwareClient{}, fakeMCPTools{}, prompts.FixedTestRegistry(), Config{})
	err := pipeline.UpdateParticipantLocationFromCoordinates(ctx, "user-geo", 41.7151, 44.8271)
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
}

type locationAwareClient struct{}

func (locationAwareClient) CompleteJSON(context.Context, string, any, string) (json.RawMessage, error) {
	return json.RawMessage(`{"style":"calm","verbosity":"short"}`), nil
}

func (locationAwareClient) Embed(_ context.Context, texts ...string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i := range texts {
		out[i] = []float32{0.1, 0.2, 0.3}
	}
	return out, nil
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
