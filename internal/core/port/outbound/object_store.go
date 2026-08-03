package outbound

import (
	"context"
	"io"
)

// ObjectStore persists binary objects outside PostgreSQL.
type ObjectStore interface {
	Put(context.Context, string, io.Reader, int64, string) error
	Ping(context.Context) error
}
