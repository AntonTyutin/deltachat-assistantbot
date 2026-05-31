package scheduler

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"
)

func TestRunRemindersRunsImmediately(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var (
		mu    sync.Mutex
		calls int
	)
	done := make(chan struct{})
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
		close(done)
	}()

	err := RunReminders(ctx, time.Hour, slog.Default(), func(_ context.Context, _ time.Time) error {
		mu.Lock()
		calls++
		mu.Unlock()
		return nil
	})
	if err != context.Canceled {
		t.Fatalf("expected context canceled, got %v", err)
	}
	<-done

	mu.Lock()
	defer mu.Unlock()
	if calls < 1 {
		t.Fatalf("expected at least one immediate run, got %d", calls)
	}
}
