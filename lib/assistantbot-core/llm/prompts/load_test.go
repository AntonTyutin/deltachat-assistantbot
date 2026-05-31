package prompts

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseYAMLMultilineAndTaskOverride(t *testing.T) {
	reg, err := ParseYAML([]byte(`default: |
  memory assistant
generate_chat_reply: |
  chat participant
`))
	if err != nil {
		t.Fatal(err)
	}
	if got := reg.SystemPrompt(TaskUpdateTopic); got != "memory assistant" {
		t.Fatalf("fallback: got %q", got)
	}
	if got := reg.SystemPrompt(TaskGenerateChatReply); got != "chat participant" {
		t.Fatalf("task override: got %q", got)
	}
}

func TestParseYAMLMissingDefault(t *testing.T) {
	_, err := ParseYAML([]byte(`generate_chat_reply: hi`))
	if err == nil {
		t.Fatal("expected error for missing default")
	}
}

func TestSystemPromptForMCP(t *testing.T) {
	reg, err := ParseYAML([]byte(`default: fallback
generate_chat_reply: reply base
`))
	if err != nil {
		t.Fatal(err)
	}
	got := reg.SystemPromptForMCP("mcp extra")
	want := "reply base\nmcp extra"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	got = reg.SystemPromptForMCP("")
	if got != "reply base" {
		t.Fatalf("without append: got %q", got)
	}
	reg2, err := ParseYAML([]byte("default: only default\n"))
	if err != nil {
		t.Fatal(err)
	}
	if reg2.SystemPromptForMCP("x") != "only default\nx" {
		t.Fatalf("fallback to default: got %q", reg2.SystemPromptForMCP("x"))
	}
}

func TestParseYAMLRejectsMCPTaskKey(t *testing.T) {
	_, err := ParseYAML([]byte(`default: ok
chat_with_tools: extra
`))
	if err == nil {
		t.Fatal("expected error for task id not allowed in prompts file")
	}
}

func TestParseYAMLUnknownKey(t *testing.T) {
	_, err := ParseYAML([]byte(`default: ok
unknown_task: nope
`))
	if err == nil {
		t.Fatal("expected error for unknown key")
	}
}

func TestLoadFromFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "prompts.yaml")
	if err := os.WriteFile(path, []byte("default: from file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	reg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if reg.SystemPrompt(TaskClassifyMessageTopic) != "from file" {
		t.Fatalf("got %q", reg.SystemPrompt(TaskClassifyMessageTopic))
	}
}
