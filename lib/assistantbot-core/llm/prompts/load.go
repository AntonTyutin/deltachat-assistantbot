package prompts

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// Registry holds system prompts loaded from YAML.
type Registry struct {
	defaultPrompt  string
	byTask         map[string]string
	instanceAppend string
}

// Load reads and validates a prompts YAML file.
func Load(path string) (*Registry, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, fmt.Errorf("prompts file path is empty")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read prompts file %q: %w", path, err)
	}
	return parseYAML(trimLeadingBOM(data))
}

// ParseYAML builds a registry from YAML bytes (tests and Load).
func ParseYAML(data []byte) (*Registry, error) {
	return parseYAML(trimLeadingBOM(data))
}

func parseYAML(data []byte) (*Registry, error) {
	if strings.TrimSpace(string(data)) == "" {
		return nil, fmt.Errorf("prompts file is empty")
	}
	var raw map[string]string
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse prompts YAML: %w", err)
	}
	return newRegistry(raw)
}

func newRegistry(raw map[string]string) (*Registry, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("prompts file has no keys")
	}
	defaultPrompt, ok := raw[KeyDefault]
	if !ok {
		return nil, fmt.Errorf("prompts file missing required key %q", KeyDefault)
	}
	defaultPrompt, err := validatePromptText(KeyDefault, defaultPrompt)
	if err != nil {
		return nil, err
	}

	byTask := make(map[string]string, len(raw))
	for key, text := range raw {
		if key == KeyDefault {
			continue
		}
		if !IsYAMLTaskKey(key) {
			return nil, fmt.Errorf("prompts file: unknown key %q (expected %q or a prompts task id)", key, KeyDefault)
		}
		text, err := validatePromptText(key, text)
		if err != nil {
			return nil, err
		}
		byTask[key] = text
	}

	return &Registry{
		defaultPrompt: defaultPrompt,
		byTask:        byTask,
	}, nil
}

func validatePromptText(key, text string) (string, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", fmt.Errorf("prompts file: key %q must be non-empty", key)
	}
	if strings.Contains(text, "\x00") {
		return "", fmt.Errorf("prompts file: key %q contains NUL byte", key)
	}
	return text, nil
}

// SetInstanceContext configures deployment-level facts appended to every system prompt.
func (r *Registry) SetInstanceContext(c InstanceContext) {
	if r == nil {
		return
	}
	r.instanceAppend = FormatInstanceContext(c)
}

func (r *Registry) appendInstance(base string) string {
	if r == nil || r.instanceAppend == "" {
		return base
	}
	return base + "\n" + r.instanceAppend
}

func (r *Registry) baseSystemPrompt(task string) string {
	task = strings.TrimSpace(task)
	if text, ok := r.byTask[task]; ok {
		return text
	}
	return r.defaultPrompt
}

// SystemPrompt returns the system prompt for task, falling back to default.
func (r *Registry) SystemPrompt(task string) string {
	if r == nil {
		return ""
	}
	return r.appendInstance(r.baseSystemPrompt(task))
}

// SystemPromptForMCP is the system message for ChatWithTools: generate_chat_reply (or default) plus MCP append.
func (r *Registry) SystemPromptForMCP(mcpAppend string) string {
	if r == nil {
		return ""
	}
	prompt := r.appendInstance(r.baseSystemPrompt(TaskGenerateChatReply))
	if extra := strings.TrimSpace(mcpAppend); extra != "" {
		prompt += "\n" + extra
	}
	return prompt
}

func trimLeadingBOM(b []byte) []byte {
	if len(b) >= 3 && b[0] == 0xEF && b[1] == 0xBB && b[2] == 0xBF {
		return b[3:]
	}
	return b
}
