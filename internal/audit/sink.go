package audit

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"github.com/social-sync/digestive/internal/config"
)

// Sink stores one audit record under the given object name.
type Sink interface {
	Write(ctx context.Context, name string, data []byte) error
}

// NewSink builds the Sink described by cfg. The caller must have validated cfg
// (exactly one of Directory or S3); NewSink assumes that invariant holds.
func NewSink(cfg config.AuditConfig) (Sink, error) {
	if cfg.S3 != nil {
		return newS3Sink(cfg.S3)
	}
	return &dirSink{dir: cfg.Directory}, nil
}

// dirSink writes audit records as files in a local directory.
type dirSink struct {
	dir string
}

func (s *dirSink) Write(_ context.Context, name string, data []byte) error {
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return fmt.Errorf("audit: create directory %q: %w", s.dir, err)
	}
	path := filepath.Join(s.dir, name)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("audit: write %q: %w", path, err)
	}
	return nil
}

// s3Sink writes audit records to an S3-compatible bucket via minio-go.
type s3Sink struct {
	client *minio.Client
	bucket string
	prefix string
}

func newS3Sink(cfg *config.S3Config) (Sink, error) {
	region := cfg.Region
	if region == "" {
		region = "us-east-1"
	}
	opts := &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.AccessKeyID, cfg.SecretAccessKey, ""),
		Secure: cfg.UseSSL,
		Region: region,
	}
	if cfg.PathStyle {
		opts.BucketLookup = minio.BucketLookupPath
	}
	client, err := minio.New(stripScheme(cfg.Endpoint), opts)
	if err != nil {
		return nil, fmt.Errorf("audit: init s3 client: %w", err)
	}
	return &s3Sink{
		client: client,
		bucket: cfg.Bucket,
		prefix: cfg.Prefix,
	}, nil
}

func (s *s3Sink) Write(ctx context.Context, name string, data []byte) error {
	key := name
	if s.prefix != "" {
		key = strings.TrimSuffix(s.prefix, "/") + "/" + name
	}
	_, err := s.client.PutObject(ctx, s.bucket, key, bytes.NewReader(data), int64(len(data)),
		minio.PutObjectOptions{ContentType: "application/json"})
	if err != nil {
		return fmt.Errorf("audit: upload to s3 bucket %q key %q: %w", s.bucket, key, err)
	}
	return nil
}

// stripScheme removes a leading http(s):// from an endpoint. minio.New expects a
// bare host[:port]; UseSSL, not the scheme, controls TLS. Tolerating a scheme
// here avoids a common config footgun.
func stripScheme(endpoint string) string {
	if i := strings.Index(endpoint, "://"); i >= 0 {
		return endpoint[i+len("://"):]
	}
	return endpoint
}
