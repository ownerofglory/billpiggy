package outbound

import (
	"context"

	"github.com/ownerofglory/billpiggy/internal/core/domain"
)

// AIProvider generates completions from an external model provider.
//
// The port is deliberately narrow: tools, structured output and model choice
// all travel inside domain.CompletionRequest rather than multiplying methods,
// so adding a capability does not change the interface every adapter and fake
// has to implement.
type AIProvider interface {
	// Complete returns a finished response, including any tools the model asked
	// to run.
	Complete(ctx context.Context, request domain.CompletionRequest) (domain.Completion, error)
	// Stream returns a channel of incremental updates. The channel is closed
	// when the stream ends, and the final chunk carries either Done or Err.
	//
	// Callers must either drain the channel or cancel ctx; abandoning it without
	// cancelling leaks the goroutine feeding it.
	Stream(ctx context.Context, request domain.CompletionRequest) (<-chan domain.CompletionChunk, error)
}
