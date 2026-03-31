// Package llm defines the large language model abstraction.
package llm

import "context"

// CompletionRequest describes a text generation request.
type CompletionRequest struct {
	SystemPrompt string
	UserPrompt   string
	Temperature  float64
	MaxTokens    int
}

// Client generates text for summaries and autofix drafts.
type Client interface {
	Complete(context.Context, CompletionRequest) (string, error)
}
