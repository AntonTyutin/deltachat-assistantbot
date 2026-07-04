package prompts

import (
	"strings"
)

// InstanceContext holds deployment-level facts appended to every LLM system prompt.
type InstanceContext struct {
	BotNames []string
	Version  string
}

func (c InstanceContext) IsEmpty() bool {
	if strings.TrimSpace(c.Version) != "" {
		return false
	}
	for _, name := range c.BotNames {
		if strings.TrimSpace(name) != "" {
			return false
		}
	}
	return true
}

func normalizeBotNames(names []string) []string {
	seen := make(map[string]struct{}, len(names))
	out := make([]string, 0, len(names))
	for _, name := range names {
		name = strings.ToLower(strings.TrimSpace(name))
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	return out
}

// FormatInstanceContext renders instance facts for the system prompt.
func FormatInstanceContext(c InstanceContext) string {
	version := strings.TrimSpace(c.Version)
	names := normalizeBotNames(c.BotNames)
	if len(names) == 0 && version == "" {
		return ""
	}

	var b strings.Builder
	b.WriteString("Instance context (for your reasoning; do not recite unless the user asks):")
	if len(names) > 0 {
		b.WriteString("\n- You may be addressed by these names (case-insensitive): ")
		b.WriteString(strings.Join(names, ", "))
	}
	if version != "" {
		b.WriteString("\n- Your current version: ")
		b.WriteString(version)
	}
	return b.String()
}
