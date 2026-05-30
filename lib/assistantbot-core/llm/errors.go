package llm

import (
	"errors"
	"fmt"
)

// ErrAllModelsFailed is returned when every retry attempt failed for a task.
var ErrAllModelsFailed = errors.New("all llm retry attempts failed")

func allModelsFailedError(task string, errs []error) error {
	return fmt.Errorf("%w for task %q: %w", ErrAllModelsFailed, task, errors.Join(errs...))
}
