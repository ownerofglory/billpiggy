// Package minio provides the S3-compatible object-storage adapter.
package minio

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"time"

	minio "github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"github.com/ownerofglory/billpiggy/internal/core/port/outbound"
)

// maxPresignTTL is the longest lifetime S3 signature v4 permits.
const maxPresignTTL = 7 * 24 * time.Hour

// ObjectStore stores BillPiggy objects in one MinIO bucket.
type ObjectStore struct {
	client *minio.Client
	bucket string
}

// NewObjectStore creates a bucket-backed MinIO adapter.
func NewObjectStore(endpoint, accessKey, secretKey, bucket string, useSSL bool) (*ObjectStore, error) {
	if endpoint == "" || accessKey == "" || secretKey == "" || bucket == "" {
		return nil, fmt.Errorf("MinIO endpoint, credentials, and bucket are required")
	}
	client, err := minio.New(endpoint, &minio.Options{Creds: credentials.NewStaticV4(accessKey, secretKey, ""), Secure: useSSL})
	if err != nil {
		return nil, err
	}
	return &ObjectStore{client: client, bucket: bucket}, nil
}

// Put stores one object using its supplied content type.
func (s *ObjectStore) Put(ctx context.Context, key string, body io.Reader, size int64, contentType string) error {
	_, err := s.client.PutObject(ctx, s.bucket, key, body, size, minio.PutObjectOptions{ContentType: contentType})
	return err
}

// Get streams a stored object.
func (s *ObjectStore) Get(ctx context.Context, key string) (outbound.Object, error) {
	object, err := s.client.GetObject(ctx, s.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return outbound.Object{}, translateNotFound(err)
	}
	// GetObject is lazy, so the first Stat is what actually surfaces a missing
	// key; without it a caller would only discover the error mid-stream.
	stat, err := object.Stat()
	if err != nil {
		_ = object.Close()
		return outbound.Object{}, translateNotFound(err)
	}
	return outbound.Object{
		ObjectInfo: outbound.ObjectInfo{Key: key, Size: stat.Size, ContentType: stat.ContentType, LastModified: stat.LastModified},
		Body:       object,
	}, nil
}

// Stat describes a stored object without reading its body.
func (s *ObjectStore) Stat(ctx context.Context, key string) (outbound.ObjectInfo, error) {
	stat, err := s.client.StatObject(ctx, s.bucket, key, minio.StatObjectOptions{})
	if err != nil {
		return outbound.ObjectInfo{}, translateNotFound(err)
	}
	return outbound.ObjectInfo{Key: key, Size: stat.Size, ContentType: stat.ContentType, LastModified: stat.LastModified}, nil
}

// Delete removes an object. Removing an absent key succeeds, so retention
// sweeps and repeated cleanup are safe.
func (s *ObjectStore) Delete(ctx context.Context, key string) error {
	err := s.client.RemoveObject(ctx, s.bucket, key, minio.RemoveObjectOptions{})
	if errors.Is(translateNotFound(err), outbound.ErrObjectNotFound) {
		return nil
	}
	return err
}

// PresignedGetURL returns a time-limited download URL so large files are served
// by the object store rather than proxied through the API.
func (s *ObjectStore) PresignedGetURL(ctx context.Context, key string, ttl time.Duration) (string, error) {
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	if ttl > maxPresignTTL {
		ttl = maxPresignTTL
	}
	// Confirm the object exists first: presigning is a local signing operation
	// and would happily mint a URL that resolves to a 404.
	if _, err := s.Stat(ctx, key); err != nil {
		return "", err
	}
	signed, err := s.client.PresignedGetObject(ctx, s.bucket, key, ttl, url.Values{})
	if err != nil {
		return "", err
	}
	return signed.String(), nil
}

// Ping verifies that the configured bucket is reachable.
func (s *ObjectStore) Ping(ctx context.Context) error {
	exists, err := s.client.BucketExists(ctx, s.bucket)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("MinIO bucket %q does not exist", s.bucket)
	}
	return nil
}

// translateNotFound maps MinIO's not-found responses onto the port's sentinel so
// callers do not have to know the driver's error shapes.
func translateNotFound(err error) error {
	if err == nil {
		return nil
	}
	switch minio.ToErrorResponse(err).Code {
	case "NoSuchKey", "NoSuchBucket", "NotFound":
		return outbound.ErrObjectNotFound
	}
	return err
}
