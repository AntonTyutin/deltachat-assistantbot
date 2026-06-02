package reply

import (
	"context"
	"testing"
	"time"

	"github.com/AntonTyutin/assistantbot-core/metrics"
)

type recordingToolMetrics struct {
	calls  []toolCallRecord
	counts []sourceCountRecord
}

type toolCallRecord struct {
	source  string
	tool    string
	outcome string
}

type sourceCountRecord struct {
	source string
	count  int
}

func (r *recordingToolMetrics) RecordChatCompletion(string, string, string, time.Duration, error, string, int, int, int) {
}
func (r *recordingToolMetrics) RecordLLMLogicalFailure(string, string, string, string) {}
func (r *recordingToolMetrics) RecordMCPTool(string, string, string, time.Duration)    {}
func (r *recordingToolMetrics) RecordReplyGenerate(string, time.Duration)              {}
func (r *recordingToolMetrics) RecordMessagePhase(string, time.Duration)               {}
func (r *recordingToolMetrics) RecordInboundMessageHandle(string, time.Duration)       {}
func (r *recordingToolMetrics) RecordChatMemoryQueueDepth(string, int)                 {}
func (r *recordingToolMetrics) RecordChatMemoryQueueWait(string, time.Duration)        {}
func (r *recordingToolMetrics) RecordChatMemoryTaskDuration(string, time.Duration)     {}

func (r *recordingToolMetrics) RecordReplyToolCall(source, tool, outcome string, _ time.Duration) {
	r.calls = append(r.calls, toolCallRecord{source: source, tool: tool, outcome: outcome})
}

func (r *recordingToolMetrics) RecordPromptPartBytes(string, string, int) {}

func (r *recordingToolMetrics) RecordInboundMessageToolCallCount(source string, count int) {
	r.counts = append(r.counts, sourceCountRecord{source: source, count: count})
}

func TestReplyToolTrackerRecordsMemoryAndMCPCalls(t *testing.T) {
	rec := &recordingToolMetrics{}
	tracker := newReplyToolTracker(rec)
	exec := tracker.wrap(func(_ context.Context, toolName, _ string) (string, error) {
		if toolName == "memory_add_note" {
			return `{"status":"saved"}`, nil
		}
		return "", context.Canceled
	})

	if _, err := exec(context.Background(), "memory_add_note", `{}`); err != nil {
		t.Fatal(err)
	}
	if _, err := exec(context.Background(), "mcp__web__search", `{}`); err == nil {
		t.Fatal("expected MCP call error")
	}

	tracker.flush()

	if len(rec.calls) != 2 {
		t.Fatalf("calls = %d, want 2", len(rec.calls))
	}
	if rec.calls[0].source != metrics.ToolSourceMemory || rec.calls[0].tool != "memory_add_note" || rec.calls[0].outcome != "success" {
		t.Fatalf("unexpected memory call: %#v", rec.calls[0])
	}
	if rec.calls[1].source != metrics.ToolSourceMCP || rec.calls[1].outcome != "error" {
		t.Fatalf("unexpected mcp call: %#v", rec.calls[1])
	}
	if len(rec.counts) != 2 {
		t.Fatalf("counts = %d, want 2", len(rec.counts))
	}
	for _, item := range rec.counts {
		switch item.source {
		case metrics.ToolSourceMemory:
			if item.count != 1 {
				t.Fatalf("memory count = %d", item.count)
			}
		case metrics.ToolSourceMCP:
			if item.count != 1 {
				t.Fatalf("mcp count = %d", item.count)
			}
		default:
			t.Fatalf("unexpected source %q", item.source)
		}
	}
}

func TestReplyToolTrackerUsesNoopRecorderWhenNil(t *testing.T) {
	tracker := newReplyToolTracker(nil)
	exec := tracker.wrap(func(context.Context, string, string) (string, error) {
		return "ok", nil
	})
	if _, err := exec(context.Background(), "memory_read_list", `{}`); err != nil {
		t.Fatal(err)
	}
	tracker.flush()
}
