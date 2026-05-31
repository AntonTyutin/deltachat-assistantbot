package reply

import (
	"context"
	"strings"
	"time"

	"github.com/AntonTyutin/assistantbot-core/llm"
	"github.com/AntonTyutin/assistantbot-core/metrics"
)

type replyToolTracker struct {
	recorder metrics.Recorder
	counts   map[string]int
}

func newReplyToolTracker(recorder metrics.Recorder) *replyToolTracker {
	if recorder == nil {
		recorder = metrics.Noop
	}
	return &replyToolTracker{
		recorder: recorder,
		counts: map[string]int{
			metrics.ToolSourceMemory: 0,
			metrics.ToolSourceMCP:    0,
		},
	}
}

func (t *replyToolTracker) wrap(exec llm.ToolExecutorFunc) llm.ToolExecutorFunc {
	return func(ctx context.Context, toolName, argumentsJSON string) (string, error) {
		start := time.Now()
		result, err := exec(ctx, toolName, argumentsJSON)
		source := replyToolSource(toolName)
		outcome := "success"
		if err != nil {
			outcome = "error"
		}
		t.recorder.RecordReplyToolCall(source, toolName, outcome, time.Since(start))
		t.counts[source]++
		return result, err
	}
}

func (t *replyToolTracker) flush() {
	t.recorder.RecordInboundMessageToolCallCount(metrics.ToolSourceMemory, t.counts[metrics.ToolSourceMemory])
	t.recorder.RecordInboundMessageToolCallCount(metrics.ToolSourceMCP, t.counts[metrics.ToolSourceMCP])
}

func replyToolSource(toolName string) string {
	if strings.HasPrefix(toolName, "memory_") {
		return metrics.ToolSourceMemory
	}
	return metrics.ToolSourceMCP
}
