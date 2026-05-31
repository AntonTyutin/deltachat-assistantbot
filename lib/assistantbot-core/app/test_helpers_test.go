package app

import (
	"encoding/json"
	"io"
	"log/slog"
	"testing"

	"github.com/AntonTyutin/assistantbot-core/llm"
	"github.com/AntonTyutin/assistantbot-core/llm/prompts"
	"github.com/AntonTyutin/assistantbot-core/memory"
	"github.com/AntonTyutin/assistantbot-core/reply"
	"github.com/AntonTyutin/assistantbot-core/storage"
	"github.com/AntonTyutin/assistantbot-core/transport"
)

func buildTestApp(messenger transport.Messenger, store *storage.Store, client llm.Client, botNames []string) *App {
	if botNames == nil {
		botNames = []string{"bot", "чатик"}
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	embedder := llm.StaticEmbedder{Vector: []float32{0.1, 0.2, 0.3}}
	pipeline := memory.NewPipeline(store, client, embedder, nil, prompts.FixedTestRegistry(), memory.Config{})
	replyService := reply.NewService(pipeline, client, prompts.FixedTestRegistry(), botNames, nil, logger, nil, 20)
	return New(messenger, pipeline, replyService, logger, nil)
}

func testLLMClient(extra map[string]json.RawMessage) llm.StaticClient {
	responses := map[string]json.RawMessage{
		llm.TaskClassifyMessageTopic: json.RawMessage(`{"is_new_topic":true,"title":"topic","summary":"summary"}`),
	}
	for k, v := range extra {
		responses[k] = v
	}
	return llm.StaticClient{Responses: responses}
}

func openTestStore(t *testing.T) *storage.Store {
	t.Helper()
	return storage.OpenTestDB(t, "test-secret")
}
