package outbound

import (
	"context"
	"errors"
	"io"
	"time"
)

// ErrObjectNotFound reports that a key has no stored object.
var ErrObjectNotFound = errors.New("object not found")

// ErrPresignUnsupported reports that a store cannot mint presigned URLs.
// Callers fall back to streaming the object through the API instead.
var ErrPresignUnsupported = errors.New("object store cannot presign URLs")

// ObjectInfo describes a stored object without reading its body.
type ObjectInfo struct {
	// Key is the object's location within the bucket.
	Key string
	// Size is the object's length in bytes.
	Size int64
	// ContentType is the media type recorded at upload time.
	ContentType string
	// LastModified is when the object was last written.
	LastModified time.Time
}

// Object is a stored object together with its body. Callers must close Body.
type Object struct {
	// ObjectInfo describes the object.
	ObjectInfo
	// Body streams the object's content and must be closed by the caller.
	Body io.ReadCloser
}

// ObjectStore persists binary objects outside PostgreSQL.
type ObjectStore interface {
	// Put stores an object, replacing any existing object at the same key.
	Put(ctx context.Context, key string, body io.Reader, size int64, contentType string) error
	// Get streams a stored object, returning ErrObjectNotFound when the key is
	// absent.
	Get(ctx context.Context, key string) (Object, error)
	// Stat describes a stored object without reading its body.
	Stat(ctx context.Context, key string) (ObjectInfo, error)
	// Delete removes an object. Deleting an absent key is not an error, so
	// cleanup is safe to repeat.
	Delete(ctx context.Context, key string) error
	// PresignedGetURL returns a time-limited download URL, or
	// ErrPresignUnsupported when the store cannot mint one.
	PresignedGetURL(ctx context.Context, key string, ttl time.Duration) (string, error)
	// Ping reports whether the store is reachable and correctly configured.
	Ping(ctx context.Context) error
}
