// Package pgxtx propagates a pgx transaction through a context so outbound
// adapters can join a caller-owned unit of work without changing their ports.
//
// Adapters resolve the handle to use with [From], which returns the ambient
// transaction when the context carries one and the supplied fallback otherwise.
// A caller opens a unit of work with [Runner.Within]; every adapter call made
// with the context handed to the callback then commits or rolls back together.
package pgxtx

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Querier is the subset of pgx used by outbound adapters. Both *pgxpool.Pool
// and pgx.Tx satisfy it, which is what lets an adapter run inside or outside a
// caller-owned transaction without branching.
type Querier interface {
	// Exec runs a statement that returns no rows.
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	// Query runs a statement that returns rows.
	Query(ctx context.Context, sql string, arguments ...any) (pgx.Rows, error)
	// QueryRow runs a statement that returns at most one row.
	QueryRow(ctx context.Context, sql string, arguments ...any) pgx.Row
}

type contextKey struct{}

// Runner opens transactions on a pool and exposes them through context. It
// satisfies the outbound UnitOfWork port structurally, so no wrapper is needed.
type Runner struct{ pool *pgxpool.Pool }

// NewRunner creates a transaction runner over an existing connection pool.
func NewRunner(pool *pgxpool.Pool) *Runner { return &Runner{pool: pool} }

// Within runs fn inside a transaction, committing when fn returns nil and
// rolling back otherwise. A nested Within joins the enclosing transaction
// rather than opening a second one, so an inner failure aborts the whole unit.
func (r *Runner) Within(ctx context.Context, fn func(ctx context.Context) error) error {
	if r == nil || r.pool == nil {
		return errors.New("postgres pool is required")
	}
	if _, nested := ctx.Value(contextKey{}).(pgx.Tx); nested {
		return fn(ctx)
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := fn(WithTx(ctx, tx)); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}

// Pool returns the underlying connection pool. Adapters that must open their
// own transaction, such as the outbox store driving a projector, use it to
// bypass the ambient unit of work deliberately.
func (r *Runner) Pool() *pgxpool.Pool {
	if r == nil {
		return nil
	}
	return r.pool
}

// WithTx returns a context carrying tx. It is exported for adapters that own
// their transaction and need to expose it to ports they call.
func WithTx(ctx context.Context, tx pgx.Tx) context.Context {
	return context.WithValue(ctx, contextKey{}, tx)
}

// From returns the transaction carried by ctx, or fallback when there is none.
// Adapters call From(ctx, r.pool) in place of using their pool directly.
func From(ctx context.Context, fallback Querier) Querier {
	if tx, ok := ctx.Value(contextKey{}).(pgx.Tx); ok {
		return tx
	}
	return fallback
}

// InTx reports whether ctx already carries a transaction.
func InTx(ctx context.Context) bool {
	_, ok := ctx.Value(contextKey{}).(pgx.Tx)
	return ok
}

// Atomic runs fn against the ambient transaction when ctx carries one, and
// otherwise opens a transaction of its own for the duration.
//
// Adapters whose methods span several statements use it so they stay atomic on
// their own while still joining a larger caller-owned unit of work rather than
// committing independently inside it.
func Atomic(ctx context.Context, pool *pgxpool.Pool, fn func(ctx context.Context, querier Querier) error) error {
	if tx, ok := ctx.Value(contextKey{}).(pgx.Tx); ok {
		return fn(ctx, tx)
	}
	if pool == nil {
		return errors.New("postgres pool is required")
	}
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := fn(WithTx(ctx, tx), tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}
