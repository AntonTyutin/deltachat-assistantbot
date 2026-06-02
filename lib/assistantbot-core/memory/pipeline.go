package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	openai "github.com/sashabaranov/go-openai"

	"github.com/AntonTyutin/assistantbot-core/llm"
	"github.com/AntonTyutin/assistantbot-core/metrics"
	"github.com/AntonTyutin/assistantbot-core/reminders"
	"github.com/AntonTyutin/assistantbot-core/storage"
	"github.com/AntonTyutin/assistantbot-core/transport"
)

type Pipeline struct {
	store    *storage.Store
	llm      llm.Client
	embedder llm.Embedder
	mcp      mcpToolRuntime
	prompts  promptRegistry
	cfg      Config

	profiles  *ProfileManager
	topics    *TopicManager
	locations *LocationResolver
	tools     *ToolRegistry
}

type mcpToolRuntime interface {
	ToolsForTask(task string) []llm.ToolDefinition
	HasToolsForTask(task string) bool
	ExecuteTool(ctx context.Context, prefixedName string, argumentsJSON string) (string, error)
	SystemPromptAppendForTask(task string) string
}

func NewPipeline(store *storage.Store, llmClient llm.Client, embedder llm.Embedder, mcp mcpToolRuntime, promptReg promptRegistry, cfg Config) *Pipeline {
	p := &Pipeline{store: store, llm: llmClient, embedder: embedder, mcp: mcp, prompts: promptReg, cfg: cfg.withDefaults()}
	p.profiles = NewProfileManager(store, llmClient)
	p.topics = NewTopicManager(store, llmClient, embedder, p.cfg)
	p.locations = NewLocationResolver(store, llmClient, mcp, promptReg)
	p.tools = NewToolRegistry(store, embedder, cfg.Logger)
	return p
}

type promptRegistry interface {
	SystemPrompt(task string) string
	SystemPromptForMCP(mcpAppend string) string
}

func (p *Pipeline) recorder() metrics.Recorder {
	if p.cfg.Recorder != nil {
		return p.cfg.Recorder
	}
	return metrics.Noop
}

func (p *Pipeline) EnsureChat(ctx context.Context, message transport.Message) error {
	return p.store.UpsertChat(ctx, storage.Chat{
		ID:        message.ChatID,
		IsGroup:   message.IsGroup,
		UpdatedAt: time.Now(),
	})
}

func (p *Pipeline) ProcessMessage(ctx context.Context, message transport.Message) (storage.Topic, error) {
	return p.ProcessNewMessage(ctx, message)
}

func (p *Pipeline) PrepareForReply(ctx context.Context, message transport.Message) (storage.Topic, error) {
	storedMessage := storage.MessageFromTransport(message)
	topicAssignStart := time.Now()
	topic, embedding, err := p.topics.AssignTopic(ctx, storedMessage)
	p.recorder().RecordMessagePhase(metrics.PhaseMemoryTopicAssign, time.Since(topicAssignStart))
	if err != nil {
		return storage.Topic{}, err
	}
	storedMessage.TopicID = topic.ID
	upsertStart := time.Now()
	if err := p.store.UpsertMessage(ctx, storedMessage, embedding); err != nil {
		return storage.Topic{}, err
	}
	p.recorder().RecordMessagePhase(metrics.PhaseMemoryMessageUpsert, time.Since(upsertStart))
	return topic, nil
}

func (p *Pipeline) ProcessNewMessage(ctx context.Context, message transport.Message) (storage.Topic, error) {
	storedMessage := storage.MessageFromTransport(message)
	if !message.IsFromSelf {
		profileStart := time.Now()
		if err := p.profiles.UpdateFromMessage(ctx, storedMessage); err != nil {
			return storage.Topic{}, err
		}
		p.recorder().RecordMessagePhase(metrics.PhaseMemoryProfileUpdate, time.Since(profileStart))
	}
	topicStart := time.Now()
	topic, err := p.topics.UpdateFromMessage(ctx, storedMessage)
	p.recorder().RecordMessagePhase(metrics.PhaseMemoryTopicUpdate, time.Since(topicStart))
	return topic, err
}

