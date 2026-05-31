package metrics

import "time"

// Method labels for LLM metrics.
const (
	MethodCompleteJSON       = "complete_json"
	MethodChatCompletion     = "chat_completion"
	ReplyPathJSON            = "json"
	ReplyPathMCPTools        = "mcp_tools"
	ReplyPathOpenRouterTools = "openrouter_tools"
	ReplyPathMixedTools      = "mixed_tools"
	PhaseMemoryPrepare       = "memory_prepare"
	PhaseMemoryBackground    = "memory_background"
	PhaseMemoryOutbound      = "memory_outbound"
	PhaseMemoryNewMessage    = "memory_new_message"
	PhaseMemoryTopicAssign   = "memory_topic_assign"
	PhaseMemoryMessageUpsert = "memory_message_upsert"
	PhaseMemoryProfileUpdate = "memory_profile_update"
	PhaseMemoryProfilePatch  = "memory_profile_patch"
	PhaseMemoryTopicUpdate   = "memory_topic_update"
	PhaseMemoryTopicPatch    = "memory_topic_patch"
	PhaseMemoryDelete        = "memory_delete"
	PhaseReply               = "reply"
	PhaseSend                = "send"
	ToolSourceMemory         = "memory"
	ToolSourceMCP            = "mcp"
	// InboundMessageHandleResult labels for RecordInboundMessageHandle.
	ResultReplied        = "replied"
	ResultNoReply        = "no_reply"
	ResultError          = "error"
	OutcomeToolLoopLimit = "tool_loop_limit"
	OutcomeUnknownTool   = "unknown_tool"
	OutcomeInvalidArgs   = "invalid_args"
	OutcomeNoSession     = "no_session"
)

// Recorder collects runtime metrics for LLM, MCP, and reply paths.
// Implementations must be safe for concurrent use.
type Recorder interface {
	// RecordChatCompletion records one CreateChatCompletion HTTP call.
	// If apiErr is non-nil, responseOutcome and token counts are ignored.
	RecordChatCompletion(task, model, method string, dur time.Duration, apiErr error, responseOutcome string, promptTokens, completionTokens, totalTokens int)
	// RecordLLMLogicalFailure records a task-level LLM outcome without an HTTP round-trip (e.g. tool loop limit).
	RecordLLMLogicalFailure(task, model, method, outcome string)
	RecordMCPTool(server, tool, outcome string, dur time.Duration)
	RecordReplyGenerate(path string, dur time.Duration)
	RecordMessagePhase(phase string, dur time.Duration)
	// RecordInboundMessageHandle records wall-clock time for HandleMessage end-to-end
	// (from after SentAt normalization through return). result is ResultReplied, ResultNoReply, or ResultError.
	RecordInboundMessageHandle(result string, dur time.Duration)

	// RecordChatMemoryQueueDepth records the number of queued per-chat memory tasks at enqueue time.
	// queue is "prepare" or "background".
	RecordChatMemoryQueueDepth(queue string, depth int)
	// RecordChatMemoryQueueWait records how long a task waited in the per-chat FIFO queue.
	RecordChatMemoryQueueWait(queue string, dur time.Duration)
	// RecordChatMemoryTaskDuration records how long the task function ran.
	RecordChatMemoryTaskDuration(queue string, dur time.Duration)

	// RecordReplyToolCall records one tool invocation during reply generation.
	// source is ToolSourceMemory or ToolSourceMCP; tool is the function name passed to the LLM.
	RecordReplyToolCall(source, tool, outcome string, dur time.Duration)
	// RecordInboundMessageToolCallCount records how many reply-path tool calls of source ran for one inbound message.
	RecordInboundMessageToolCallCount(source string, count int)
}

// Noop is a Recorder that discards all events.
var Noop Recorder = noopRecorder{}

type noopRecorder struct{}

func (noopRecorder) RecordChatCompletion(string, string, string, time.Duration, error, string, int, int, int) {
}
func (noopRecorder) RecordLLMLogicalFailure(string, string, string, string) {}
func (noopRecorder) RecordMCPTool(string, string, string, time.Duration)    {}
func (noopRecorder) RecordReplyGenerate(string, time.Duration)              {}
func (noopRecorder) RecordMessagePhase(string, time.Duration)               {}
func (noopRecorder) RecordInboundMessageHandle(string, time.Duration)       {}
func (noopRecorder) RecordChatMemoryQueueDepth(string, int)                 {}
func (noopRecorder) RecordChatMemoryQueueWait(string, time.Duration)        {}
func (noopRecorder) RecordChatMemoryTaskDuration(string, time.Duration)     {}
func (noopRecorder) RecordReplyToolCall(string, string, string, time.Duration) {
}
func (noopRecorder) RecordInboundMessageToolCallCount(string, int) {}
