package storage

type Options struct {
	RecentMessagesLimit int
	EmbeddingDimensions int
}

func (o Options) withDefaults() Options {
	if o.RecentMessagesLimit <= 0 {
		o.RecentMessagesLimit = 20
	}
	if o.EmbeddingDimensions <= 0 {
		o.EmbeddingDimensions = 1536
	}
	return o
}
