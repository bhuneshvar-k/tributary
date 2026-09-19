// Package ai provides a natural-language interface for Tributary.
//
// Users can say things like:
//
//	tributary ai "sync user admin@example.com"
//	tributary ai "plan a subset for all active users"
//
// The AI parses the prompt into a structured Tributary command, optionally
// inspects the database schema for context, and executes or previews the
// resulting command.
package ai

import (
	"context"
	"fmt"
	"time"
)

// Provider is the interface every AI backend must implement.
type Provider interface {
	// Name returns the provider identifier ("claude", "openai", etc.).
	Name() string

	// Complete sends a chat-completion request and returns the response.
	Complete(ctx context.Context, req *Request) (*Response, error)

	// SupportsStreaming reports whether this provider can stream tokens.
	SupportsStreaming() bool
}

// Request is a unified chat-completion request sent to any provider.
type Request struct {
	// SystemPrompt sets the assistant's role and context.
	SystemPrompt string

	// UserPrompt is the user's natural-language instruction.
	UserPrompt string

	// Model overrides the provider's default model. Empty = default.
	Model string

	// MaxTokens caps the response length. 0 = provider default.
	MaxTokens int

	// Temperature controls randomness. 0 = deterministic.
	Temperature float32
}

// Response is a unified chat-completion response from any provider.
type Response struct {
	// Content is the assistant's reply text.
	Content string

	// Model is the model that actually served the request.
	Model string

	// Tokens tracks usage.
	Tokens TokenUsage

	// Latency is the total round-trip time.
	Latency time.Duration

	// Raw is the unparsed provider response, for debugging.
	Raw []byte
}

// TokenUsage tracks input/output token counts.
type TokenUsage struct {
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
}

// ProviderError is a structured error from an AI provider.
type ProviderError struct {
	Provider  string
	StatusCode int
	Message   string
	Err       error
}

func (e *ProviderError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %s: %v", e.Provider, e.Message, e.Err)
	}
	return fmt.Sprintf("%s: %s (HTTP %d)", e.Provider, e.Message, e.StatusCode)
}

func (e *ProviderError) Unwrap() error {
	return e.Err
}
