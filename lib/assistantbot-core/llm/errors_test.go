package llm

import (
	"errors"
	"fmt"
	"testing"
)

func TestAllModelsFailedError(t *testing.T) {
	err := allModelsFailedError("generate_chat_reply", []error{fmt.Errorf("model-a: timeout")})
	if !errors.Is(err, ErrAllModelsFailed) {
		t.Fatalf("expected ErrAllModelsFailed, got %v", err)
	}
}
