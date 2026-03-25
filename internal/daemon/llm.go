package daemon

import (
	"context"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

// LLM wraps the Anthropic client for streaming messages.
type LLM struct {
	client anthropic.Client
	model  string
	apiKey string
}

// NewLLM creates a new LLM wrapper.
func NewLLM(apiKey, model string, opts ...option.RequestOption) *LLM {
	allOpts := append([]option.RequestOption{option.WithAPIKey(apiKey)}, opts...)
	client := anthropic.NewClient(allOpts...)
	return &LLM{client: client, model: model, apiKey: apiKey}
}

// APIKey returns the API key used by this LLM instance.
func (l *LLM) APIKey() string {
	return l.apiKey
}

// Model returns the model name used by this LLM instance.
func (l *LLM) Model() string {
	return l.model
}

// StreamMessage sends a streaming request to the Anthropic API.
// onDelta is called for each text delta received.
// Returns the accumulated message, elapsed time, and any error.
func (l *LLM) StreamMessage(
	ctx context.Context,
	system []anthropic.TextBlockParam,
	messages []anthropic.MessageParam,
	tools []anthropic.ToolUnionParam,
	onDelta func(string),
) (*anthropic.Message, time.Duration, error) {
	t0 := time.Now()

	stream := l.client.Messages.NewStreaming(ctx, anthropic.MessageNewParams{
		Model:        anthropic.Model(l.model),
		MaxTokens:    32768,
		System:       system,
		Messages:     messages,
		Tools:        tools,
		CacheControl: anthropic.NewCacheControlEphemeralParam(),
	})

	msg := anthropic.Message{}
	for stream.Next() {
		event := stream.Current()
		if err := msg.Accumulate(event); err != nil {
			return nil, 0, err
		}

		switch ev := event.AsAny().(type) {
		case anthropic.ContentBlockDeltaEvent:
			switch delta := ev.Delta.AsAny().(type) {
			case anthropic.TextDelta:
				if onDelta != nil {
					onDelta(delta.Text)
				}
			}
		}
	}

	if err := stream.Err(); err != nil {
		return nil, 0, err
	}

	elapsed := time.Since(t0)
	return &msg, elapsed, nil
}
