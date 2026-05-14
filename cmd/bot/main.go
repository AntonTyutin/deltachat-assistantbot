package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/spf13/cobra"

	"assistantbot/internal/app"
	"assistantbot/internal/config"
	"assistantbot/internal/deltachat"
	"assistantbot/internal/llm"
	"assistantbot/internal/mcpclient"
	"assistantbot/internal/memory"
	"assistantbot/internal/metrics"
	"assistantbot/internal/reply"
	"assistantbot/internal/scheduler"
	"assistantbot/internal/storage"
)

func main() {
	logger := newLogger()
	slog.SetDefault(logger)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := newRootCommand(ctx, logger).Execute(); err != nil {
		logger.Error("command failed", "error", err)
		os.Exit(1)
	}
}

func newRootCommand(ctx context.Context, logger *slog.Logger) *cobra.Command {
	root := &cobra.Command{
		Use:   "assistantbot",
		Short: "Assistant Bot — DeltaChat assistant CLI",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.FromEnv()
			if err != nil {
				return err
			}
			return runBot(ctx, cfg, logger)
		},
	}

	root.AddCommand(newSetupAccountCommand(ctx))
	root.AddCommand(newInviteLinkCommand(ctx))
	root.AddCommand(newEditProfileCommand(ctx))
	root.AddCommand(newRunCommand(ctx, logger))
	return root
}

func newSetupAccountCommand(ctx context.Context) *cobra.Command {
	var setupQRData string
	cmd := &cobra.Command{
		Use:   "setup-account",
		Short: "Create DeltaChat bot account from setup QR",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.FromEnv()
			if err != nil {
				return err
			}
			return runSetupAccount(ctx, cfg, setupQRData)
		},
	}
	cmd.Flags().StringVar(&setupQRData, "qr-data", "", "Raw setup QR data from DeltaChat")
	return cmd
}

func newInviteLinkCommand(ctx context.Context) *cobra.Command {
	return &cobra.Command{
		Use:   "invite-link",
		Short: "Print secure-join invite link for bot account",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.FromEnv()
			if err != nil {
				return err
			}
			return runInviteLink(ctx, cfg)
		},
	}
}

func newEditProfileCommand(ctx context.Context) *cobra.Command {
	var name string
	var bio string
	var photo string
	cmd := &cobra.Command{
		Use:   "edit-profile",
		Short: "Update bot name, bio, and photo",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.FromEnv()
			if err != nil {
				return err
			}
			return runEditProfile(ctx, cfg, deltachat.BotProfileUpdate{
				Name:      name,
				Bio:       bio,
				PhotoPath: photo,
			})
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "Bot display name")
	cmd.Flags().StringVar(&bio, "bio", "", "Bot bio/status text")
	cmd.Flags().StringVar(&photo, "photo", "", "Path to bot photo")
	return cmd
}

func newRunCommand(ctx context.Context, logger *slog.Logger) *cobra.Command {
	return &cobra.Command{
		Use:   "run",
		Short: "Run the bot event loop",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.FromEnv()
			if err != nil {
				return err
			}
			return runBot(ctx, cfg, logger)
		},
	}
}

func runSetupAccount(ctx context.Context, cfg config.Config, setupQRData string) error {
	if strings.TrimSpace(setupQRData) == "" {
		return fmt.Errorf("missing required flag: --qr-data")
	}
	addr, err := deltachat.SetupAccount(ctx, cfg.DeltaChatRPCServerCmd, cfg.DeltaChatAccountsPath, setupQRData)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stdout, "DeltaChat account configured: %s\n", addr)
	return nil
}

func runInviteLink(ctx context.Context, cfg config.Config) error {
	if err := cfg.ValidateInviteLink(); err != nil {
		return err
	}
	link, err := deltachat.InviteLink(ctx, cfg.DeltaChatRPCServerCmd, cfg.DeltaChatAccountsPath)
	if err != nil {
		return err
	}
	fmt.Fprintln(os.Stdout, link)
	return nil
}

