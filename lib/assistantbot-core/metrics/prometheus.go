package metrics

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	openai "github.com/sashabaranov/go-openai"
)

const namespace = "dc_assistantbot"

var llmBuckets = []float64{.05, .1, .25, .5, 1, 2.5, 5, 10, 30, 60, 120}
var toolCallCountBuckets = []float64{0, 1, 2, 3, 5, 8, 13, 21}

// Prometheus implements Recorder using Prometheus collectors.
type Prometheus struct {
	botID                       string
	llmRequests                 *prometheus.CounterVec
	llmDuration                 *prometheus.HistogramVec
	llmPromptTokens             *prometheus.CounterVec
	llmCompletionTokens         *prometheus.CounterVec
	llmTotalTokens              *prometheus.CounterVec
	mcpCalls                    *prometheus.CounterVec
	mcpDuration                 *prometheus.HistogramVec
	replyGenerate               *prometheus.HistogramVec
	messagePhase                *prometheus.HistogramVec
	inboundMessageHandle        *prometheus.HistogramVec
	chatMemoryQueueDepth        *prometheus.GaugeVec
	chatMemoryQueueWait         *prometheus.HistogramVec
	chatMemoryTaskDur           *prometheus.HistogramVec
	replyToolCalls              *prometheus.CounterVec
	replyToolCallDuration       *prometheus.HistogramVec
	inboundMessageToolCallCount *prometheus.HistogramVec
	serviceStarted              *prometheus.CounterVec
}

// NewPrometheus registers all metric collectors with reg, records one service_started
// event for the given version, and returns a Recorder.
func NewPrometheus(reg prometheus.Registerer, botID, version string) *Prometheus {
	botID = strings.TrimSpace(botID)
	if botID == "" {
		botID = "unknown"
	}

	p := &Prometheus{
		botID: botID,
		llmRequests: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Name:      "llm_requests_total",
				Help:      "LLM chat completion requests by task, model, method and outcome.",
			},
			[]string{"bot_id", "task", "model", "method", "outcome"},
		),
		llmDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: namespace,
				Name:      "llm_request_duration_seconds",
				Help:      "Latency of successful HTTP chat completion requests (no outcome label).",
				Buckets:   llmBuckets,
			},
			[]string{"bot_id", "task", "model", "method"},
		),
		llmPromptTokens: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Name:      "llm_prompt_tokens_total",
				Help:      "Prompt tokens reported by the LLM API, per task and model.",
			},
			[]string{"bot_id", "task", "model"},
		),
		llmCompletionTokens: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Name:      "llm_completion_tokens_total",
				Help:      "Completion tokens reported by the LLM API, per task and model.",
			},
			[]string{"bot_id", "task", "model"},
		),
		llmTotalTokens: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Name:      "llm_total_tokens_total",
				Help:      "Total tokens reported by the LLM API, per task and model.",
			},
			[]string{"bot_id", "task", "model"},
		),
		mcpCalls: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Name:      "mcp_tool_calls_total",
				Help:      "MCP tool invocations by server, tool, and outcome.",
			},
			[]string{"bot_id", "server", "tool", "outcome"},
		),
		mcpDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: namespace,
				Name:      "mcp_tool_duration_seconds",
				Help:      "Latency of MCP tool calls (success or failure).",
				Buckets:   llmBuckets,
			},
			[]string{"bot_id", "server", "tool"},
		),
		replyGenerate: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: namespace,
				Name:      "reply_generate_duration_seconds",
				Help:      "End-to-end reply generation duration.",
				Buckets:   llmBuckets,
			},
			[]string{"bot_id", "path"},
		),
		messagePhase: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: namespace,
				Name:      "message_handle_duration_seconds",
				Help:      "Duration of inbound message handling phases.",
				Buckets:   llmBuckets,
			},
			[]string{"bot_id", "phase"},
		),
		inboundMessageHandle: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: namespace,
				Name:      "inbound_message_handle_duration_seconds",
				Help:      "Wall-clock time to process one inbound message (UpsertChat through return).",
				Buckets:   llmBuckets,
			},
			[]string{"bot_id", "result"},
		),
		chatMemoryQueueDepth: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: namespace,
				Name:      "chat_memory_queue_depth",
				Help:      "Current depth of the per-chat memory task queue (sampled at enqueue time).",
			},
			[]string{"bot_id", "queue"},
		),
		chatMemoryQueueWait: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: namespace,
				Name:      "chat_memory_queue_wait_seconds",
				Help:      "Time a per-chat memory task waited in the FIFO queue before starting.",
				Buckets:   llmBuckets,
			},
			[]string{"bot_id", "queue"},
		),
		chatMemoryTaskDur: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: namespace,
				Name:      "chat_memory_task_duration_seconds",
				Help:      "Runtime of the per-chat memory task function.",
				Buckets:   llmBuckets,
			},
			[]string{"bot_id", "queue"},
		),
		replyToolCalls: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Name:      "reply_tool_calls_total",
				Help:      "Tool invocations during reply generation, by source (memory or mcp), tool name, and outcome.",
			},
			[]string{"bot_id", "source", "tool", "outcome"},
		),
		replyToolCallDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: namespace,
				Name:      "reply_tool_call_duration_seconds",
				Help:      "Latency of reply-path tool calls (memory and MCP).",
				Buckets:   llmBuckets,
			},
			[]string{"bot_id", "source", "tool"},
		),
		inboundMessageToolCallCount: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: namespace,
				Name:      "inbound_message_reply_tool_calls",
				Help:      "Number of reply-path tool calls per inbound message, by source (memory or mcp).",
				Buckets:   toolCallCountBuckets,
			},
			[]string{"bot_id", "source"},
		),
		serviceStarted: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Name:      "service_started",
				Help:      "Increments by 1 on each process start (use for Grafana deploy annotations).",
			},
			[]string{"bot_id", "version"},
		),
	}

	reg.MustRegister(
		p.llmRequests,
		p.llmDuration,
		p.llmPromptTokens,
		p.llmCompletionTokens,
		p.llmTotalTokens,
		p.mcpCalls,
		p.mcpDuration,
		p.replyGenerate,
		p.messagePhase,
		p.inboundMessageHandle,
		p.chatMemoryQueueDepth,
		p.chatMemoryQueueWait,
		p.chatMemoryTaskDur,
		p.replyToolCalls,
		p.replyToolCallDuration,
		p.inboundMessageToolCallCount,
		p.serviceStarted,
	)
	version = strings.TrimSpace(version)
	if version == "" {
		version = "unknown"
	}
	p.serviceStarted.WithLabelValues(botID, version).Inc()
	return p
}

