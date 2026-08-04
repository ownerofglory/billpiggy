//go:build integration

// Tests in this file exercise the MinIO adapter against a real MinIO server.
// They are behind the `integration` build tag and skip unless
// TEST_MINIO_ENDPOINT is set.
//
//	docker compose up -d minio minio-bucket
//	TEST_MINIO_ENDPOINT="localhost:9000" \
//	    go test -tags=integration ./internal/adapter/outbound/minio/...
package minio_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"testing"
	"time"

	miniogo "github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	minioadapter "github.com/ownerofglory/billpiggy/internal/adapter/outbound/minio"
	"github.com/ownerofglory/billpiggy/internal/core/port/outbound"
)

// newTestStore connects to the test MinIO server, creating its bucket if
// necessary, and skips the test if no server is configured.
func newTestStore(t *testing.T) *minioadapter.ObjectStore {
	t.Helper()
	endpoint := os.Getenv("TEST_MINIO_ENDPOINT")
	if endpoint == "" {
		t.Skip("TEST_MINIO_ENDPOINT is not set; skipping MinIO integration tests")
	}
	accessKey := envOr("TEST_MINIO_ACCESS_KEY", "billpiggy")
	secretKey := envOr("TEST_MINIO_SECRET_KEY", "billpiggy-dev-secret")
	bucket := envOr("TEST_MINIO_BUCKET", "billpiggy-test")

	ensureBucket(t, endpoint, accessKey, secretKey, bucket)

	store, err := minioadapter.NewObjectStore(endpoint, accessKey, secretKey, bucket, false)
	if err != nil {
		t.Fatalf("new object store: %v", err)
	}
	return store
}

func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

// ensureBucket creates the test bucket once; a repeat run finds it already
// present and does nothing.
func ensureBucket(t *testing.T, endpoint, accessKey, secretKey, bucket string) {
	t.Helper()
	client, err := miniogo.New(endpoint, &miniogo.Options{Creds: credentials.NewStaticV4(accessKey, secretKey, ""), Secure: false})
	if err != nil {
		t.Fatalf("create raw minio client: %v", err)
	}
	ctx := context.Background()
	exists, err := client.BucketExists(ctx, bucket)
	if err != nil {
		t.Fatalf("check bucket exists: %v", err)
	}
	if exists {
		return
	}
	if err := client.MakeBucket(ctx, bucket, miniogo.MakeBucketOptions{}); err != nil {
		t.Fatalf("make bucket: %v", err)
	}
}

// uniqueKey returns a key scoped to one test, so parallel tests never collide.
func uniqueKey(t *testing.T, name string) string {
	t.Helper()
	return "test/" + t.Name() + "/" + name
}

func TestObjectStorePutGetRoundTrip(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	key := uniqueKey(t, "receipt.jpg")
	content := []byte("fake jpeg bytes")

	if err := store.Put(ctx, key, bytes.NewReader(content), int64(len(content)), "image/jpeg"); err != nil {
		t.Fatalf("put: %v", err)
	}
	object, err := store.Get(ctx, key)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer object.Body.Close()
	body, err := io.ReadAll(object.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if !bytes.Equal(body, content) {
		t.Fatalf("body = %q, want %q", body, content)
	}
	if object.ContentType != "image/jpeg" {
		t.Fatalf("content type = %q, want image/jpeg", object.ContentType)
	}
	if object.Size != int64(len(content)) {
		t.Fatalf("size = %d, want %d", object.Size, len(content))
	}
}

func TestObjectStoreStatDescribesWithoutReadingBody(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	key := uniqueKey(t, "stat.txt")
	content := []byte("stat me")

	if err := store.Put(ctx, key, bytes.NewReader(content), int64(len(content)), "text/plain"); err != nil {
		t.Fatalf("put: %v", err)
	}
	info, err := store.Stat(ctx, key)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Size != int64(len(content)) || info.ContentType != "text/plain" {
		t.Fatalf("info = %#v", info)
	}
}

func TestObjectStoreGetMissingKeyReturnsErrObjectNotFound(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	if _, err := store.Get(ctx, uniqueKey(t, "does-not-exist")); !errors.Is(err, outbound.ErrObjectNotFound) {
		t.Fatalf("get missing key: err = %v, want ErrObjectNotFound", err)
	}
	if _, err := store.Stat(ctx, uniqueKey(t, "does-not-exist")); !errors.Is(err, outbound.ErrObjectNotFound) {
		t.Fatalf("stat missing key: err = %v, want ErrObjectNotFound", err)
	}
}

func TestObjectStoreDeleteIsIdempotent(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	key := uniqueKey(t, "to-delete.txt")
	if err := store.Put(ctx, key, bytes.NewReader([]byte("bye")), 3, "text/plain"); err != nil {
		t.Fatalf("put: %v", err)
	}
	if err := store.Delete(ctx, key); err != nil {
		t.Fatalf("first delete: %v", err)
	}
	if err := store.Delete(ctx, key); err != nil {
		t.Fatalf("second delete (already gone) should be a no-op: %v", err)
	}
	if _, err := store.Get(ctx, key); !errors.Is(err, outbound.ErrObjectNotFound) {
		t.Fatalf("get deleted key: err = %v, want ErrObjectNotFound", err)
	}
}

func TestObjectStorePresignedGetURLResolvesToTheStoredContent(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	key := uniqueKey(t, "presigned.txt")
	content := []byte("presigned content")
	if err := store.Put(ctx, key, bytes.NewReader(content), int64(len(content)), "text/plain"); err != nil {
		t.Fatalf("put: %v", err)
	}

	url, err := store.PresignedGetURL(ctx, key, 5*time.Minute)
	if err != nil {
		t.Fatalf("presigned url: %v", err)
	}
	response, err := http.Get(url)
	if err != nil {
		t.Fatalf("fetch presigned url: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("presigned url status = %d, want 200", response.StatusCode)
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read presigned response: %v", err)
	}
	if !bytes.Equal(body, content) {
		t.Fatalf("presigned body = %q, want %q", body, content)
	}
}

func TestObjectStorePresignedGetURLForMissingKeyFails(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	if _, err := store.PresignedGetURL(ctx, uniqueKey(t, "does-not-exist"), 5*time.Minute); !errors.Is(err, outbound.ErrObjectNotFound) {
		t.Fatalf("presigned url for missing key: err = %v, want ErrObjectNotFound", err)
	}
}

func TestObjectStorePing(t *testing.T) {
	store := newTestStore(t)
	if err := store.Ping(context.Background()); err != nil {
		t.Fatalf("ping: %v", err)
	}
}
