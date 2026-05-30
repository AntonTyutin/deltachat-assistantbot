package mcpclient

import (
	"testing"

	"github.com/AntonTyutin/assistantbot-core/llm"
)

func TestSystemPromptAppendForTask(t *testing.T) {
	t.Parallel()
	r := &Registry{
		promptAppends: []promptAppend{
			{text: "Use source B.", tasks: []string{llm.TaskGenerateChatReply}},
			{text: "Use source A.", tasks: []string{llm.TaskGenerateChatReply}},
			{text: "Geocode hint.", tasks: []string{llm.TaskChatWithTools}},
		},
	}
	got := r.SystemPromptAppendForTask(llm.TaskGenerateChatReply)
	want := "Use source A.\nUse source B."
	if got != want {
		t.Fatalf("unexpected append text: got %q want %q", got, want)
	}
	if got := r.SystemPromptAppendForTask(llm.TaskChatWithTools); got != "Geocode hint." {
		t.Fatalf("unexpected chat_with_tools append: %q", got)
	}
}
