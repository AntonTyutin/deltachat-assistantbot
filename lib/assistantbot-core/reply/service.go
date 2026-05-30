package reply

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"

	openai "github.com/sashabaranov/go-openai"

	"github.com/AntonTyutin/assistantbot-core/llm"
	"github.com/AntonTyutin/assistantbot-core/llm/prompts"
	"github.com/AntonTyutin/assistantbot-core/mcpclient"
	"github.com/AntonTyutin/assistantbot-core/metrics"
	"github.com/AntonTyutin/assistantbot-core/storage"
	"github.com/AntonTyutin/assistantbot-core/transport"
)

const failureReply = "😵"

type Service struct {
	store      *storage.Store
	llm        llm.Client
	prompts    *prompts.Registry
	classifier *Classifier
	mcp        *mcpclient.Registry
	logger     *slog.Logger
	recorder   metrics.Recorder
}

func NewService(store *storage.Store, llmClient llm.Client, promptReg *prompts.Registry, botNames []string, mcp *mcpclient.Registry, logger *slog.Logger, recorder metrics.Recorder) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	if recorder == nil {
		recorder = metrics.Noop
	}
	return &Service{
		store:      store,
		llm:        llmClient,
		prompts:    promptReg,
		classifier: NewClassifier(botNames),
		mcp:        mcp,
		logger:     logger,
		recorder:   recorder,
	}
}

func (s *Service) Decide(ctx context.Context, message transport.Message, topic storage.Topic) (*transport.OutboundMessage, Classification, error) {
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
		s.logger.ErrorContext(ctx, "reply generation failed",
			"chat_id", message.ChatID,
			"message_id", message.ID,
			"intent", classification.Intent,
			"error", err,
		)
		return failureOutbound(message), classification, nil
	}
	reply = sanitizeDirectAddress(reply, message)
	if strings.TrimSpace(reply) == "" {
		return nil, classification, nil
	}
	return outboundMessage(message, reply, replyToID(message)), classification, nil
}

func replyToID(message transport.Message) string {
	if message.IsGroup {
		return message.ID
	}
	return ""
}

func failureOutbound(message transport.Message) *transport.OutboundMessage {
	return outboundMessage(message, failureReply, replyToID(message))
}

func outboundMessage(message transport.Message, text, replyToID string) *transport.OutboundMessage {
	return &transport.OutboundMessage{
		ChatID:    message.ChatID,
		Text:      text,
		ReplyToID: replyToID,
	}
}

func (s *Service) generate(ctx context.Context, message transport.Message, topic storage.Topic, classification Classification) (string, error) {
	path := metrics.ReplyPathJSON
	if s.mcp != nil {
		path = s.replyToolPath()
	}
	start := time.Now()
	defer func() {
		s.recorder.RecordReplyGenerate(path, time.Since(start))
	}()

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

	if s.mcp != nil && s.mcp.HasToolsForTask(llm.TaskGenerateChatReply) {
		text, err := s.generateWithMCP(ctx, payload)
		if err != nil {
			if classification.Intent == IntentSummary {
				return fallbackSummary(topic), nil
			}
			return "", err
		}
		return strings.TrimSpace(extractReplyFromModel(text)), nil
	}

	raw, err := s.llm.CompleteJSON(ctx, llm.TaskGenerateChatReply, payload, `{"reply":"short chat message in the language of the conversation"}`)
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

func (s *Service) generateWithMCP(ctx context.Context, payload map[string]any) (string, error) {
	inputJSON, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	messages := []openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleSystem, Content: s.prompts.SystemPromptForMCP(s.mcp.SystemPromptAppendForTask(llm.TaskGenerateChatReply))},
		{Role: openai.ChatMessageRoleUser, Content: string(inputJSON)},
	}
	return s.llm.ChatWithTools(ctx, llm.TaskGenerateChatReply, messages, s.mcp.ToolsForTask(llm.TaskGenerateChatReply), s.mcp.ExecuteTool)
}

func (s *Service) replyToolPath() string {
	if s.mcp == nil {
		return metrics.ReplyPathJSON
	}
	tools := s.mcp.ToolsForTask(llm.TaskGenerateChatReply)
	if len(tools) == 0 {
		return metrics.ReplyPathJSON
	}
	hasMCP := false
	hasOpenRouter := false
	for _, t := range tools {
		if llm.IsOpenRouterToolType(t.Type) {
			hasOpenRouter = true
		} else {
			hasMCP = true
		}
	}
	switch {
	case hasMCP && hasOpenRouter:
		return metrics.ReplyPathMixedTools
	case hasOpenRouter:
		return metrics.ReplyPathOpenRouterTools
	default:
		return metrics.ReplyPathMCPTools
	}
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

func sanitizeDirectAddress(reply string, message transport.Message) string {
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
