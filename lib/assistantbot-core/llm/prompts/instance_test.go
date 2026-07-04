package prompts

import (
	"strings"
	"testing"
)

func TestFormatInstanceContext(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		ctx  InstanceContext
		want string
	}{
		{
			name: "names and version",
			ctx:  InstanceContext{BotNames: []string{"bot", "assistant"}, Version: "2026.3.1"},
			want: "Instance context (for your reasoning; do not recite unless the user asks):\n- You may be addressed by these names (case-insensitive): bot, assistant\n- Your current version: 2026.3.1",
		},
		{
			// Unicode bot name: non-ASCII must survive trim/join unchanged.
			name: "names only",
			ctx:  InstanceContext{BotNames: []string{"чатик"}},
			want: "Instance context (for your reasoning; do not recite unless the user asks):\n- You may be addressed by these names (case-insensitive): чатик",
		},
		{
			name: "dedupe and lowercase",
			ctx:  InstanceContext{BotNames: []string{"Bot", "bot", " Assistant "}, Version: "1.0"},
			want: "Instance context (for your reasoning; do not recite unless the user asks):\n- You may be addressed by these names (case-insensitive): bot, assistant\n- Your current version: 1.0",
		},
		{
			name: "version only",
			ctx:  InstanceContext{Version: "dev"},
			want: "Instance context (for your reasoning; do not recite unless the user asks):\n- Your current version: dev",
		},
		{
			name: "empty",
			ctx:  InstanceContext{},
			want: "",
		},
		{
			name: "skips blank names",
			ctx:  InstanceContext{BotNames: []string{"", " bot ", "assistant"}, Version: " 1.0 "},
			want: "Instance context (for your reasoning; do not recite unless the user asks):\n- You may be addressed by these names (case-insensitive): bot, assistant\n- Your current version: 1.0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := FormatInstanceContext(tt.ctx); got != tt.want {
				t.Fatalf("FormatInstanceContext() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestInstanceContextIsEmpty(t *testing.T) {
	t.Parallel()
	if !(InstanceContext{}).IsEmpty() {
		t.Fatal("expected empty context")
	}
	if (InstanceContext{BotNames: []string{"bot"}}).IsEmpty() {
		t.Fatal("expected non-empty context")
	}
}

func TestSystemPromptWithInstanceContext(t *testing.T) {
	reg, err := ParseYAML([]byte(`default: memory assistant
generate_chat_reply: chat participant
`))
	if err != nil {
		t.Fatal(err)
	}
	reg.SetInstanceContext(InstanceContext{BotNames: []string{"bot"}, Version: "2026.3.1"})

	got := reg.SystemPrompt(TaskUpdateTopic)
	if !strings.HasPrefix(got, "memory assistant\n") {
		t.Fatalf("base prompt missing: %q", got)
	}
	if !strings.Contains(got, "Your current version: 2026.3.1") {
		t.Fatalf("instance block missing: %q", got)
	}

	gotReply := reg.SystemPrompt(TaskGenerateChatReply)
	if !strings.HasPrefix(gotReply, "chat participant\n") {
		t.Fatalf("task override missing: %q", gotReply)
	}
}

func TestSystemPromptForMCPWithInstanceContext(t *testing.T) {
	reg, err := ParseYAML([]byte(`default: fallback
generate_chat_reply: reply base
`))
	if err != nil {
		t.Fatal(err)
	}
	reg.SetInstanceContext(InstanceContext{BotNames: []string{"bot"}, Version: "dev"})

	got := reg.SystemPromptForMCP("mcp extra")
	if !strings.HasPrefix(got, "reply base\n") {
		t.Fatalf("base missing: %q", got)
	}
	instanceIdx := strings.Index(got, "Instance context")
	mcpIdx := strings.Index(got, "mcp extra")
	if instanceIdx < 0 || mcpIdx < 0 || instanceIdx > mcpIdx {
		t.Fatalf("expected order base -> instance -> mcp, got %q", got)
	}
}

func TestSetInstanceContextNilRegistry(t *testing.T) {
	var reg *Registry
	reg.SetInstanceContext(InstanceContext{Version: "dev"})
}
