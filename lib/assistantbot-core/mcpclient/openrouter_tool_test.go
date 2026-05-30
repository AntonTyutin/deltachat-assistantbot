package mcpclient

import "testing"

func TestNormalizeOpenRouterTool(t *testing.T) {
	t.Parallel()
	full, err := normalizeOpenRouterTool("web_search")
	if err != nil || full != "openrouter:web_search" {
		t.Fatalf("alias: %q err=%v", full, err)
	}
	full, err = normalizeOpenRouterTool("openrouter:datetime")
	if err != nil || full != "openrouter:datetime" {
		t.Fatalf("full: %q err=%v", full, err)
	}
	if _, err := normalizeOpenRouterTool("nope"); err == nil {
		t.Fatal("expected error for unknown tool")
	}
}
