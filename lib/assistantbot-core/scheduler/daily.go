package scheduler

import (
	"context"
	"log/slog"
	"time"
)

type DailySummaryFunc func(ctx context.Context, date time.Time) error

func RunDaily(ctx context.Context, at string, logger *slog.Logger, fn DailySummaryFunc) error {
	clock, err := time.Parse("15:04", at)
	if err != nil {
		return err
	}
	for {
		next := nextRun(time.Now(), clock)
		timer := time.NewTimer(time.Until(next))
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
			if err := fn(ctx, next); err != nil {
				logger.Error("daily summary failed", "error", err)
			}
		}
	}
}

func nextRun(now time.Time, clock time.Time) time.Time {
	next := time.Date(now.Year(), now.Month(), now.Day(), clock.Hour(), clock.Minute(), 0, 0, now.Location())
	if !next.After(now) {
		next = next.Add(24 * time.Hour)
	}
	return next
}
