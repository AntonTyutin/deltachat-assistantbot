package memory

import (
	"log/slog"

	"github.com/AntonTyutin/assistantbot-core/metrics"
)

type Config struct {
	TopicKNNMessages int
	TopicKNNTopics   int
	ListKNNLists     int
	Recorder         metrics.Recorder
	Logger           *slog.Logger
}

func (c Config) withDefaults() Config {
	if c.TopicKNNMessages <= 0 {
		c.TopicKNNMessages = 5
	}
	if c.TopicKNNTopics <= 0 {
		c.TopicKNNTopics = 5
	}
	if c.ListKNNLists <= 0 {
		c.ListKNNLists = 5
	}
	return c
}
