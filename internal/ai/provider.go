package ai

import "context"

type Provider interface {
	Rewrite(ctx context.Context, query string) (string, error)
	EmbedBatch(ctx context.Context, texts []string) ([][]float32, error)
	Rerank(ctx context.Context, query string, docs []string) ([]float64, error)
}