func (p *Pipeline) ProcessMessageUpdate(ctx context.Context, message transport.Message) error {
	storedMessage := storage.MessageFromTransport(message)
	oldMessage, hadOldMessage, err := p.store.GetMessage(ctx, message.ChatID, message.ID)
	if err != nil {
		return err
	}
	topicID, hasTopic, err := p.store.TopicIDForMessage(ctx, message.ChatID, message.ID)
	if err != nil {
		return err
	}
	if !hasTopic {
		newMessageStart := time.Now()
		_, err := p.ProcessNewMessage(ctx, message)
		p.recorder().RecordMessagePhase(metrics.PhaseMemoryNewMessage, time.Since(newMessageStart))
		return err
	}
	storedMessage.TopicID = topicID
	upsertStart := time.Now()
	if err := p.store.UpsertMessage(ctx, storedMessage, nil); err != nil {
		return err
	}
	p.recorder().RecordMessagePhase(metrics.PhaseMemoryMessageUpsert, time.Since(upsertStart))
	if hadOldMessage && messageContentChanged(oldMessage, storedMessage) {
		topicPatchStart := time.Now()
		if _, err := p.topics.PatchFromMessageEdit(ctx, topicID, storedMessage, oldMessage); err != nil {
			return err
		}
		p.recorder().RecordMessagePhase(metrics.PhaseMemoryTopicPatch, time.Since(topicPatchStart))
		if !storedMessage.IsFromSelf {
			profilePatchStart := time.Now()
			if err := p.profiles.PatchFromMessageEdit(ctx, storedMessage, oldMessage); err != nil {
				return err
			}
			p.recorder().RecordMessagePhase(metrics.PhaseMemoryProfilePatch, time.Since(profilePatchStart))
		}
		return nil
	}
	if !storedMessage.IsFromSelf {
		profileStart := time.Now()
		if err := p.profiles.UpdateFromMessage(ctx, storedMessage); err != nil {
			return err
		}
		p.recorder().RecordMessagePhase(metrics.PhaseMemoryProfileUpdate, time.Since(profileStart))
	}
	return nil
}

func (p *Pipeline) ProcessMessageDelete(ctx context.Context, chatID, messageID string) error {
	deleteStart := time.Now()
	defer func() {
		p.recorder().RecordMessagePhase(metrics.PhaseMemoryDelete, time.Since(deleteStart))
	}()

	oldMessage, ok, err := p.store.GetMessage(ctx, chatID, messageID)
	if err != nil {
		return err
	}
	topicID, hasTopic, err := p.store.TopicIDForMessage(ctx, chatID, messageID)
	if err != nil {
		return err
	}
	if err := p.store.DeleteMessage(ctx, chatID, messageID); err != nil {
		return err
	}
	if hasTopic {
		topicPatchStart := time.Now()
		if _, err := p.topics.PatchFromMessageDelete(ctx, topicID, oldMessage); err != nil {
			return err
		}
		p.recorder().RecordMessagePhase(metrics.PhaseMemoryTopicPatch, time.Since(topicPatchStart))
	}
	if ok && oldMessage.SenderID != "" && !oldMessage.IsFromSelf {
		profilePatchStart := time.Now()
		if err := p.profiles.PatchFromMessageDelete(ctx, oldMessage); err != nil {
			return err
		}
		p.recorder().RecordMessagePhase(metrics.PhaseMemoryProfilePatch, time.Since(profilePatchStart))
	}
	return nil
}

func messageContentChanged(old, updated storage.Message) bool {
	return old.Text != updated.Text
}

func (p *Pipeline) UpdateParticipantLocationFromCoordinates(ctx context.Context, participantID string, latitude, longitude float64) error {
	return p.locations.UpdateParticipantLocationFromCoordinates(ctx, participantID, latitude, longitude)
}

func (p *Pipeline) RecentMessages(ctx context.Context, chatID string, limit int) ([]storage.Message, error) {
	return p.store.RecentMessages(ctx, chatID, limit)
}

