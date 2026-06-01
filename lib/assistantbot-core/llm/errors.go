package llm

import (
	"errors"
	"fmt"
)

// ErrAllModelsFailed is returned when every retry attempt failed for a task.
var ErrAllModelsFailed = errors.New("all llm retry attempts failed")

// ErrEmptyMessageContent is returned when the model finishes without tool calls and no text.
var ErrEmptyMessageContent = errors.New("llm returned empty message content")

func allModelsFailedError(task string, errs []error) error {
	return fmt.Errorf("%w for task %q: %w", ErrAllModelsFailed, task, errors.Join(errs...))
}
