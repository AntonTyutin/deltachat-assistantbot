package llm

import (
	"testing"
	"time"

	openai "github.com/sashabaranov/go-openai"
)

type recordingPromptMetrics struct {
	calls []struct {
		task  string
		part  string
		bytes int
	}
}

func (r *recordingPromptMetrics) RecordPromptPartBytes(task, part string, bytes int) {
	r.calls = append(r.calls, struct {
		task  string
		part  string
		bytes int
	}{task, part, bytes})
}

func (r *recordingPromptMetrics) RecordChatCompletion(string, string, string, time.Duration, error, string, int, int, int) {
}
func (r *recordingPromptMetrics) RecordLLMLogicalFailure(string, string, string, string) {}
func (r *recordingPromptMetrics) RecordMCPTool(string, string, string, time.Duration)    {}
func (r *recordingPromptMetrics) RecordReplyGenerate(string, time.Duration)              {}
func (r *recordingPromptMetrics) RecordMessagePhase(string, time.Duration)               {}
func (r *recordingPromptMetrics) RecordInboundMessageHandle(string, time.Duration)       {}
func (r *recordingPromptMetrics) RecordChatMemoryQueueDepth(string, int)                 {}
func (r *recordingPromptMetrics) RecordChatMemoryQueueWait(string, time.Duration)        {}
func (r *recordingPromptMetrics) RecordChatMemoryTaskDuration(string, time.Duration)     {}
func (r *recordingPromptMetrics) RecordReplyToolCall(string, string, string, time.Duration) {
}
func (r *recordingPromptMetrics) RecordInboundMessageToolCallCount(string, int) {}
func (r *recordingPromptMetrics) RecordEmbedding(string, string, time.Duration, error, string, int, int) {
}

func TestNewPromptParts(t *testing.T) {
	tools := []ToolDefinition{
		FunctionToolDefinition(openai.Tool{
			Type: openai.ToolTypeFunction,
			Function: &openai.FunctionDefinition{
				Name:        "memory_add_note",
				Description: "add note",
			},
		}),
	}
	parts := NewPromptParts("base system", "mcp hint", "memory hint", `{"x":1}`, tools)
	if parts.System != len("base system") {
		t.Fatalf("system bytes: got %d", parts.System)
	}
	wantTools := len("mcp hint\nmemory hint")
	if parts.Tools != wantTools {
		t.Fatalf("tools append bytes: got %d want %d", parts.Tools, wantTools)
	}
	if parts.ToolsDefinitions <= 0 {
		t.Fatal("expected positive tools_definitions bytes")
	}
	if parts.User != len(`{"x":1}`) {
		t.Fatalf("user bytes: got %d", parts.User)
	}
}

func TestPromptPartsRecord(t *testing.T) {
	rec := &recordingPromptMetrics{}
	PromptParts{System: 10, Tools: 5, ToolsDefinitions: 20, User: 30}.Record(rec, "generate_chat_reply")
	if len(rec.calls) != 4 {
		t.Fatalf("calls: got %d", len(rec.calls))
	}
}

func TestCompleteJSONUserPrompt(t *testing.T) {
	got := CompleteJSONUserPrompt("update_chat_topic", `{"title":""}`, []byte(`{"a":1}`))
	if got == "" {
		t.Fatal("expected non-empty user prompt")
	}
}