func (p *Pipeline) GetProfile(ctx context.Context, participantID string) (storage.ParticipantProfile, bool, error) {
	return p.store.GetProfile(ctx, participantID)
}

func (p *Pipeline) GetTopic(ctx context.Context, topicID string) (storage.Topic, bool, error) {
	return p.store.GetTopic(ctx, topicID)
}

func (p *Pipeline) ListTopics(ctx context.Context, chatID string, limit int) ([]storage.Topic, error) {
	return p.store.ListTopics(ctx, chatID, limit)
}

func (p *Pipeline) ListsContext(ctx context.Context, chatID string, message transport.Message) (map[string]any, error) {
	lists, err := p.relevantLists(ctx, chatID, message)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(lists))
	for _, list := range lists {
		items, err := p.store.ListItems(ctx, list.ID)
		if err != nil {
			return nil, err
		}
		out = append(out, map[string]any{
			"id":              list.ID,
			"kind":            list.Kind,
			"title":           list.Title,
			"items_condensed": CondenseListItems(items),
		})
	}
	reminders, err := p.store.ListReminders(ctx, chatID, storage.ReminderPending)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"lists":     out,
		"reminders": reminders,
	}, nil
}

func (p *Pipeline) relevantLists(ctx context.Context, chatID string, message transport.Message) ([]storage.List, error) {
	limit := p.cfg.ListKNNLists
	embedding, err := p.embedQueryMessage(ctx, message)
	if err != nil {
		return nil, err
	}
	if len(embedding) > 0 {
		nearest, err := p.store.NearestLists(ctx, chatID, embedding, limit)
		if err != nil {
			return nil, err
		}
		if len(nearest) > 0 {
			lists := make([]storage.List, len(nearest))
			for i, item := range nearest {
				lists[i] = item.List
			}
			return lists, nil
		}
	}
	all, err := p.store.ListLists(ctx, chatID, "")
	if err != nil {
		return nil, err
	}
	if len(all) > limit {
		all = all[:limit]
	}
	return all, nil
}

func (p *Pipeline) embedQueryMessage(ctx context.Context, message transport.Message) ([]float32, error) {
	if p.embedder == nil {
		return nil, nil
	}
	parentText := ""
	if message.ReplyToID != "" {
		if parent, ok, err := p.store.GetMessage(ctx, message.ChatID, message.ReplyToID); err != nil {
			return nil, err
		} else if ok {
			parentText = parent.Text
		}
	}
	vectors, err := p.embedder.Embed(ctx, llm.MessageEmbeddingText(message.Text, parentText))
	if err != nil {
		return nil, err
	}
	if len(vectors) == 0 {
		return nil, nil
	}
	return vectors[0], nil
}

func (p *Pipeline) ToolRegistry() *ToolRegistry {
	return p.tools
}

func (p *Pipeline) DeliverDueReminders(ctx context.Context, now time.Time, send func(ctx context.Context, chatID, text string) error) error {
	due, err := p.store.DueReminders(ctx, now, 100)
	if err != nil {
		return err
	}
	for _, reminder := range due {
		text, err := p.renderReminderText(ctx, reminder)
		if err != nil {
			p.logReminderRenderError(ctx, reminder, err)
			continue
		}
		if err := send(ctx, reminder.ChatID, text); err != nil {
			return err
		}
		if reminder.Recurrence == nil {
			if err := p.store.AdvanceReminder(ctx, reminder.ID, nil, 0); err != nil {
				return err
			}
			continue
		}
		count := reminder.OccurrenceCount + 1
		anchor := reminder.AnchorAt
		if anchor.IsZero() {
			anchor = reminder.DueAt
		}
		next, ok := reminders.NextDue(anchor, now, *reminder.Recurrence, count)
		if !ok {
			if err := p.store.AdvanceReminder(ctx, reminder.ID, nil, count); err != nil {
				return err
			}
			continue
		}
		if err := p.store.AdvanceReminder(ctx, reminder.ID, &next, count); err != nil {
			return err
		}
	}
	return nil
}

