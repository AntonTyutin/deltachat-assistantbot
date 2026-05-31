package scheduler

import (
	"context"
	"log/slog"
	"time"
)

type ReminderFunc func(ctx context.Context, now time.Time) error

func RunReminders(ctx context.Context, interval time.Duration, logger *slog.Logger, fn ReminderFunc) error {
	if interval <= 0 {
		interval = time.Minute
	}
	run := func(now time.Time) {
		if err := fn(ctx, now); err != nil {
			logger.Error("reminder delivery failed", "error", err)
		}
	}
	run(time.Now())
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case now := <-ticker.C:
			run(now)
		}
	}
}
