package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/AntonTyutin/assistantbot-core/mcpclient"
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
	HTTPTimeout                time.Duration
	EmbeddingModel             string
	EmbeddingDimensions        int
	RecentMessagesLimit        int
	ReminderPollInterval       time.Duration
	TopicKNNMessages           int
	TopicKNNTopics             int
	ListKNNLists               int
	LLMMaxCompletionTokens     int
	LLMRetryBackoffMultiplier  float64
	MCPServers                 map[string]mcpclient.MCPServerEntry
	MCPConfigWarnings          []string
	MetricsAddr                string
	LLMPromptsFile             string
}

func FromEnv() (Config, error) {
	mcpServers, mcpWarnings := mcpclient.LoadMCPServersFromFile()
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
		HTTPTimeout:                time.Duration(envInt("HTTP_TIMEOUT_SECONDS", 30)) * time.Second,
		EmbeddingModel:             env("EMBEDDING_MODEL", "text-embedding-3-small"),
		EmbeddingDimensions:        envInt("EMBEDDING_DIMENSIONS", 1536),
		RecentMessagesLimit:        envInt("RECENT_MESSAGES_LIMIT", 20),
		ReminderPollInterval:       time.Duration(envInt("REMINDER_POLL_INTERVAL_SECONDS", 60)) * time.Second,
		TopicKNNMessages:           envInt("TOPIC_KNN_MESSAGES", 5),
		TopicKNNTopics:             envInt("TOPIC_KNN_TOPICS", 5),
		ListKNNLists:               envInt("LIST_KNN_LISTS", 5),
		LLMMaxCompletionTokens:     llmMaxCompletionTokens(),
		LLMRetryBackoffMultiplier:  llmRetryBackoffMultiplier(),
		MCPServers:                 mcpServers,
		MCPConfigWarnings:          mcpWarnings,
		MetricsAddr:                strings.TrimSpace(os.Getenv("ASSISTANT_BOT_METRICS_ADDR")),
		LLMPromptsFile:             strings.TrimSpace(os.Getenv("ASSISTANT_BOT_LLM_PROMPTS_FILE")),
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
	if c.LLMPromptsFile == "" {
		missing = append(missing, "ASSISTANT_BOT_LLM_PROMPTS_FILE")
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

// AppDebug reports whether APP_DEBUG is enabled (verbose logging, including LLM bodies).
func AppDebug() bool {
	return envBool("APP_DEBUG")
}

func envBool(name string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(name))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
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
	setTasks(os.Getenv("LLM_MODEL_TOPIC"), "update_chat_topic", "classify_message_topic")
	setTasks(os.Getenv("LLM_MODEL_PROFILE"), "update_participant_profile")
	return models
}

func llmRetryBackoffMultiplier() float64 {
	raw := strings.TrimSpace(os.Getenv("LLM_RETRY_BACKOFF_MULTIPLIER"))
	if raw == "" {
		return 2
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil || value < 1 {
		return 2
	}
	return value
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
	setTasks(os.Getenv("LLM_MAX_COMPLETION_TOKENS_TOPIC"), "update_chat_topic", "classify_message_topic")
	setTasks(os.Getenv("LLM_MAX_COMPLETION_TOKENS_PROFILE"), "update_participant_profile")
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
