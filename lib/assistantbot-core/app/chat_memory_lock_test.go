package app

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestChatMemoryPrepareQueueFIFOOrder(t *testing.T) {
	locker := newChatMemoryLocker(nil)
	ctx := context.Background()
	gate := make(chan struct{})

	var mu sync.Mutex
	var order []int

	var wg sync.WaitGroup
	job1Started := make(chan struct{})
	job2Started := make(chan struct{})

	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = locker.runPrepare(ctx, "chat-1", func(context.Context) error {
			mu.Lock()
			order = append(order, 1)
			mu.Unlock()
			close(job1Started)
			<-gate
			return nil
		})
	}()
	select {
	case <-job1Started:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("job 1 did not start")
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = locker.runPrepare(ctx, "chat-1", func(context.Context) error {
			mu.Lock()
			order = append(order, 2)
			mu.Unlock()
			close(job2Started)
			return nil
		})
	}()
	select {
	case <-job2Started:
		t.Fatal("job 2 should not start while job 1 holds the prepare worker")
	case <-time.After(50 * time.Millisecond):
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = locker.runPrepare(ctx, "chat-1", func(context.Context) error {
			mu.Lock()
			order = append(order, 3)
			mu.Unlock()
			return nil
		})
	}()

	close(gate)
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	if len(order) != 3 {
		t.Fatalf("expected 3 jobs, got %v", order)
	}
	for i, want := range []int{1, 2, 3} {
		if order[i] != want {
			t.Fatalf("FIFO violated: got %v want [1 2 3]", order)
		}
	}
}

func TestPrepareNotBlockedByBackgroundWorker(t *testing.T) {
	locker := newChatMemoryLocker(nil)
	ctx := context.Background()
	gate := make(chan struct{})

	bgStarted := make(chan struct{})
	go func() {
		_ = locker.runBackground(ctx, "chat-1", func(context.Context) error {
			close(bgStarted)
			<-gate
			return nil
		})
	}()
	<-bgStarted

	prepDone := make(chan struct{})
	go func() {
		_ = locker.runPrepare(ctx, "chat-1", func(context.Context) error {
			return nil
		})
		close(prepDone)
	}()

	select {
	case <-prepDone:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("prepare should not wait for background worker")
	}
	close(gate)
}
