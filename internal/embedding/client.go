// Package embedding defines the embedding abstraction.
package embedding

import "context"

// Client generates float vectors for text.
type Client interface {
	EmbedTexts(context.Context, []string) ([][]float32, error)
}