func runEditProfile(ctx context.Context, cfg config.Config, update deltachat.BotProfileUpdate) error {
	if err := cfg.ValidateEditProfile(); err != nil {
		return err
	}
	if strings.TrimSpace(update.Name) == "" && strings.TrimSpace(update.Bio) == "" && strings.TrimSpace(update.PhotoPath) == "" {
		return fmt.Errorf("provide at least one flag: --name, --bio, or --photo")
	}
	if strings.TrimSpace(update.PhotoPath) != "" {
		info, err := os.Stat(update.PhotoPath)
		if err != nil {
			return fmt.Errorf("photo file: %w", err)
		}
		if info.IsDir() {
			return fmt.Errorf("photo path points to a directory: %s", update.PhotoPath)
		}
	}
	if err := deltachat.UpdateBotProfile(ctx, cfg.DeltaChatRPCServerCmd, cfg.DeltaChatAccountsPath, update); err != nil {
		return err
	}
	fmt.Fprintln(os.Stdout, "Bot profile updated.")
	return nil
}

func runBot(ctx context.Context, cfg config.Config, logger *slog.Logger) error {
	if err := cfg.ValidateRun(); err != nil {
		return err
	}

	for _, w := range cfg.MCPConfigWarnings {
		logger.Warn("mcp configuration", "detail", w)
	}

	store, err := storage.Open(ctx, cfg.DBPath, cfg.DBEncryptionKey)
	if err != nil {
		return fmt.Errorf("open storage: %w", err)
	}
	defer store.Close()

	recorder := metrics.Noop
	if addr := strings.TrimSpace(cfg.MetricsAddr); addr != "" {
		reg := prometheus.NewRegistry()
		recorder = metrics.NewPrometheus(reg, cfg.DeltaChatAccountAddr)
		srv := &http.Server{
			Addr:    addr,
			Handler: promhttp.HandlerFor(reg, promhttp.HandlerOpts{EnableOpenMetrics: true}),
		}
		go func() {
			logger.Info("metrics server listening", "addr", addr)
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
		llm.WithRecorder(recorder),
	)

	var mcpReg *mcpclient.Registry
	if len(cfg.MCPServers) > 0 {
		logger.Info("connecting mcp servers", "count", len(cfg.MCPServers))
		httpClient := &http.Client{Timeout: cfg.HTTPTimeout}
		connectCtx, cancel := context.WithTimeout(ctx, cfg.HTTPTimeout)
		var connectWarnings []string
		mcpReg, connectWarnings = mcpclient.Connect(connectCtx, cfg.MCPServers, httpClient, recorder)
		cancel()
		for _, w := range connectWarnings {
			logger.Warn("mcp connection", "detail", w)
		}
		if mcpReg != nil {
			defer mcpReg.Close()
		}
	}

	deltaClient := deltachat.NewRPCClient(cfg.DeltaChatRPCServerCmd, cfg.DeltaChatAccountsPath)
	memoryPipeline := memory.NewPipeline(store, llmClient, mcpReg)
	replyService := reply.NewService(store, llmClient, cfg.BotNames, mcpReg, recorder)
	bot := app.New(deltaClient, store, memoryPipeline, replyService, logger, recorder)

	go func() {
		err := scheduler.RunDaily(ctx, cfg.DailySummaryTime, logger, func(ctx context.Context, date time.Time) error {
			chats, err := store.ListChats(ctx)
			if err != nil {
				return err
			}
			for _, chat := range chats {
				if err := memoryPipeline.UpdateDailySummary(ctx, chat.ID, date); err != nil {
					logger.Error("chat daily summary failed", "chat_id", chat.ID, "error", err)
				}
			}
			return nil
		})
		if err != nil && !errors.Is(err, context.Canceled) {
			logger.Error("scheduler stopped", "error", err)
		}
	}()

	logger.Info("Assistant Bot started", "db_path", cfg.DBPath, "model", cfg.LLMModel, "dc_accounts_path", cfg.DeltaChatAccountsPath)
	if err := bot.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		logger.Error("deltachat event loop stopped", "error", err)
		return err
	}
	return nil
}
