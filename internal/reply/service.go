package reply

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	openai "github.com/sashabaranov/go-openai"

	"assistantbot/internal/deltachat"
	"assistantbot/internal/llm"
	"assistantbot/internal/mcpclient"
	"assistantbot/internal/storage"
)

const taskGenerateReply = "generate_chat_reply"

type Service struct {
	store      *storage.Store
	llm        llm.Client
	classifier *Classifier
	mcp        *mcpclient.Registry
}

func NewService(store *storage.Store, llmClient llm.Client, botNames []string, mcp *mcpclient.Registry) *Service {
	return &Service{
		store:      store,
		llm:        llmClient,
		classifier: NewClassifier(botNames),
		mcp:        mcp,
	}
}

func (s *Service) Decide(ctx context.Context, message deltachat.Message, topic storage.Topic) (*deltachat.OutboundMessage, Classification, error) {
	repliedToBot := false
	if message.ReplyToID != "" {
		recent, err := s.store.RecentMessages(ctx, message.ChatID, 20)
		if err != nil {
			return nil, Classification{}, err
		}
		for _, recentMessage := range recent {
			if recentMessage.ID == message.ReplyToID && recentMessage.IsFromSelf {
				repliedToBot = true
				break
			}
		}
	}

	classification := s.classifier.Classify(message, repliedToBot)
	if classification.Intent == IntentNone {
		return nil, classification, nil
	}
	reply, err := s.generate(ctx, message, topic, classification)
	if err != nil {
		return nil, classification, err
	}
	reply = sanitizeDirectAddress(reply, message)
	if strings.TrimSpace(reply) == "" {
		return nil, classification, nil
	}
	replyToID := ""
	if message.IsGroup {
		replyToID = message.ID
	}
	return &deltachat.OutboundMessage{
		ChatID:    message.ChatID,
		Text:      reply,
		ReplyToID: replyToID,
	}, classification, nil
}

func (s *Service) generate(ctx context.Context, message deltachat.Message, topic storage.Topic, classification Classification) (string, error) {
	recent, err := s.store.RecentMessages(ctx, message.ChatID, 20)
	if err != nil {
		return "", err
	}
	profile, _, err := s.store.GetProfile(ctx, message.SenderID)
	if err != nil {
		return "", err
	}
	payload := map[string]any{
		"intent":  classification,
		"message": message,
		"profile": profile,
		"topic":   topic,
		"recent":  recent,
	}

	if s.mcp != nil && len(s.mcp.OpenAITools()) > 0 {
		text, err := s.generateWithMCP(ctx, payload)
		if err != nil {
			if classification.Intent == IntentSummary {
				return fallbackSummary(topic), nil
			}
			return "", err
		}
		return strings.TrimSpace(extractReplyFromModel(text)), nil
	}

	raw, err := s.llm.CompleteJSON(ctx, taskGenerateReply, payload, `{"reply":"short chat message in the language of the conversation"}`)
	if err != nil {
		if classification.Intent == IntentSummary {
			return fallbackSummary(topic), nil
		}
		return "", err
	}
	var response struct {
		Reply string `json:"reply"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return "", err
	}
	return strings.TrimSpace(response.Reply), nil
}

const mcpSystemPrompt = `You are a helpful participant in a group chat. You may call tools to fetch live information when useful.
When you have finished using tools, reply with a single JSON object only, no markdown fences: {"reply":"your short message"} using the same language as the conversation (use Russian when the chat is in Russian).`

func (s *Service) generateWithMCP(ctx context.Context, payload map[string]any) (string, error) {
	inputJSON, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	systemPrompt := mcpSystemPrompt
	if extra := strings.TrimSpace(s.mcp.SystemPromptAppend()); extra != "" {
		systemPrompt += "\n" + extra
	}
	messages := []openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleSystem, Content: systemPrompt},
		{Role: openai.ChatMessageRoleUser, Content: string(inputJSON)},
	}
	return s.llm.ChatWithTools(ctx, taskGenerateReply, messages, s.mcp.OpenAITools(), s.mcp.ExecuteTool)
}

func extractReplyFromModel(text string) string {
	text = strings.TrimSpace(text)
	text = stripJSONFence(text)
	var response struct {
		Reply string `json:"reply"`
	}
	if err := json.Unmarshal([]byte(text), &response); err == nil && strings.TrimSpace(response.Reply) != "" {
		return strings.TrimSpace(response.Reply)
	}
	return text
}

func stripJSONFence(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimSuffix(s, "```")
	return strings.TrimSpace(s)
}

func fallbackSummary(topic storage.Topic) string {
	if topic.Summary == "" {
		return "Not enough data for a summary yet."
	}
	return fmt.Sprintf("Summary: %s", topic.Summary)
}

func sanitizeDirectAddress(reply string, message deltachat.Message) string {
	if message.IsGroup && message.ReplyToID == "" {
		return strings.TrimSpace(reply)
	}
	trimmed := strings.TrimSpace(reply)
	for _, candidate := range senderNameCandidates(message.Sender) {
		withoutName, changed := stripVocativeName(trimmed, candidate)
		if changed {
			return strings.TrimSpace(withoutName)
		}
	}
	return trimmed
}

func senderNameCandidates(sender string) []string {
	sender = strings.TrimSpace(sender)
	if sender == "" {
		return nil
	}
	sender = strings.SplitN(sender, "(", 2)[0]
	sender = strings.TrimSpace(sender)
	if sender == "" {
		return nil
	}
	parts := strings.Fields(sender)
	candidates := []string{sender}
	if len(parts) > 0 {
		candidates = append(candidates, parts[0])
	}
	return candidates
}

func stripVocativeName(text, name string) (string, bool) {
	name = strings.TrimSpace(name)
	if name == "" {
		return text, false
	}
	quotedName := regexp.QuoteMeta(name)

	leading := regexp.MustCompile(`(?i)^\s*` + quotedName + `\s*[,!:\-]+\s*`)
	trimmed := leading.ReplaceAllString(text, "")
	changed := trimmed != text

	middle := regexp.MustCompile(`(?i)\s*,\s*` + quotedName + `\s*,\s*`)
	trimmedMiddle := middle.ReplaceAllString(trimmed, " ")
	if trimmedMiddle != trimmed {
		changed = true
	}

	trailing := regexp.MustCompile(`(?i),\s*` + quotedName + `\s*[!?.:;\-]*\s*$`)
	trimmedTrailing := trailing.ReplaceAllString(trimmedMiddle, "")
	if trimmedTrailing != trimmedMiddle {
		changed = true
	}

	return strings.TrimSpace(trimmedTrailing), changed
}
