package reply

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

var (
	ErrEmptyModelReply       = errors.New("empty model reply")
	ErrUnparseableModelReply = errors.New("unparseable model reply")
)

var replyFieldPartialRE = regexp.MustCompile(`(?is)"reply"\s*:\s*"((?:[^"\\]|\\.)*)"?`)

// parseModelReply extracts user-visible text from a model response. Valid JSON with a
// non-empty reply field is preferred; truncated JSON may still yield a reply prefix.
// Plain text is accepted when the response does not look like a reply JSON object.
func parseModelReply(text string) (string, error) {
	text = stripJSONFence(strings.TrimSpace(text))
	if text == "" {
		return "", ErrEmptyModelReply
	}

	var response struct {
		Reply string `json:"reply"`
	}
	if err := json.Unmarshal([]byte(text), &response); err == nil {
		reply := strings.TrimSpace(response.Reply)
		if reply == "" {
			return "", ErrEmptyModelReply
		}
		return reply, nil
	}

	if reply, ok := extractReplyFieldPartial(text); ok {
		reply = strings.TrimSpace(reply)
		if reply != "" {
			return reply, nil
		}
	}

	if !looksLikeJSONReply(text) {
		return text, nil
	}

	return "", fmt.Errorf("%w: %s", ErrUnparseableModelReply, previewForLog(text))
}

func extractReplyFieldPartial(s string) (string, bool) {
	m := replyFieldPartialRE.FindStringSubmatch(s)
	if len(m) < 2 {
		return "", false
	}
	return unescapeJSONString(m[1]), true
}

func looksLikeJSONReply(s string) bool {
	s = strings.TrimSpace(s)
	return strings.HasPrefix(s, "{") && strings.Contains(strings.ToLower(s), `"reply"`)
}

func unescapeJSONString(s string) string {
	var decoded string
	if err := json.Unmarshal([]byte(`"`+s+`"`), &decoded); err == nil {
		return decoded
	}
	return s
}

func previewForLog(s string) string {
	const max = 160
	s = strings.TrimSpace(s)
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
