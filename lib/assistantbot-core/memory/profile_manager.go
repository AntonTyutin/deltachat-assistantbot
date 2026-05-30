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
	profile, ok, err := m.store.GetProfile(ctx, message.SenderID)
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
		if err := m.store.SetChatName(ctx, message.ChatID, message.SenderID, message.Sender); err != nil {
			return err
		}
	}
	raw, err := m.llm.CompleteJSON(ctx, llm.TaskUpdateProfile, map[string]any{
		"profile": profile,
		"message": message,
	}, `{"city":"string","address":"string","style":"string","verbosity":"short|medium|long","expertise":{"topic":"level"},"interests":["string"],"correction":true}`)
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

func (m *ProfileManager) RebuildParticipantProfile(ctx context.Context, participantID string) error {
	current, ok, err := m.store.GetProfile(ctx, participantID)
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
	messages, err := m.store.ParticipantMessages(ctx, participantID, 100)
	if err != nil {
		return err
	}
	rebuilt := rebuildProfileFallback(current, messages)
	raw, err := m.llm.CompleteJSON(ctx, llm.TaskRebuildProfile, map[string]any{
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
	return m.store.UpsertProfile(ctx, rebuilt)
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
		if interest != "" && !containsString(profile.Interests, interest) {
			profile.Interests = append(profile.Interests, interest)
		}
	}
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