// RecordChatCompletion implements Recorder.
func (p *Prometheus) RecordChatCompletion(task, model, method string, dur time.Duration, apiErr error, responseOutcome string, promptTokens, completionTokens, totalTokens int) {
	if apiErr != nil {
		out := classifyAPIOutcome(apiErr)
		p.llmRequests.WithLabelValues(p.botID, task, model, method, out).Inc()
		return
	}
	p.llmDuration.WithLabelValues(p.botID, task, model, method).Observe(dur.Seconds())
	if promptTokens > 0 {
		p.llmPromptTokens.WithLabelValues(p.botID, task, model).Add(float64(promptTokens))
	}
	if completionTokens > 0 {
		p.llmCompletionTokens.WithLabelValues(p.botID, task, model).Add(float64(completionTokens))
	}
	if totalTokens > 0 {
		p.llmTotalTokens.WithLabelValues(p.botID, task, model).Add(float64(totalTokens))
	}
	p.llmRequests.WithLabelValues(p.botID, task, model, method, responseOutcome).Inc()
}

// RecordLLMLogicalFailure implements Recorder.
func (p *Prometheus) RecordLLMLogicalFailure(task, model, method, outcome string) {
	p.llmRequests.WithLabelValues(p.botID, task, model, method, outcome).Inc()
}

// RecordMCPTool implements Recorder.
func (p *Prometheus) RecordMCPTool(server, tool, outcome string, dur time.Duration) {
	p.mcpCalls.WithLabelValues(p.botID, server, tool, outcome).Inc()
	p.mcpDuration.WithLabelValues(p.botID, server, tool).Observe(dur.Seconds())
}

// RecordReplyGenerate implements Recorder.
func (p *Prometheus) RecordReplyGenerate(path string, dur time.Duration) {
	p.replyGenerate.WithLabelValues(p.botID, path).Observe(dur.Seconds())
}

// RecordMessagePhase implements Recorder.
func (p *Prometheus) RecordMessagePhase(phase string, dur time.Duration) {
	p.messagePhase.WithLabelValues(p.botID, phase).Observe(dur.Seconds())
}

func (p *Prometheus) RecordInboundMessageHandle(result string, dur time.Duration) {
	p.inboundMessageHandle.WithLabelValues(p.botID, result).Observe(dur.Seconds())
}

func (p *Prometheus) RecordChatMemoryQueueDepth(queue string, depth int) {
	p.chatMemoryQueueDepth.WithLabelValues(p.botID, queue).Set(float64(depth))
}

func (p *Prometheus) RecordChatMemoryQueueWait(queue string, dur time.Duration) {
	p.chatMemoryQueueWait.WithLabelValues(p.botID, queue).Observe(dur.Seconds())
}

func (p *Prometheus) RecordChatMemoryTaskDuration(queue string, dur time.Duration) {
	p.chatMemoryTaskDur.WithLabelValues(p.botID, queue).Observe(dur.Seconds())
}

func (p *Prometheus) RecordReplyToolCall(source, tool, outcome string, dur time.Duration) {
	p.replyToolCalls.WithLabelValues(p.botID, source, tool, outcome).Inc()
	p.replyToolCallDuration.WithLabelValues(p.botID, source, tool).Observe(dur.Seconds())
}

func (p *Prometheus) RecordInboundMessageToolCallCount(source string, count int) {
	p.inboundMessageToolCallCount.WithLabelValues(p.botID, source).Observe(float64(count))
}

func classifyAPIOutcome(err error) string {
	if err == nil {
		return "success"
	}
	if errors.Is(err, context.Canceled) {
		return "cancelled"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	var reqErr *openai.RequestError
	if errors.As(err, &reqErr) {
		if reqErr.HTTPStatusCode == 408 || reqErr.HTTPStatusCode == 499 {
			return "timeout"
		}
	}
	var apiErr *openai.APIError
	if errors.As(err, &apiErr) && apiErr.HTTPStatusCode == 408 {
		return "timeout"
	}
	return "api_error"
}
