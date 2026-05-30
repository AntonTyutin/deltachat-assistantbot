package memory

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	openai "github.com/sashabaranov/go-openai"

	"github.com/AntonTyutin/assistantbot-core/llm"
	"github.com/AntonTyutin/assistantbot-core/storage"
)

type LocationResolver struct {
	store   *storage.Store
	llm     llm.Client
	mcp     mcpToolRuntime
	prompts promptRegistry
}

func NewLocationResolver(store *storage.Store, llmClient llm.Client, mcp mcpToolRuntime, promptReg promptRegistry) *LocationResolver {
	return &LocationResolver{store: store, llm: llmClient, mcp: mcp, prompts: promptReg}
}

func (r *LocationResolver) resolveCityFromCoordinates(ctx context.Context, latitude, longitude float64) (string, string, error) {
	if r.mcp == nil {
		return "", "", nil
	}
	tools := r.mcp.OpenAITools()
	if len(tools) == 0 {
		return "", "", nil
	}
	userInput, _ := json.Marshal(map[string]any{
		"latitude":  latitude,
		"longitude": longitude,
	})
	text, err := r.llm.ChatWithTools(ctx, llm.TaskChatWithTools, []openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleSystem, Content: r.prompts.SystemPromptForMCP(r.mcp.SystemPromptAppend())},
		{Role: openai.ChatMessageRoleUser, Content: string(userInput)},
	}, tools, r.mcp.ExecuteTool)
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

func (r *LocationResolver) UpdateParticipantLocationFromCoordinates(ctx context.Context, participantID string, latitude, longitude float64) error {
	participantID = strings.TrimSpace(participantID)
	if participantID == "" {
		return nil
	}
	city, address, err := r.resolveCityFromCoordinates(ctx, latitude, longitude)
	if err != nil {
		return err
	}
	city = strings.TrimSpace(city)
	address = strings.TrimSpace(address)
	if city == "" && address == "" {
		return nil
	}
	profile, ok, err := r.store.GetProfile(ctx, participantID)
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
	return r.store.UpsertProfile(ctx, profile)
}
