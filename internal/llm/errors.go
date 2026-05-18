package llm

import (
	"errors"
	"fmt"
)

// ErrAllModelsFailed is returned when every configured model failed for a task.
var ErrAllModelsFailed = errors.New("all llm model attempts failed")

func allModelsFailedError(task string, errs []error) error {
	return fmt.Errorf("%w for task %q: %w", ErrAllModelsFailed, task, errors.Join(errs...))
}
