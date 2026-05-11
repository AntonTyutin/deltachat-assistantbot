package memory

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	openai "github.com/sashabaranov/go-openai"

	"assistantbot/internal/deltachat"
	"assistantbot/internal/llm"
	"assistantbot/internal/storage"
)

const (
	taskUpdateProfile  = "update_participant_profile"
	taskUpdateTopic    = "update_chat_topic"
	taskRebuildProfile = "rebuild_participant_profile"
	taskRebuildTopic   = "rebuild_chat_topic"
	taskDailySummary   = "daily_summary"
	taskResolveCity    = "resolve_city_from_coordinates"
)

type Pipeline struct {
	store *storage.Store
	llm   llm.Client
	mcp   mcpToolRuntime
}

type mcpToolRuntime interface {
	OpenAITools() []openai.Tool
	ExecuteTool(ctx context.Context, prefixedName string, argumentsJSON string) (string, error)
}

func NewPipeline(store *storage.Store, llmClient llm.Client, mcp mcpToolRuntime) *Pipeline {
	return &Pipeline{store: store, llm: llmClient, mcp: mcp}
}

func (p *Pipeline) ProcessMessage(ctx context.Context, message deltachat.Message) (storage.Topic, error) {
	return p.ProcessNewMessage(ctx, message)
}

func (p *Pipeline) ProcessNewMessage(ctx context.Context, message deltachat.Message) (storage.Topic, error) {
	storedMessage := storage.Message{
		ID:         message.ID,
		ChatID:     message.ChatID,
		SenderID:   message.SenderID,
		Sender:     message.Sender,
		Text:       message.Text,
		IsGroup:    message.IsGroup,
		IsFromSelf: message.IsFromSelf,
		ReplyToID:  message.ReplyToID,
		SentAt:     message.SentAt,
	}
	if err := p.store.UpsertMessage(ctx, storedMessage); err != nil {
		return storage.Topic{}, err
	}
	if !message.IsFromSelf {
		if err := p.updateProfile(ctx, storedMessage); err != nil {
			return storage.Topic{}, err
		}
	}
	topic, err := p.updateTopic(ctx, storedMessage)
	if err != nil {
		return storage.Topic{}, err
	}
	return topic, p.store.AttachMessageToTopic(ctx, message.ChatID, message.ID, topic.ID)
}

func (p *Pipeline) ProcessMessageUpdate(ctx context.Context, message deltachat.Message) error {
	storedMessage := storageMessageFromDelta(message)
	oldMessage, hadOldMessage, err := p.store.GetMessage(ctx, message.ChatID, message.ID)
	if err != nil {
		return err
	}
	topicID, hasTopic, err := p.store.TopicIDForMessage(ctx, message.ChatID, message.ID)
	if err != nil {
		return err
	}
	if !hasTopic {
		topic, err := p.ProcessNewMessage(ctx, message)
		if err != nil {
			return err
		}
		topicID = topic.ID
		hasTopic = true
	} else if err := p.store.UpsertMessage(ctx, storedMessage); err != nil {
		return err
	}
	if hasTopic {
		if _, err := p.RebuildTopic(ctx, topicID); err != nil {
			return err
		}
	}
	if !storedMessage.IsFromSelf {
		if err := p.RebuildParticipantProfile(ctx, storedMessage.SenderID); err != nil {
			return err
		}
	}
	if hadOldMessage && oldMessage.SenderID != "" && oldMessage.SenderID != storedMessage.SenderID && !oldMessage.IsFromSelf {
		return p.RebuildParticipantProfile(ctx, oldMessage.SenderID)
	}
	return nil
}

func (p *Pipeline) ProcessMessageDelete(ctx context.Context, chatID, messageID string) error {
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
		if _, err := p.RebuildTopic(ctx, topicID); err != nil {
			return err
		}
	}
	if ok && oldMessage.SenderID != "" && !oldMessage.IsFromSelf {
		return p.RebuildParticipantProfile(ctx, oldMessage.SenderID)
	}
	return nil
}

func (p *Pipeline) RebuildTopic(ctx context.Context, topicID string) (storage.Topic, error) {
	topic, ok, err := p.store.GetTopic(ctx, topicID)
	if err != nil || !ok {
		return topic, err
	}
	messages, err := p.store.TopicMessages(ctx, topic.ChatID, topicID)
	if err != nil {
		return storage.Topic{}, err
	}
	rebuilt := rebuildTopicFallback(topic, messages)
	raw, err := p.llm.CompleteJSON(ctx, taskRebuildTopic, map[string]any{
		"topic":    topic,
		"messages": messages,
	}, `{"title":"string","summary":"string","decisions":["string"],"open_questions":["string"],"active_participants":["id"]}`)
	if err == nil {
		var patch topicPatch
		if json.Unmarshal(raw, &patch) == nil {
			mergeTopic(&rebuilt, patch)
		}
	}
	rebuilt.UpdatedAt = time.Now()
	return rebuilt, p.store.UpsertTopic(ctx, rebuilt)
}

