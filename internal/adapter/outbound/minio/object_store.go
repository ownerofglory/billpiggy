// Package minio provides the S3-compatible object-storage adapter.
package minio

import (
	"context"
	"fmt"
	"io"

	minio "github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

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
