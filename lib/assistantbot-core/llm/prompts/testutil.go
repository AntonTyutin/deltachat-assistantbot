package prompts

import (
	"testing"
)

// FixedTestRegistry returns a minimal registry for tests that cannot take *testing.T.
func FixedTestRegistry() *Registry {
	reg, err := ParseYAML([]byte(`default: test default prompt
generate_chat_reply: test reply prompt
`))
	if err != nil {
		panic(err)
	}
	return reg
}

// MustTestRegistry returns a minimal registry for unit tests.
func MustTestRegistry(t *testing.T) *Registry {
	t.Helper()
	r, err := ParseYAML([]byte(`default: test default prompt
generate_chat_reply: test reply prompt
`))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// MustTestRegistryWithTasks builds a registry with the given default and optional task overrides.
func MustTestRegistryWithTasks(t *testing.T, defaultPrompt string, tasks map[string]string) *Registry {
	t.Helper()
	raw := map[string]string{KeyDefault: defaultPrompt}
	for task, text := range tasks {
		if !IsYAMLTaskKey(task) {
			t.Fatalf("unknown prompts yaml task key %q", task)
		}
		raw[task] = text
	}
	r, err := newRegistry(raw)
	if err != nil {
		t.Fatal(err)
	}
	return r
}
