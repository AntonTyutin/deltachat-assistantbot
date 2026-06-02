package memory

import (
	"strings"

	"github.com/AntonTyutin/assistantbot-core/storage"
)

// CondenseListItems formats list items as compact newline-separated text for LLM context.
// Done items are suffixed with " [done]".
func CondenseListItems(items []storage.ListItem) string {
	if len(items) == 0 {
		return ""
	}
	lines := make([]string, 0, len(items))
	for _, item := range items {
		line := strings.TrimSpace(item.Text)
		if line == "" {
			continue
		}
		if item.Done {
			line += " [done]"
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}