func (p *Pipeline) RebuildParticipantProfile(ctx context.Context, participantID string) error {
	current, ok, err := p.store.GetProfile(ctx, participantID)
	if err != nil {
		return err
	}
	if !ok {
		current = storage.ParticipantProfile{
			ID:        participantID,
			Names:     map[string]string{},
			Expertise: map[string]string{},
		}
	}
	messages, err := p.store.ParticipantMessages(ctx, participantID, 100)
	if err != nil {
		return err
	}
	rebuilt := rebuildProfileFallback(current, messages)
	raw, err := p.llm.CompleteJSON(ctx, taskRebuildProfile, map[string]any{
		"profile":  current,
		"messages": messages,
	}, `{"names":{"scope":"name"},"city":"string","address":"string","style":"string","verbosity":"short|medium|long","expertise":{"topic":"level"},"interests":["string"]}`)
	if err == nil {
		var full profileRebuild
		if json.Unmarshal(raw, &full) == nil {
			applyProfileRebuild(&rebuilt, full)
		}
	}
	rebuilt.ID = participantID
	rebuilt.UpdatedAt = time.Now()
	return p.store.UpsertProfile(ctx, rebuilt)
}

func (p *Pipeline) UpdateDailySummary(ctx context.Context, chatID string, date time.Time) error {
	recent, err := p.store.RecentMessages(ctx, chatID, 20)
	if err != nil {
		return err
	}
	topics, err := p.store.ListTopics(ctx, chatID, 20)
	if err != nil {
		return err
	}
	raw, err := p.llm.CompleteJSON(ctx, taskDailySummary, map[string]any{
		"chat_id": chatID,
		"date":    date.Format("2006-01-02"),
		"recent":  recent,
		"topics":  topics,
	}, `{"summary":"string"}`)
	if err != nil {
		return err
	}
	var response struct {
		Summary string `json:"summary"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return err
	}
	return p.store.SaveDailySummary(ctx, storage.DailySummary{
		ChatID:    chatID,
		Date:      date.Format("2006-01-02"),
		Summary:   response.Summary,
		CreatedAt: time.Now(),
	})
}

func (p *Pipeline) updateProfile(ctx context.Context, message storage.Message) error {
	profile, ok, err := p.store.GetProfile(ctx, message.SenderID)
	if err != nil {
		return err
	}
	if !ok {
		profile = storage.ParticipantProfile{
			ID:        message.SenderID,
			Names:     map[string]string{},
			Expertise: map[string]string{},
			UpdatedAt: time.Now(),
		}
	}
	if message.Sender != "" {
		profile.Names["self"] = message.Sender
		if err := p.store.SetChatName(ctx, message.ChatID, message.SenderID, message.Sender); err != nil {
			return err
		}
	}
	raw, err := p.llm.CompleteJSON(ctx, taskUpdateProfile, map[string]any{
		"profile": profile,
		"message": message,
	}, `{"city":"string","address":"string","style":"string","verbosity":"short|medium|long","expertise":{"topic":"level"},"interests":["string"],"correction":true}`)
	if err != nil {
		return p.store.UpsertProfile(ctx, profile)
	}
	var patch profilePatch
	if err := json.Unmarshal(raw, &patch); err != nil {
		return p.store.UpsertProfile(ctx, profile)
	}
	mergeProfile(&profile, patch)
	profile.UpdatedAt = time.Now()
	return p.store.UpsertProfile(ctx, profile)
}

func (p *Pipeline) updateTopic(ctx context.Context, message storage.Message) (storage.Topic, error) {
	now := time.Now()
	if topic, ok, err := p.store.TopicForReply(ctx, message.ChatID, message.ReplyToID); err != nil {
		return storage.Topic{}, err
	} else if ok {
		return p.patchTopic(ctx, topic, message)
	}

	topics, err := p.store.ListTopics(ctx, message.ChatID, 5)
	if err != nil {
		return storage.Topic{}, err
	}
	if matched, ok := matchTopic(message.Text, topics); ok {
		return p.patchTopic(ctx, matched, message)
	}

	topic := storage.Topic{
		ID:                 storage.NewTopicID(message.ChatID, now),
		ChatID:             message.ChatID,
		Title:              titleFromText(message.Text),
		Summary:            message.Text,
		ActiveParticipants: []string{message.SenderID},
		UpdatedAt:          now,
	}
	return p.patchTopic(ctx, topic, message)
}

func (p *Pipeline) patchTopic(ctx context.Context, topic storage.Topic, message storage.Message) (storage.Topic, error) {
	recent, err := p.store.RecentMessages(ctx, message.ChatID, 20)
	if err != nil {
		return storage.Topic{}, err
	}
	raw, err := p.llm.CompleteJSON(ctx, taskUpdateTopic, map[string]any{
		"topic":   topic,
		"message": message,
		"recent":  recent,
	}, `{"title":"string","summary":"string","decisions":["string"],"open_questions":["string"],"active_participants":["id"]}`)
	if err == nil {
		var patch topicPatch
		if json.Unmarshal(raw, &patch) == nil {
			mergeTopic(&topic, patch)
		}
	}
	if !contains(topic.ActiveParticipants, message.SenderID) && message.SenderID != "" {
		topic.ActiveParticipants = append(topic.ActiveParticipants, message.SenderID)
	}
	topic.UpdatedAt = time.Now()
	return topic, p.store.UpsertTopic(ctx, topic)
}

func mergeProfile(profile *storage.ParticipantProfile, patch profilePatch) {
	if strings.TrimSpace(patch.City) != "" {
		profile.City = strings.TrimSpace(patch.City)
	}
	if strings.TrimSpace(patch.Address) != "" {
		profile.Address = strings.TrimSpace(patch.Address)
	}
	if patch.Style != "" {
		profile.Style = patch.Style
	}
	if patch.Verbosity != "" {
		profile.Verbosity = patch.Verbosity
	}
	if profile.Expertise == nil {
		profile.Expertise = map[string]string{}
	}
	for topic, level := range patch.Expertise {
		if topic != "" && level != "" {
			profile.Expertise[topic] = level
		}
	}
	for _, interest := range patch.Interests {
		interest = strings.TrimSpace(strings.ToLower(interest))
		if interest != "" && !contains(profile.Interests, interest) {
			profile.Interests = append(profile.Interests, interest)
		}
	}
}

func mergeTopic(topic *storage.Topic, patch topicPatch) {
	if patch.Title != "" {
		topic.Title = patch.Title
	}
	if patch.Summary != "" {
		topic.Summary = patch.Summary
	}
	if patch.Decisions != nil {
		topic.Decisions = patch.Decisions
	}
	if patch.OpenQuestions != nil {
		topic.OpenQuestions = patch.OpenQuestions
	}
	for _, participant := range patch.ActiveParticipants {
		if participant != "" && !contains(topic.ActiveParticipants, participant) {
			topic.ActiveParticipants = append(topic.ActiveParticipants, participant)
		}
	}
}

func storageMessageFromDelta(message deltachat.Message) storage.Message {
	return storage.Message{
		ID:         message.ID,
		ChatID:     message.ChatID,
		SenderID:   message.SenderID,
		Sender:     message.Sender,
		Text:       message.Text,
		IsGroup:    message.IsGroup,
		IsFromSelf: message.IsFromSelf,
		ReplyToID:  message.ReplyToID,
		SentAt:     message.SentAt,
	}
}

func rebuildTopicFallback(topic storage.Topic, messages []storage.Message) storage.Topic {
	topic.Decisions = nil
	topic.OpenQuestions = nil
	topic.ActiveParticipants = nil
	if len(messages) == 0 {
		topic.Summary = ""
		return topic
	}
	topic.Title = titleFromText(messages[0].Text)
	var summaries []string
	for _, message := range messages {
		if strings.TrimSpace(message.Text) != "" {
			summaries = append(summaries, message.Text)
		}
		if message.SenderID != "" && !contains(topic.ActiveParticipants, message.SenderID) {
			topic.ActiveParticipants = append(topic.ActiveParticipants, message.SenderID)
		}
	}
	topic.Summary = strings.Join(summaries, "\n")
	return topic
}

func rebuildProfileFallback(profile storage.ParticipantProfile, messages []storage.Message) storage.ParticipantProfile {
	names := profile.Names
	if names == nil {
		names = map[string]string{}
	}
	profile = storage.ParticipantProfile{
		ID:        profile.ID,
		Names:     names,
		Expertise: map[string]string{},
	}
	for _, message := range messages {
		if message.Sender != "" {
			profile.Names["self"] = message.Sender
		}
	}
	return profile
}

func applyProfileRebuild(profile *storage.ParticipantProfile, rebuild profileRebuild) {
	if rebuild.Names != nil {
		profile.Names = rebuild.Names
	}
	if profile.Names == nil {
		profile.Names = map[string]string{}
	}
	if strings.TrimSpace(rebuild.City) != "" {
		profile.City = strings.TrimSpace(rebuild.City)
	}
	if strings.TrimSpace(rebuild.Address) != "" {
		profile.Address = strings.TrimSpace(rebuild.Address)
	}
	profile.Style = rebuild.Style
	profile.Verbosity = rebuild.Verbosity
	profile.Expertise = rebuild.Expertise
	if profile.Expertise == nil {
		profile.Expertise = map[string]string{}
	}
	profile.Interests = rebuild.Interests
}

func (p *Pipeline) resolveCityFromCoordinates(ctx context.Context, latitude, longitude float64) (string, string, error) {
	if p.mcp == nil {
		return "", "", nil
	}
	tools := p.mcp.OpenAITools()
	if len(tools) == 0 {
		return "", "", nil
	}
	prompt := `You resolve user city from coordinates.
Call available MCP geolocation/reverse-geocoding tools if needed.
Return one JSON object only, without markdown: {"city":"name or empty string","address":"short address or empty string"}.
Use empty city when city cannot be determined reliably. Address might be incomplete or missing.`
	userInput, _ := json.Marshal(map[string]any{
		"latitude":  latitude,
		"longitude": longitude,
	})
	text, err := p.llm.ChatWithTools(ctx, taskResolveCity, []openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleSystem, Content: prompt},
		{Role: openai.ChatMessageRoleUser, Content: string(userInput)},
	}, tools, p.mcp.ExecuteTool)
	if err != nil {
		return "", "", err
	}
	text = strings.TrimSpace(text)
	text = strings.TrimPrefix(text, "```json")
	text = strings.TrimPrefix(text, "```")
	text = strings.TrimSuffix(text, "```")
	text = strings.TrimSpace(text)
	var response struct {
		City    string `json:"city"`
		Address string `json:"address"`
	}
	if err := json.Unmarshal([]byte(text), &response); err != nil {
		return "", "", nil
	}
	return strings.TrimSpace(response.City), strings.TrimSpace(response.Address), nil
}

func (p *Pipeline) UpdateParticipantLocationFromCoordinates(ctx context.Context, participantID string, latitude, longitude float64) error {
	participantID = strings.TrimSpace(participantID)
	if participantID == "" {
		return nil
	}
	city, address, err := p.resolveCityFromCoordinates(ctx, latitude, longitude)
	if err != nil {
		return err
	}
	city = strings.TrimSpace(city)
	address = strings.TrimSpace(address)
	if city == "" && address == "" {
		return nil
	}
	profile, ok, err := p.store.GetProfile(ctx, participantID)
	if err != nil {
		return err
	}
	if !ok {
		profile = storage.ParticipantProfile{
			ID:        participantID,
			Names:     map[string]string{},
			Expertise: map[string]string{},
		}
	}
	if profile.City == city && profile.Address == address {
		return nil
	}
	if city != "" {
		profile.City = city
	}
	if address != "" {
		profile.Address = address
	}
	profile.UpdatedAt = time.Now()
	return p.store.UpsertProfile(ctx, profile)
}

func matchTopic(text string, topics []storage.Topic) (storage.Topic, bool) {
	lower := strings.ToLower(text)
	for _, topic := range topics {
		title := strings.ToLower(topic.Title)
		if title != "" && strings.Contains(lower, title) {
			return topic, true
		}
		for _, word := range strings.Fields(title) {
			if len([]rune(word)) > 4 && strings.Contains(lower, word) {
				return topic, true
			}
		}
	}
	return storage.Topic{}, false
}

func titleFromText(text string) string {
	text = strings.TrimSpace(text)
	runes := []rune(text)
	if len(runes) > 48 {
		return string(runes[:48])
	}
	if text == "" {
		return "Новая тема"
	}
	return text
}

func contains(items []string, needle string) bool {
	for _, item := range items {
		if item == needle {
			return true
		}
	}
	return false
}

type profilePatch struct {
	City      string            `json:"city"`
	Address   string            `json:"address"`
	Style     string            `json:"style"`
	Verbosity string            `json:"verbosity"`
	Expertise map[string]string `json:"expertise"`
	Interests []string          `json:"interests"`
}

type profileRebuild struct {
	Names     map[string]string `json:"names"`
	City      string            `json:"city"`
	Address   string            `json:"address"`
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
