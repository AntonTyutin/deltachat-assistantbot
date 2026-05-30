package llm

import (
	"testing"
	"time"
)

func TestRetryPolicyForTask(t *testing.T) {
	if max, base := retryPolicyForTask(TaskGenerateChatReply); max != replyMaxAttempts || base != replyRetryBaseDelay {
		t.Fatalf("reply policy: max=%d base=%v", max, base)
	}
	if max, base := retryPolicyForTask("update_chat_topic"); max != backgroundMaxAttempts || base != backgroundRetryBaseDelay {
		t.Fatalf("background policy: max=%d base=%v", max, base)
	}
}

func TestRetryBackoffDelay(t *testing.T) {
	base := 400 * time.Millisecond
	multiplier := 2.0

	if got := retryBackoffDelay(base, multiplier, 1); got != 0 {
		t.Fatalf("attempt 1: expected no delay, got %v", got)
	}
	if got := retryBackoffDelay(base, multiplier, 2); got != 400*time.Millisecond {
		t.Fatalf("attempt 2: got %v", got)
	}
	if got := retryBackoffDelay(base, multiplier, 3); got != 800*time.Millisecond {
		t.Fatalf("attempt 3: got %v", got)
	}

	bgBase := time.Second
	if got := retryBackoffDelay(bgBase, multiplier, 2); got != time.Second {
		t.Fatalf("background attempt 2: got %v", got)
	}
	if got := retryBackoffDelay(bgBase, multiplier, 3); got != 2*time.Second {
		t.Fatalf("background attempt 3: got %v", got)
	}
}

func TestPickRandomModel(t *testing.T) {
	if got := pickRandomModel(nil); got != "" {
		t.Fatalf("empty pool: got %q", got)
	}
	if got := pickRandomModel([]string{"only"}); got != "only" {
		t.Fatalf("single model: got %q", got)
	}

	models := []string{"a", "b", "c"}
	seen := map[string]struct{}{}
	for i := 0; i < 50; i++ {
		seen[pickRandomModel(models)] = struct{}{}
	}
	if len(seen) < 2 {
		t.Fatalf("expected random spread across pool, saw %d distinct models", len(seen))
	}
}