func (p *Pipeline) renderReminderText(ctx context.Context, reminder storage.Reminder) (string, error) {
	if reminder.Mode != storage.ReminderModeAction {
		return "🔔 " + reminder.Text, nil
	}
	actionPrompt := strings.TrimSpace(reminder.ActionPrompt)
	if actionPrompt == "" {
		return "", fmt.Errorf("reminder %s: empty action_prompt", reminder.ID)
	}
	defs := p.actionToolDefs()
	if len(defs) == 0 {
		return "", fmt.Errorf("reminder %s: no tools available for action", reminder.ID)
	}
	system := "Generate a concise message for chat based on the action prompt. Use tools when needed. Return only JSON: {\"reply\":\"...\"}."
	if p.prompts != nil {
		mcpAppend := ""
		if p.mcp != nil {
			mcpAppend = p.mcp.SystemPromptAppendForTask(llm.TaskGenerateChatReply)
		}
		system = p.prompts.SystemPromptForMCP(mcpAppend)
	}
	payload := map[string]any{
		"type":          "reminder_action",
		"chat_id":       reminder.ChatID,
		"requester_id":  reminder.RequesterID,
		"action_prompt": actionPrompt,
		"timezone":      reminderTimezone(reminder),
		"now":           time.Now().UTC().Format(time.RFC3339),
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	msgs := []openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleSystem, Content: system},
		{Role: openai.ChatMessageRoleUser, Content: string(raw)},
	}
	text, err := p.llm.ChatWithTools(ctx, llm.TaskGenerateChatReply, msgs, defs, p.executeActionTool)
	if err != nil {
		return "", err
	}
	reply := extractReplyJSON(text)
	if reply == "" {
		return "", fmt.Errorf("reminder %s: empty action reply", reminder.ID)
	}
	return reply, nil
}

func (p *Pipeline) actionToolDefs() []llm.ToolDefinition {
	if p.mcp == nil {
		return nil
	}
	return p.mcp.ToolsForTask(llm.TaskGenerateChatReply)
}

func (p *Pipeline) executeActionTool(ctx context.Context, toolName string, argumentsJSON string) (string, error) {
	if strings.HasPrefix(toolName, "memory_") {
		return "", fmt.Errorf("memory tools are disabled for reminder actions")
	}
	if p.mcp == nil {
		return "", fmt.Errorf("mcp tools unavailable")
	}
	return p.mcp.ExecuteTool(ctx, toolName, argumentsJSON)
}

func (p *Pipeline) logReminderRenderError(ctx context.Context, reminder storage.Reminder, err error) {
	if p.cfg.Logger == nil {
		return
	}
	p.cfg.Logger.WarnContext(ctx, "reminder action render failed",
		"reminder_id", reminder.ID,
		"chat_id", reminder.ChatID,
		"mode", reminder.Mode,
		"error", err)
}

func reminderTimezone(reminder storage.Reminder) string {
	if reminder.Recurrence != nil && strings.TrimSpace(reminder.Recurrence.Timezone) != "" {
		return strings.TrimSpace(reminder.Recurrence.Timezone)
	}
	return "UTC"
}

func extractReplyJSON(text string) string {
	text = strings.TrimSpace(text)
	text = strings.TrimPrefix(text, "```json")
	text = strings.TrimPrefix(text, "```")
	text = strings.TrimSuffix(text, "```")
	text = strings.TrimSpace(text)
	var payload struct {
		Reply string `json:"reply"`
	}
	if err := json.Unmarshal([]byte(text), &payload); err != nil {
		return ""
	}
	return strings.TrimSpace(payload.Reply)
}

type profilePatch struct {
	City      string            `json:"city"`
	Address   string            `json:"address"`
	Timezone  string            `json:"timezone"`
	Style     string            `json:"style"`
	Verbosity string            `json:"verbosity"`
	Expertise map[string]string `json:"expertise"`
	Interests []string          `json:"interests"`
}

type topicPatch struct {
	Title              string   `json:"title"`
	Summary            string   `json:"summary"`
	Decisions          []string `json:"decisions"`
	OpenQuestions      []string `json:"open_questions"`
	ActiveParticipants []string `json:"active_participants"`
}
