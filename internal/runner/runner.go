package runner

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/AntonTyutin/assistantbot-core/app"
	"github.com/AntonTyutin/assistantbot-core/llm"
	"github.com/AntonTyutin/assistantbot-core/llm/prompts"
	"github.com/AntonTyutin/assistantbot-core/mcpclient"
	"github.com/AntonTyutin/assistantbot-core/memory"
	"github.com/AntonTyutin/assistantbot-core/metrics"
	"github.com/AntonTyutin/assistantbot-core/reply"
	"github.com/AntonTyutin/assistantbot-core/scheduler"
	"github.com/AntonTyutin/assistantbot-core/storage"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"assistantbot/internal/config"
	"assistantbot/internal/deltachat"
	"assistantbot/internal/version"
)

func NewFromConfig(ctx context.Context, cfg config.Config, logger *slog.Logger) (*app.App, func(), error) {
	if err := cfg.ValidateRun(); err != nil {
		return nil, nil, err
	}

	for _, w := range cfg.MCPConfigWarnings {
		logger.Warn("mcp configuration", "detail", w)
	}
	if config.AppDebug() {
		logger.Warn("APP_DEBUG enabled", "detail", "LLM and tool call payloads are logged at debug level and may contain sensitive data")
	}

	store, err := storage.Open(ctx, cfg.DBPath, cfg.DBEncryptionKey, storage.Options{
		RecentMessagesLimit: cfg.RecentMessagesLimit,
		EmbeddingDimensions: cfg.EmbeddingDimensions,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("open storage: %w", err)
	}

	cleanupFns := []func(){}
	cleanupFns = append(cleanupFns, func() {
		if err := store.Close(); err != nil {
			logger.Error("store close failed", "error", err)
		}
	})

	deltachatClient := deltachat.NewRPCClient(cfg.DeltaChatRPCServerCmd, cfg.DeltaChatAccountsPath)
	metricsBotID := metricsBotIDFallback()
	if accountAddr, err := deltachatClient.ConfiguredAccountAddr(ctx); err != nil {
		logger.Warn("could not resolve bot account address for metrics bot_id; using hostname", "error", err, "bot_id", metricsBotID)
	} else {
		metricsBotID = accountAddr
	}

	recorder := metrics.Noop
	if addr := strings.TrimSpace(cfg.MetricsAddr); addr != "" {
		shutdownMetrics, metricsRecorder := startMetricsServer(ctx, addr, metricsBotID, logger)
		cleanupFns = append(cleanupFns, shutdownMetrics)
		recorder = metricsRecorder
	}

	promptReg, err := prompts.Load(cfg.LLMPromptsFile)
	if err != nil {
		return nil, nil, fmt.Errorf("load llm prompts: %w", err)
	}

	llmClient := llm.NewOpenRouterClient(
		cfg.LLMBaseURL,
		cfg.LLMAPIKey,
		cfg.LLMModel,
		cfg.LLMTaskModels,
		cfg.LLMTaskMaxCompletionTokens,
		cfg.HTTPTimeout,
		cfg.LLMMaxCompletionTokens,
		logger,
		llm.WithPrompts(promptReg),
		llm.WithRecorder(recorder),
		llm.WithRetryBackoffMultiplier(cfg.LLMRetryBackoffMultiplier),
	)
	embedder := llm.NewOpenAICompatibleEmbedder(cfg.LLMBaseURL, cfg.LLMAPIKey, cfg.EmbeddingModel, cfg.EmbeddingDimensions, cfg.HTTPTimeout)

	var mcpReg *mcpclient.Registry
	if len(cfg.MCPServers) > 0 {
		logger.Info("connecting mcp servers", "count", len(cfg.MCPServers))
		httpClient := &http.Client{Timeout: cfg.HTTPTimeout}
		connectCtx, cancel := context.WithTimeout(ctx, cfg.HTTPTimeout)
		var connectWarnings []string
		mcpReg, connectWarnings = mcpclient.Connect(connectCtx, cfg.MCPServers, httpClient, recorder, logger)
		cancel()
		for _, w := range connectWarnings {
			logger.Warn("mcp connection", "detail", w)
		}
		if mcpReg != nil {
			cleanupFns = append(cleanupFns, func() {
				if err := mcpReg.Close(); err != nil {
					logger.Error("mcp registry close failed", "error", err)
				}
			})
		}
	}

	memoryPipeline := memory.NewPipeline(store, llmClient, embedder, mcpReg, promptReg, memory.Config{
		TopicKNNMessages: cfg.TopicKNNMessages,
		TopicKNNTopics:   cfg.TopicKNNTopics,
		ListKNNLists:     cfg.ListKNNLists,
		Recorder:         recorder,
		Logger:           logger,
	})
	replyService := reply.NewService(memoryPipeline, llmClient, promptReg, cfg.BotNames, mcpReg, logger, recorder, cfg.RecentMessagesLimit)
	bot := app.New(deltachatClient, memoryPipeline, replyService, logger, recorder)

	go func() {
		err := scheduler.RunReminders(ctx, cfg.ReminderPollInterval, logger, func(ctx context.Context, now time.Time) error {
			return bot.DeliverDueReminders(ctx, now)
		})
		if err != nil && !errors.Is(err, context.Canceled) {
			logger.Error("reminder scheduler stopped", "error", err)
		}
	}()

	logger.Info("Assistant Bot started", "db_path", cfg.DBPath, "model", cfg.LLMModel, "account", metricsBotID, "dc_accounts_path", cfg.DeltaChatAccountsPath)
	cleanup := func() {
		var wg sync.WaitGroup
		for i := len(cleanupFns) - 1; i >= 0; i-- {
			wg.Add(1)
			go func(f func()) {
				defer wg.Done()
				f()
			}(cleanupFns[i])
		}
		wg.Wait()
	}
	return bot, cleanup, nil
}

func startMetricsServer(ctx context.Context, addr, botID string, logger *slog.Logger) (func(), metrics.Recorder) {
	reg := prometheus.NewRegistry()
	recorder := metrics.NewPrometheus(reg, botID, version.Version)
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{EnableOpenMetrics: true}))
	srv := &http.Server{
		Addr:    addr,
		Handler: mux,
	}
	go func() {
		logger.Info("metrics server listening", "addr", addr, "path", "/metrics")
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("metrics server exited", "error", err)
		}
	}()
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			logger.Error("metrics server shutdown", "error", err)
		}
	}()
	return func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("metrics server shutdown", "error", err)
		}
	}, recorder
}

func metricsBotIDFallback() string {
	host, err := os.Hostname()
	if err != nil {
		return "unknown"
	}
	host = strings.TrimSpace(host)
	if host == "" {
		return "unknown"
	}
	return host
}
