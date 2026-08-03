package memory

import (
	"context"
	"io"
)

// ObjectStore is a discard-only local object store for development and tests.
type ObjectStore struct{}

// NewObjectStore creates a local object-store adapter.
func NewObjectStore() *ObjectStore { return &ObjectStore{} }

// Put consumes an object without persisting it.
func (*ObjectStore) Put(_ context.Context, _ string, body io.Reader, _ int64, _ string) error {
	_, err := io.Copy(io.Discard, body)
	return err
}

// Ping always succeeds for the local adapter.
func (*ObjectStore) Ping(context.Context) error { return nil }
