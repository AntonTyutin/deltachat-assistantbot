package llm

import (
	"testing"
	"time"
)

func TestModelsForTaskUsesTaskOverride(t *testing.T) {
	client := NewOpenRouterClient("https://example.com/api", "key", "default-model", map[string]string{
		"generate_chat_reply": "reply-model, reply-model-2",
	}, nil, time.Second, 1024, nil)

	gotReply := client.modelsForTask("generate_chat_reply")
	if len(gotReply) != 2 || gotReply[0] != "reply-model" || gotReply[1] != "reply-model-2" {
		t.Fatalf("expected override models, got %#v", gotReply)
	}
	gotClassify := client.modelsForTask("classify_message_topic")
	if len(gotClassify) != 1 || gotClassify[0] != "default-model" {
		t.Fatalf("expected default-model fallback, got %#v", gotClassify)
	}
}

func TestModelsForTaskIgnoresBlankOverrides(t *testing.T) {
	client := NewOpenRouterClient("https://example.com/api", "key", "default-model", map[string]string{
		"classify_message_topic": "   ",
	}, nil, time.Second, 1024, nil)

	got := client.modelsForTask("classify_message_topic")
	if len(got) != 1 || got[0] != "default-model" {
		t.Fatalf("expected default-model fallback, got %#v", got)
	}
}

func TestParseModelListSupportsCommaAndSpaceSeparators(t *testing.T) {
	got := parseModelList("a,b c,\n d\tb")
	want := []string{"a", "b", "c", "d"}
	if len(got) != len(want) {
		t.Fatalf("expected %d models, got %d: %#v", len(want), len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("expected %q at position %d, got %q", want[i], i, got[i])
		}
	}
}

func TestMaxCompletionTokensForTaskUsesOverride(t *testing.T) {
	client := NewOpenRouterClient(
		"https://example.com/api",
		"key",
		"default-model",
		nil,
		map[string]int{"generate_chat_reply": 700},
		time.Second,
		2048,
		nil,
	)
	if got := client.maxCompletionTokensForTask("generate_chat_reply"); got != 700 {
		t.Fatalf("expected task override 700, got %d", got)
	}
	if got := client.maxCompletionTokensForTask("classify_message_topic"); got != 2048 {
		t.Fatalf("expected fallback 2048, got %d", got)
	}
}
