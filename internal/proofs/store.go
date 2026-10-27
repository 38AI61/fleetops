// Package proofs stores delivery proof photos in MinIO (D-07, D-25). FromEnv
// reads the MINIO_* variables itself, so internal/config stays untouched (GF-201).
package proofs

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/google/uuid"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// Bucket holds every proof photo (D-07). EnsureBucket creates it at API boot.
const Bucket = "proofs"

// region is fixed (GF-201). With a region set, bucket creation and presigning
// never ask the server for the bucket location.
const region = "us-east-1"

// defaultPresignTTL is the GET URL lifetime the README fixes (GF-202).
const defaultPresignTTL = 15 * time.Minute

// Store is the proof photo store. PresignTTL is the lifetime of a presigned GET;
// zero means 15 minutes (GF-202).
type Store struct {
	client     *minio.Client
	bucket     string
	PresignTTL time.Duration
}

// FromEnv builds a Store from MINIO_ENDPOINT (host:port), MINIO_KEY and
// MINIO_SECRET. Each value is required. The error names the variable, never the
// value. The connection is plain HTTP (no TLS in MVP).
func FromEnv(lookup func(string) string) (*Store, error) {
	endpoint := lookup("MINIO_ENDPOINT")
	key := lookup("MINIO_KEY")
	secret := lookup("MINIO_SECRET")
	for _, req := range []struct{ name, value string }{
		{"MINIO_ENDPOINT", endpoint},
		{"MINIO_KEY", key},
		{"MINIO_SECRET", secret},
	} {
		if req.value == "" {
			return nil, fmt.Errorf("proofs: %s is required", req.name)
		}
	}
	client, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(key, secret, ""),
		Secure: false,
		Region: region,
	})
	if err != nil {
		return nil, fmt.Errorf("proofs: minio client: %w", err)
	}
	return &Store{client: client, bucket: Bucket}, nil
}

// EnsureBucket creates the proofs bucket when it is missing. Calling it again,
// or racing another caller, is a no-op.
func (s *Store) EnsureBucket(ctx context.Context) error {
	ok, err := s.client.BucketExists(ctx, s.bucket)
	if err != nil {
		return fmt.Errorf("proofs: check bucket: %w", err)
	}
	if ok {
		return nil
	}
	err = s.client.MakeBucket(ctx, s.bucket, minio.MakeBucketOptions{Region: region})
	if err != nil && minio.ToErrorResponse(err).Code != "BucketAlreadyOwnedByYou" {
		return fmt.Errorf("proofs: make bucket: %w", err)
	}
	return nil
}

// Put uploads one proof photo under the D-25 key and returns that key. ext is
// the extension the handler sniffed: jpg, png or webp.
func (s *Store) Put(ctx context.Context, fleetID, jobID int64, r io.Reader, size int64, ext string) (string, error) {
	key := fmt.Sprintf("proofs/%d/%d/%s.%s", fleetID, jobID, uuid.NewString(), ext)
	_, err := s.client.PutObject(ctx, s.bucket, key, r, size, minio.PutObjectOptions{ContentType: contentType(ext)})
	if err != nil {
		return "", fmt.Errorf("proofs: put %s: %w", key, err)
	}
	return key, nil
}

// PresignGet returns a GET URL for key that lives for PresignTTL. It is signed
// locally, so no request reaches MinIO.
func (s *Store) PresignGet(ctx context.Context, key string) (string, error) {
	ttl := s.PresignTTL
	if ttl == 0 {
		ttl = defaultPresignTTL
	}
	u, err := s.client.PresignedGetObject(ctx, s.bucket, key, ttl, nil)
	if err != nil {
		return "", fmt.Errorf("proofs: presign %s: %w", key, err)
	}
	return u.String(), nil
}

// contentType maps the sniffed extension to the MIME type stored with the object.
func contentType(ext string) string {
	switch ext {
	case "jpg":
		return "image/jpeg"
	case "png":
		return "image/png"
	case "webp":
		return "image/webp"
	}
	return "application/octet-stream"
}
