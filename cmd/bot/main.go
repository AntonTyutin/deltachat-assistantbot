package main

import (
	"context"
	"errors"
	"fmt"
	"github.com/spf13/cobra"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"assistantbot/internal/config"
	"assistantbot/internal/deltachat"
	"assistantbot/internal/runner"
)

func main() {
	logger := newLogger()
	slog.SetDefault(logger)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := newRootCommand(ctx, logger).Execute(); err != nil {
		if serviceLogging() {
			logger.Error("command failed", "error", err)
		}
		os.Exit(1)
	}
}

func newRootCommand(ctx context.Context, logger *slog.Logger) *cobra.Command {
	root := &cobra.Command{
		Use:          "assistantbot",
		Short:        "Assistant Bot — DeltaChat assistant CLI",
		SilenceUsage: true,
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
	bot, cleanup, err := runner.NewFromConfig(ctx, cfg, logger)
	if err != nil {
		return err
	}
	defer cleanup()
	if err := bot.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		logger.Error("deltachat event loop stopped", "error", err)
		return err
	}
	return nil
}
