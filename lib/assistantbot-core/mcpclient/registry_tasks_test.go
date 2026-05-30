package mcpclient

import (
	"testing"

	openai "github.com/sashabaranov/go-openai"

	"github.com/AntonTyutin/assistantbot-core/llm"
)

func TestToolsForTaskOpenRouterOnly(t *testing.T) {
	t.Parallel()
	reg := &Registry{
		toolEntries: []toolEntry{
			{
				def:   llm.ToolDefinition{Type: "openrouter:web_search"},
				tasks: []string{llm.TaskGenerateChatReply},
			},
			{
				def: llm.ToolDefinition{
					Type: string(openai.ToolTypeFunction),
					Function: &openai.FunctionDefinition{
						Name:       "geo__resolve",
						Parameters: map[string]any{"type": "object"},
					},
				},
				tasks: []string{llm.TaskChatWithTools},
			},
		},
	}
	replyTools := reg.ToolsForTask(llm.TaskGenerateChatReply)
	if len(replyTools) != 1 || replyTools[0].Type != "openrouter:web_search" {
		t.Fatalf("reply tools: %+v", replyTools)
	}
	locTools := reg.ToolsForTask(llm.TaskChatWithTools)
	if len(locTools) != 1 || locTools[0].Function == nil || locTools[0].Function.Name != "geo__resolve" {
		t.Fatalf("location tools: %+v", locTools)
	}
	if !reg.HasToolsForTask(llm.TaskGenerateChatReply) {
		t.Fatal("expected reply tools")
	}
	if reg.HasToolsForTask("unknown_task") {
		t.Fatal("unexpected tools for unknown task")
	}
}
