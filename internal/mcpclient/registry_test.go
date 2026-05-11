package mcpclient

import "testing"

func TestSystemPromptAppend(t *testing.T) {
	t.Parallel()
	r := &Registry{
		systemPromptAppend: []string{
			"Use source A.",
			"Use source B.",
		},
	}
	got := r.SystemPromptAppend()
	want := "Use source A.\nUse source B."
	if got != want {
		t.Fatalf("unexpected append text: got %q want %q", got, want)
	}
}
