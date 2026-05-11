package reply

import (
	"strings"
	"unicode"

	"assistantbot/internal/deltachat"
)

type Intent string

const (
	IntentNone       Intent = "none"
	IntentAddressed  Intent = "addressed"
	IntentBotReply   Intent = "bot_reply"
	IntentSummary    Intent = "summary"
	IntentGroupAsk   Intent = "group_question"
	IntentCorrection Intent = "correction"
)

type Classification struct {
	Intent   Intent
	NeedsLLM bool
	Reason   string
}

type Classifier struct {
	botNames []string
}

func NewClassifier(botNames []string) *Classifier {
	names := make([]string, 0, len(botNames))
	for _, name := range botNames {
		name = strings.TrimSpace(strings.ToLower(name))
		if name != "" {
			names = append(names, name)
		}
	}
	return &Classifier{botNames: names}
}

func (c *Classifier) Classify(message deltachat.Message, repliedToBot bool) Classification {
	if message.IsFromSelf || strings.TrimSpace(message.Text) == "" {
		return Classification{Intent: IntentNone}
	}
	if !message.IsGroup {
		return Classification{Intent: IntentAddressed, NeedsLLM: true, Reason: "private chat"}
	}
	text := strings.ToLower(message.Text)
	if repliedToBot {
		return Classification{Intent: IntentBotReply, NeedsLLM: true, Reason: "reply to bot"}
	}
	for _, name := range c.botNames {
		if containsWord(text, name) {
			return Classification{Intent: IntentAddressed, NeedsLLM: true, Reason: "bot name mentioned"}
		}
	}
	if containsAny(text, "summary", "summarize", "recap", "tl;dr", "саммари", "кратко", "что решили") {
		return Classification{Intent: IntentSummary, NeedsLLM: true, Reason: "summary request"}
	}
	if looksLikeCorrection(text) {
		return Classification{Intent: IntentCorrection, NeedsLLM: true, Reason: "memory correction"}
	}
	trimmed := strings.TrimSpace(text)
	if strings.Contains(text, "?") || hasAnyPrefix(trimmed, "who ", "how ", "what ", "why ", "where ", "кто ", "как ", "что ", "почему ", "где ") {
		return Classification{Intent: IntentGroupAsk, NeedsLLM: true, Reason: "question to group"}
	}
	return Classification{Intent: IntentNone}
}

func looksLikeCorrection(text string) bool {
	markers := []string{
		"bot is wrong",
		"you are wrong",
		"that's wrong",
		"not like that",
		"we decided",
		"remember it differently",
		"бот ошибся",
		"ты ошибся",
		"не разбираюсь",
		"не так",
		"мы решили",
		"мы пришли к тому",
		"запомни иначе",
	}
	for _, marker := range markers {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

func containsAny(text string, markers ...string) bool {
	for _, marker := range markers {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

func hasAnyPrefix(text string, prefixes ...string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(text, prefix) {
			return true
		}
	}
	return false
}

func containsWord(text, word string) bool {
	if word == "" {
		return false
	}
	start := 0
	for start < len(text) {
		idx := strings.Index(text[start:], word)
		if idx < 0 {
			return false
		}
		idx += start
		beforeOK := idx == 0 || !isWordRune(rune(text[idx-1]))
		after := idx + len(word)
		afterOK := after >= len(text) || !isWordRune(rune(text[after]))
		if beforeOK && afterOK {
			return true
		}
		start = after
	}
	return false
}

func isWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_'
}
