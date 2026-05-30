package llm

import (
	"context"
	"fmt"
	"math/rand/v2"
	"time"
)

const (
	replyMaxAttempts         = 5
	replyRetryBaseDelay      = 400 * time.Millisecond
	backgroundMaxAttempts    = 7
	backgroundRetryBaseDelay = time.Second

	defaultRetryBackoffMultiplier = 2.0
)

func isReplyTask(task string) bool {
	return task == TaskGenerateChatReply
}

func retryPolicyForTask(task string) (maxAttempts int, baseDelay time.Duration) {
	if isReplyTask(task) {
		return replyMaxAttempts, replyRetryBaseDelay
	}
	return backgroundMaxAttempts, backgroundRetryBaseDelay
}

func retryBackoffDelay(base time.Duration, multiplier float64, attempt int) time.Duration {
	if attempt <= 1 {
		return 0
	}
	if multiplier < 1 {
		multiplier = defaultRetryBackoffMultiplier
	}
	exp := attempt - 2
	factor := 1.0
	for i := 0; i < exp; i++ {
		factor *= multiplier
	}
	return time.Duration(float64(base) * factor)
}

func pickRandomModel(models []string) string {
	if len(models) == 0 {
		return ""
	}
	if len(models) == 1 {
		return models[0]
	}
	return models[rand.IntN(len(models))]
}

func (c *OpenRouterClient) withModelRetry(ctx context.Context, task string, run func(ctx context.Context, model string) error) error {
	models := c.modelsForTask(task)
	if len(models) == 0 {
		return fmt.Errorf("no llm model configured for task %q", task)
	}
	maxAttempts, baseDelay := retryPolicyForTask(task)
	multiplier := c.retryBackoffMultiplier
	var errs []error

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if attempt > 1 {
			delay := retryBackoffDelay(baseDelay, multiplier, attempt)
			c.logger.InfoContext(ctx, "llm retry backoff", "task", task, "attempt", attempt, "delay", delay)
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
		}

		model := pickRandomModel(models)
		c.logger.InfoContext(ctx, "llm attempt started", "task", task, "model", model, "attempt", attempt, "max_attempts", maxAttempts)
		err := run(ctx, model)
		if err == nil {
			c.logger.InfoContext(ctx, "llm attempt succeeded", "task", task, "model", model, "attempt", attempt)
			return nil
		}
		c.logger.WarnContext(ctx, "llm attempt failed", "task", task, "model", model, "attempt", attempt, "error", err)
		errs = append(errs, fmt.Errorf("attempt %d (%s): %w", attempt, model, err))
	}

	finalErr := allModelsFailedError(task, errs)
	c.logger.ErrorContext(ctx, "llm failed after all retry attempts", "task", task, "models", models, "attempts", maxAttempts, "error", finalErr)
	return finalErr
}
