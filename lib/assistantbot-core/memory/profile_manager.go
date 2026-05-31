package memory

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/AntonTyutin/assistantbot-core/llm"
	"github.com/AntonTyutin/assistantbot-core/storage"
)

type ProfileManager struct {
	store *storage.Store
	llm   llm.Client
}

func NewProfileManager(store *storage.Store, llmClient llm.Client) *ProfileManager {
	return &ProfileManager{store: store, llm: llmClient}
}

func (m *ProfileManager) UpdateFromMessage(ctx context.Context, message storage.Message) error {
	return m.patchProfile(ctx, message, nil, "")
}

func (m *ProfileManager) PatchFromMessageEdit(ctx context.Context, message, previous storage.Message) error {
	return m.patchProfile(ctx, message, &previous, "edit")
}

func (m *ProfileManager) PatchFromMessageDelete(ctx context.Context, deleted storage.Message) error {
	return m.patchProfile(ctx, storage.Message{}, &deleted, "delete")
}

func (m *ProfileManager) patchProfile(ctx context.Context, message storage.Message, previous *storage.Message, change string) error {
	participantID := message.SenderID
	if participantID == "" && previous != nil {
		participantID = previous.SenderID
	}
	if participantID == "" {
		return nil
	}

	profile, ok, err := m.store.GetProfile(ctx, participantID)
	if err != nil {
		return err
	}
	if !ok {
		profile = storage.ParticipantProfile{
			ID:        participantID,
			Names:     map[string]string{},
			Expertise: map[string]string{},
			UpdatedAt: time.Now(),
		}
	}
	if message.Sender != "" {
		profile.Names["self"] = message.Sender
		if message.ChatID != "" {
			if err := m.store.SetChatName(ctx, message.ChatID, participantID, message.Sender); err != nil {
				return err
			}
		}
	}

	payload := map[string]any{
		"profile": profile,
		"message": message,
	}
	if previous != nil {
		payload["previous_message"] = *previous
	}
	if change != "" {
		payload["change"] = change
		payload["correction"] = true
	}

	raw, err := m.llm.CompleteJSON(ctx, llm.TaskUpdateProfile, payload,
		`{"city":"string","address":"string","timezone":"string","style":"string","verbosity":"short|medium|long","expertise":{"topic":"level"},"interests":["string"],"correction":true}`)
	if err != nil {
		return m.store.UpsertProfile(ctx, profile)
	}
	var patch profilePatch
	if err := json.Unmarshal(raw, &patch); err != nil {
		return m.store.UpsertProfile(ctx, profile)
	}
	mergeProfile(&profile, patch)
	profile.UpdatedAt = time.Now()
	return m.store.UpsertProfile(ctx, profile)
}

func mergeProfile(profile *storage.ParticipantProfile, patch profilePatch) {
	if strings.TrimSpace(patch.City) != "" {
		profile.City = strings.TrimSpace(patch.City)
	}
	if strings.TrimSpace(patch.Address) != "" {
		profile.Address = strings.TrimSpace(patch.Address)
	}
	if strings.TrimSpace(patch.Timezone) != "" {
		profile.Timezone = strings.TrimSpace(patch.Timezone)
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
		if interest != "" && !containsString(profile.Interests, interest) {
			profile.Interests = append(profile.Interests, interest)
		}
	}
}
