package app

import (
	"context"
	"sync"
	"time"

	"github.com/AntonTyutin/assistantbot-core/metrics"
)

const (
	chatMemoryQueuePrepare    = "prepare"
	chatMemoryQueueBackground = "background"
)

// chatMemoryLocker runs memory-path work per chat_id. Prepare and background use
// separate FIFO workers so a slow background memory update does not block the next
// message's PrepareForReply (and thus Decide / SendText).
type chatMemoryLocker struct {
	chats sync.Map // string -> *chatMemoryLanes
	rec   metrics.Recorder
}

type chatMemoryLanes struct {
	prepare    *chatMemoryWorker
	background *chatMemoryWorker
}

type chatMemoryWorker struct {
	queue string
	jobs  chan *chatMemoryTask
	start sync.Once
	rec   metrics.Recorder
}

type chatMemoryTask struct {
	ctx        context.Context
	fn         func(context.Context) error
	done       chan error
	enqueuedAt time.Time
}

func newChatMemoryLocker(rec metrics.Recorder) *chatMemoryLocker {
	if rec == nil {
		rec = metrics.Noop
	}
	return &chatMemoryLocker{rec: rec}
}

func (l *chatMemoryLocker) runPrepare(ctx context.Context, chatID string, fn func(context.Context) error) error {
	if chatID == "" {
		return fn(ctx)
	}
	return l.lanesFor(chatID).prepare.run(ctx, fn)
}

func (l *chatMemoryLocker) runBackground(ctx context.Context, chatID string, fn func(context.Context) error) error {
	if chatID == "" {
		return fn(ctx)
	}
	return l.lanesFor(chatID).background.run(ctx, fn)
}

func (l *chatMemoryLocker) lanesFor(chatID string) *chatMemoryLanes {
	if raw, ok := l.chats.Load(chatID); ok {
		return raw.(*chatMemoryLanes)
	}
	lanes := &chatMemoryLanes{
		prepare:    newChatMemoryWorker(chatMemoryQueuePrepare, l.rec),
		background: newChatMemoryWorker(chatMemoryQueueBackground, l.rec),
	}
	raw, _ := l.chats.LoadOrStore(chatID, lanes)
	return raw.(*chatMemoryLanes)
}

func newChatMemoryWorker(queue string, rec metrics.Recorder) *chatMemoryWorker {
	return &chatMemoryWorker{
		queue: queue,
		jobs:  make(chan *chatMemoryTask, 64),
		rec:   rec,
	}
}

func (w *chatMemoryWorker) run(ctx context.Context, fn func(context.Context) error) error {
	w.rec.RecordChatMemoryQueueDepth(w.queue, len(w.jobs))
	task := &chatMemoryTask{
		ctx:        ctx,
		fn:         fn,
		done:       make(chan error, 1),
		enqueuedAt: time.Now(),
	}
	w.start.Do(func() { go w.loop() })

	select {
	case <-ctx.Done():
		return ctx.Err()
	case w.jobs <- task:
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-task.done:
		return err
	}
}

func (w *chatMemoryWorker) loop() {
	for task := range w.jobs {
		start := time.Now()
		if !task.enqueuedAt.IsZero() {
			w.rec.RecordChatMemoryQueueWait(w.queue, start.Sub(task.enqueuedAt))
		}
		if err := task.ctx.Err(); err != nil {
			w.rec.RecordChatMemoryTaskDuration(w.queue, time.Since(start))
			task.done <- err
			continue
		}
		err := task.fn(task.ctx)
		w.rec.RecordChatMemoryTaskDuration(w.queue, time.Since(start))
		task.done <- err
	}
}
