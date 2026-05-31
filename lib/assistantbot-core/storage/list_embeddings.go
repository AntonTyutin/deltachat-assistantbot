package storage

import "strings"

func ListEmbeddingText(kind ListKind, title string, items []ListItem) string {
	var b strings.Builder
	b.WriteString(string(kind))
	b.WriteString(": ")
	b.WriteString(strings.TrimSpace(title))
	for _, item := range items {
		text := strings.TrimSpace(item.Text)
		if text == "" {
			continue
		}
		b.WriteByte('\n')
		if item.Done {
			b.WriteString("[done] ")
		}
		b.WriteString(text)
	}
	return b.String()
}
