package translate

import (
	"context"
	"fmt"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
)

// maxOutputTokens leaves room for a long document; the request streams so a
// slow translation never hits an HTTP timeout.
const maxOutputTokens = 64000

// apiBackend translates through the Claude API with the Go SDK. The client
// reads ANTHROPIC_API_KEY (and ANTHROPIC_BASE_URL) from the environment.
type apiBackend struct {
	client anthropic.Client
}

func newAPIBackend() *apiBackend {
	return &apiBackend{client: anthropic.NewClient()}
}

func (b *apiBackend) translate(ctx context.Context, system, markdown string) (string, error) {
	stream := b.client.Messages.NewStreaming(ctx, anthropic.MessageNewParams{
		Model:        Model,
		MaxTokens:    maxOutputTokens,
		System:       []anthropic.TextBlockParam{{Text: system}},
		OutputConfig: anthropic.OutputConfigParam{Effort: anthropic.OutputConfigEffortHigh},
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(markdown)),
		},
	})
	message := anthropic.Message{}
	for stream.Next() {
		if err := message.Accumulate(stream.Current()); err != nil {
			return "", fmt.Errorf("claude api: accumulate stream: %w", err)
		}
	}
	if err := stream.Err(); err != nil {
		return "", fmt.Errorf("claude api: %w", err)
	}
	switch message.StopReason {
	case anthropic.StopReasonRefusal:
		return "", fmt.Errorf("claude api: translation refused (%s)", message.StopDetails.Category)
	case anthropic.StopReasonMaxTokens:
		return "", fmt.Errorf("claude api: document too long for one translation (%d output tokens)", maxOutputTokens)
	}
	var text strings.Builder
	for _, block := range message.Content {
		if textBlock, ok := block.AsAny().(anthropic.TextBlock); ok {
			text.WriteString(textBlock.Text)
		}
	}
	return text.String(), nil
}
