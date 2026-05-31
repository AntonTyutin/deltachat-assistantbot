package config

import (
	"os"
	"testing"
)

func TestLLMTaskMaxCompletionTokensFromEnv(t *testing.T) {
	t.Setenv("LLM_MAX_COMPLETION_TOKENS_REPLY", "500")
	t.Setenv("LLM_MAX_COMPLETION_TOKENS_TOPIC", "900")
	t.Setenv("LLM_MAX_COMPLETION_TOKENS_PROFILE", "700")
	got := llmTaskMaxCompletionTokens()
	if got["generate_chat_reply"] != 500 {
		t.Fatalf("reply tokens: got %d", got["generate_chat_reply"])
	}
	if got["classify_message_topic"] != 900 {
		t.Fatalf("classify tokens: got %d", got["classify_message_topic"])
	}
	if got["update_chat_topic"] != 900 {
		t.Fatalf("topic update tokens: got %d", got["update_chat_topic"])
	}
	if got["update_participant_profile"] != 700 {
		t.Fatalf("profile update tokens: got %d", got["update_participant_profile"])
	}
}

func TestLLMRetryBackoffMultiplierFromEnv(t *testing.T) {
	t.Setenv("LLM_RETRY_BACKOFF_MULTIPLIER", "3.5")
	if got := llmRetryBackoffMultiplier(); got != 3.5 {
		t.Fatalf("expected 3.5, got %v", got)
	}
	t.Setenv("LLM_RETRY_BACKOFF_MULTIPLIER", "0.5")
	if got := llmRetryBackoffMultiplier(); got != 2 {
		t.Fatalf("expected fallback 2 for invalid value, got %v", got)
	}
}

func TestAppDebugFromEnv(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  bool
	}{
		{"", false},
		{"false", false},
		{"0", false},
		{"true", true},
		{"TRUE", true},
		{"1", true},
		{"yes", true},
		{"on", true},
	} {
		t.Run(tc.value, func(t *testing.T) {
			if tc.value == "" {
				t.Setenv("APP_DEBUG", "")
			} else {
				t.Setenv("APP_DEBUG", tc.value)
			}
			if got := AppDebug(); got != tc.want {
				t.Fatalf("APP_DEBUG=%q: got %v, want %v", tc.value, got, tc.want)
			}
		})
	}
}

func TestValidateRunRequiresLLMPromptsFile(t *testing.T) {
	cfg := Config{
		DBEncryptionKey: "secret",
		LLMAPIKey:       "key",
	}
	if err := cfg.ValidateRun(); err == nil {
		t.Fatal("expected error when ASSISTANT_BOT_LLM_PROMPTS_FILE is missing")
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
