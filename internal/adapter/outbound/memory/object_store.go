package memory

import (
	"bytes"
	"context"
	"io"
	"sync"
	"time"

	"github.com/ownerofglory/billpiggy/internal/core/port/outbound"
)

// storedObject is one retained object and its metadata.
type storedObject struct {
	data         []byte
	contentType  string
	lastModified time.Time
}

// ObjectStore is an in-memory object store for development and tests.
//
// It retains bodies rather than discarding them, so upload, download and
// retention flows can be exercised end to end without MinIO.
type ObjectStore struct {
	mu      sync.RWMutex
	objects map[string]storedObject
	now     func() time.Time
}

// NewObjectStore creates an empty local object-store adapter.
func NewObjectStore() *ObjectStore {
	return &ObjectStore{objects: map[string]storedObject{}, now: time.Now}
}

// Put stores an object, replacing any existing object at the same key.
func (s *ObjectStore) Put(_ context.Context, key string, body io.Reader, _ int64, contentType string) error {
	data, err := io.ReadAll(body)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.objects[key] = storedObject{data: data, contentType: contentType, lastModified: s.now().UTC()}
	return nil
}

// Get streams a stored object.
func (s *ObjectStore) Get(_ context.Context, key string) (outbound.Object, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	object, ok := s.objects[key]
	if !ok {
		return outbound.Object{}, outbound.ErrObjectNotFound
	}
	return outbound.Object{
		ObjectInfo: outbound.ObjectInfo{Key: key, Size: int64(len(object.data)), ContentType: object.contentType, LastModified: object.lastModified},
		Body:       io.NopCloser(bytes.NewReader(append([]byte(nil), object.data...))),
	}, nil
}

// Stat describes a stored object without reading its body.
func (s *ObjectStore) Stat(_ context.Context, key string) (outbound.ObjectInfo, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	object, ok := s.objects[key]
	if !ok {
		return outbound.ObjectInfo{}, outbound.ErrObjectNotFound
	}
	return outbound.ObjectInfo{Key: key, Size: int64(len(object.data)), ContentType: object.contentType, LastModified: object.lastModified}, nil
}

// Delete removes an object. Removing an absent key succeeds.
func (s *ObjectStore) Delete(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.objects, key)
	return nil
}

// PresignedGetURL reports that this store cannot presign, so callers stream the
// object through the API instead.
func (s *ObjectStore) PresignedGetURL(context.Context, string, time.Duration) (string, error) {
	return "", outbound.ErrPresignUnsupported
}

// Ping always succeeds for the local adapter.
func (s *ObjectStore) Ping(context.Context) error { return nil }

// Keys returns every stored key, for test assertions.
func (s *ObjectStore) Keys() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	keys := make([]string, 0, len(s.objects))
	for key := range s.objects {
		keys = append(keys, key)
	}
	return keys
}
