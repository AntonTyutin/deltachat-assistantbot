package config

import (
	"os"
	"testing"
)

func TestLLMTaskMaxCompletionTokensFromEnv(t *testing.T) {
	t.Setenv("LLM_MAX_COMPLETION_TOKENS_REPLY", "500")
	t.Setenv("LLM_MAX_COMPLETION_TOKENS_SUMMARY", "1200")
	t.Setenv("LLM_MAX_COMPLETION_TOKENS_TOPIC", "900")
	t.Setenv("LLM_MAX_COMPLETION_TOKENS_PROFILE_UPDATE", "700")
	t.Setenv("LLM_MAX_COMPLETION_TOKENS_PROFILE_REBUILD", "800")
	t.Setenv("LLM_MAX_COMPLETION_TOKENS_TOPIC_REBUILD", "bad")
	got := llmTaskMaxCompletionTokens()
	if got["generate_chat_reply"] != 500 {
		t.Fatalf("reply tokens: got %d", got["generate_chat_reply"])
	}
	if got["daily_summary"] != 1200 {
		t.Fatalf("summary tokens: got %d", got["daily_summary"])
	}
	if got["update_chat_topic"] != 900 {
		t.Fatalf("topic update tokens: got %d", got["update_chat_topic"])
	}
	if got["rebuild_chat_topic"] != 900 {
		t.Fatalf("topic rebuild tokens: got %d", got["rebuild_chat_topic"])
	}
	if got["update_participant_profile"] != 700 {
		t.Fatalf("profile update tokens: got %d", got["update_participant_profile"])
	}
	if got["rebuild_participant_profile"] != 800 {
		t.Fatalf("profile rebuild tokens: got %d", got["rebuild_participant_profile"])
	}
}

func TestLLMMaxCompletionTokensFallback(t *testing.T) {
	orig := os.Getenv("LLM_MAX_COMPLETION_TOKENS")
	defer os.Setenv("LLM_MAX_COMPLETION_TOKENS", orig)
	t.Setenv("LLM_MAX_COMPLETION_TOKENS", "-1")
	if got := llmMaxCompletionTokens(); got != 2048 {
		t.Fatalf("expected fallback 2048, got %d", got)
	}
}
