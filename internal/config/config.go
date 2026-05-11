package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	DBPath                     string
	DBEncryptionKey            string
	DeltaChatAccountsPath      string
	DeltaChatRPCServerCmd      string
	LLMBaseURL                 string
	LLMAPIKey                  string
	LLMModel                   string
	LLMTaskModels              map[string]string
	LLMTaskMaxCompletionTokens map[string]int
	BotNames                   []string
	DailySummaryTime           string
	HTTPTimeout                time.Duration
	LLMMaxCompletionTokens     int
	MCPServers                 map[string]MCPServerEntry
	MCPConfigWarnings          []string
}

func FromEnv() (Config, error) {
	mcpServers, mcpWarnings := LoadMCPServersFromFile()
	cfg := Config{
		DBPath:                     env("ASSISTANT_BOT_DB_PATH", "/data/assistantbot.db"),
		DBEncryptionKey:            os.Getenv("ASSISTANT_BOT_DB_KEY"),
		DeltaChatAccountsPath:      env("DC_ACCOUNTS_PATH", "/data/deltachat-accounts"),
		DeltaChatRPCServerCmd:      env("DELTACHAT_RPC_SERVER_COMMAND", "deltachat-rpc-server"),
		LLMBaseURL:                 env("LLM_BASE_URL", "https://openrouter.ai/api/v1"),
		LLMAPIKey:                  os.Getenv("LLM_API_KEY"),
		LLMModel:                   env("LLM_MODEL_DEFAULT", "openai/gpt-4o-mini"),
		LLMTaskModels:              llmTaskModels(),
		LLMTaskMaxCompletionTokens: llmTaskMaxCompletionTokens(),
		BotNames:                   splitList(env("BOT_NAMES", "bot,assistant")),
		DailySummaryTime:           env("DAILY_SUMMARY_TIME", "03:00"),
		HTTPTimeout:                time.Duration(envInt("HTTP_TIMEOUT_SECONDS", 30)) * time.Second,
		LLMMaxCompletionTokens:     llmMaxCompletionTokens(),
		MCPServers:                 mcpServers,
		MCPConfigWarnings:          mcpWarnings,
	}
	return cfg, nil
}

func (c Config) ValidateRun() error {
	var missing []string
	if c.DBEncryptionKey == "" {
		missing = append(missing, "ASSISTANT_BOT_DB_KEY")
	}
	if c.LLMAPIKey == "" {
		missing = append(missing, "LLM_API_KEY")
	}
	return c.validate(missing)
}

func (c Config) ValidateInviteLink() error {
	return c.validate(nil)
}

func (c Config) ValidateEditProfile() error {
	return c.validate(nil)
}

func (c Config) validate(missing []string) error {
	if len(missing) > 0 {
		return fmt.Errorf("missing required env vars: %s", strings.Join(missing, ", "))
	}
	if _, err := time.Parse("15:04", c.DailySummaryTime); err != nil {
		return fmt.Errorf("DAILY_SUMMARY_TIME must be HH:MM: %w", err)
	}
	return nil
}

func env(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func envInt(name string, fallback int) int {
	raw := os.Getenv(name)
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return value
}

func llmTaskModels() map[string]string {
	models := map[string]string{}
	setTasks := func(model string, tasks ...string) {
		if model == "" {
			return
		}
		for _, task := range tasks {
			models[task] = model
		}
	}

	setTasks(os.Getenv("LLM_MODEL_REPLY"), "generate_chat_reply")
	setTasks(os.Getenv("LLM_MODEL_SUMMARY"), "daily_summary")
	setTasks(os.Getenv("LLM_MODEL_TOPIC"), "update_chat_topic", "rebuild_chat_topic")
	setTasks(os.Getenv("LLM_MODEL_PROFILE"), "update_participant_profile", "rebuild_participant_profile")
	setTasks(os.Getenv("LLM_MODEL_TOPIC_UPDATE"), "update_chat_topic")
	setTasks(os.Getenv("LLM_MODEL_TOPIC_REBUILD"), "rebuild_chat_topic")
	setTasks(os.Getenv("LLM_MODEL_PROFILE_UPDATE"), "update_participant_profile")
	setTasks(os.Getenv("LLM_MODEL_PROFILE_REBUILD"), "rebuild_participant_profile")
	return models
}

func llmMaxCompletionTokens() int {
	n := envInt("LLM_MAX_COMPLETION_TOKENS", 2048)
	if n <= 0 {
		return 2048
	}
	return n
}

func llmTaskMaxCompletionTokens() map[string]int {
	limits := map[string]int{}
	setTasks := func(raw string, tasks ...string) {
		if strings.TrimSpace(raw) == "" {
			return
		}
		value, err := strconv.Atoi(strings.TrimSpace(raw))
		if err != nil || value <= 0 {
			return
		}
		for _, task := range tasks {
			limits[task] = value
		}
	}

	setTasks(os.Getenv("LLM_MAX_COMPLETION_TOKENS_REPLY"), "generate_chat_reply")
	setTasks(os.Getenv("LLM_MAX_COMPLETION_TOKENS_SUMMARY"), "daily_summary")
	setTasks(os.Getenv("LLM_MAX_COMPLETION_TOKENS_TOPIC"), "update_chat_topic", "rebuild_chat_topic")
	setTasks(os.Getenv("LLM_MAX_COMPLETION_TOKENS_PROFILE"), "update_participant_profile", "rebuild_participant_profile")
	setTasks(os.Getenv("LLM_MAX_COMPLETION_TOKENS_TOPIC_UPDATE"), "update_chat_topic")
	setTasks(os.Getenv("LLM_MAX_COMPLETION_TOKENS_TOPIC_REBUILD"), "rebuild_chat_topic")
	setTasks(os.Getenv("LLM_MAX_COMPLETION_TOKENS_PROFILE_UPDATE"), "update_participant_profile")
	setTasks(os.Getenv("LLM_MAX_COMPLETION_TOKENS_PROFILE_REBUILD"), "rebuild_participant_profile")
	return limits
}

func splitList(raw string) []string {
	parts := strings.Split(raw, ",")
	items := make([]string, 0, len(parts))
	for _, part := range parts {
		item := strings.TrimSpace(part)
		if item != "" {
			items = append(items, strings.ToLower(item))
		}
	}
	return items
}
