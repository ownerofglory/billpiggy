package outbound

import "context"

// UnitOfWork runs several outbound-port calls as one all-or-nothing change.
//
// Adapters bound to the same UnitOfWork observe the transaction through the
// context passed to the callback, so every existing port keeps its signature.
// Services use it to commit a domain event and the read-model row it implies
// together; a crash between the two can then never desynchronise them.
type UnitOfWork interface {
	// Within runs fn inside a transaction, committing when fn returns nil and
	// rolling back otherwise. Nested calls join the enclosing transaction.
	Within(ctx context.Context, fn func(ctx context.Context) error) error
}
