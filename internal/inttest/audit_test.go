//go:build integration

package inttest

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/social-sync/digestive/internal/audit"
	"github.com/social-sync/digestive/internal/config"
	"github.com/social-sync/digestive/internal/manifest"
)

const (
	minioUser   = "minioadmin"
	minioSecret = "minioadmin"
	auditBucket = "audits"
)

// minioImage is overridable so a developer can pin a version.
func minioImage() string {
	if v := os.Getenv("DIGESTIVE_MINIO_IMAGE"); v != "" {
		return v
	}
	return "minio/minio:latest"
}

// startMinIO brings up a MinIO server, creates the audit bucket, and returns the
// bare host:port endpoint plus a cleanup func.
func startMinIO(ctx context.Context, t *testing.T) (endpoint string, cleanup func()) {
	t.Helper()

	req := testcontainers.ContainerRequest{
		Image:        minioImage(),
		ExposedPorts: []string{"9000/tcp"},
		Cmd:          []string{"server", "/data"},
		Env: map[string]string{
			"MINIO_ROOT_USER":     minioUser,
			"MINIO_ROOT_PASSWORD": minioSecret,
		},
		WaitingFor: wait.ForHTTP("/minio/health/ready").
			WithPort("9000/tcp").
			WithStartupTimeout(2 * time.Minute),
	}
	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		t.Fatalf("start minio container: %v", err)
	}

	host, err := container.Host(ctx)
	if err != nil {
		_ = container.Terminate(ctx)
		t.Fatalf("minio host: %v", err)
	}
	mapped, err := container.MappedPort(ctx, "9000/tcp")
	if err != nil {
		_ = container.Terminate(ctx)
		t.Fatalf("minio mapped port: %v", err)
	}
	endpoint = fmt.Sprintf("%s:%s", host, mapped.Port())

	admin, err := minio.New(endpoint, &minio.Options{
		Creds:        credentials.NewStaticV4(minioUser, minioSecret, ""),
		Secure:       false,
		Region:       "us-east-1",
		BucketLookup: minio.BucketLookupPath,
	})
	if err != nil {
		_ = container.Terminate(ctx)
		t.Fatalf("minio admin client: %v", err)
	}
	if err := admin.MakeBucket(ctx, auditBucket, minio.MakeBucketOptions{}); err != nil {
		_ = container.Terminate(ctx)
		t.Fatalf("create bucket: %v", err)
	}

	return endpoint, func() { _ = container.Terminate(context.Background()) }
}

// TestAuditS3RoundTrip proves the full S3 audit path: an audit.Sink built from a
// compliance config uploads a record to a real S3-compatible server (MinIO), and
// the object lands under the configured prefix with secrets redacted.
func TestAuditS3RoundTrip(t *testing.T) {
	ctx := context.Background()
	endpoint, cleanup := startMinIO(ctx, t)
	defer cleanup()

	cfg := &config.Config{
		Source:  config.SourceConfig{DSN: "user:secretpass@tcp(host)/db"},
		Hashing: config.HashingConfig{Key: "top-secret-hmac"},
		Compliance: &config.ComplianceConfig{Audit: config.AuditConfig{S3: &config.S3Config{
			Endpoint:        endpoint,
			Bucket:          auditBucket,
			Prefix:          "exports/",
			Region:          "us-east-1",
			AccessKeyID:     minioUser,
			SecretAccessKey: minioSecret,
			UseSSL:          false,
			PathStyle:       true,
		}}},
	}
	if err := cfg.Compliance.Validate(); err != nil {
		t.Fatal(err)
	}

	doc, err := audit.Build(audit.BuildInput{
		Action:    "export",
		Requester: audit.Requester{Name: "Jane Auditor", Email: "jane@example.com"},
		Config:    cfg,
		Manifest: &manifest.Manifest{
			Version:   manifest.Version,
			RunID:     "2026-08-19T14-30-00Z",
			CreatedAt: "2026-08-19T14:30:00Z",
			Complete:  true,
			Tables:    []manifest.Table{{Name: "users", File: "users.parquet", Rows: 42}},
		},
		RunName:      "2026-08-19T14-30-00Z",
		RunDirectory: "/data/exports/2026-08-19T14-30-00Z",
		ToolVersion:  "test",
		Now:          time.Date(2026, 8, 19, 14, 31, 12, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}

	sink, err := audit.NewSink(cfg.Compliance.Audit)
	if err != nil {
		t.Fatal(err)
	}
	if err := audit.Write(ctx, sink, doc); err != nil {
		t.Fatalf("write audit to s3: %v", err)
	}

	// List objects under the prefix and verify exactly one landed.
	client, err := minio.New(endpoint, &minio.Options{
		Creds:        credentials.NewStaticV4(minioUser, minioSecret, ""),
		Secure:       false,
		Region:       "us-east-1",
		BucketLookup: minio.BucketLookupPath,
	})
	if err != nil {
		t.Fatal(err)
	}

	var keys []string
	for obj := range client.ListObjects(ctx, auditBucket, minio.ListObjectsOptions{Prefix: "exports/", Recursive: true}) {
		if obj.Err != nil {
			t.Fatalf("list objects: %v", obj.Err)
		}
		keys = append(keys, obj.Key)
	}
	if len(keys) != 1 {
		t.Fatalf("want 1 audit object under prefix, got %d: %v", len(keys), keys)
	}
	if !strings.HasPrefix(keys[0], "exports/2026-08-19T14-30-00Z-") || !strings.HasSuffix(keys[0], ".json") {
		t.Errorf("unexpected object key %q", keys[0])
	}

	// Fetch it back and verify content + redaction.
	rc, err := client.GetObject(ctx, auditBucket, keys[0], minio.GetObjectOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	data, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal(err)
	}

	var round audit.Document
	if err := json.Unmarshal(data, &round); err != nil {
		t.Fatalf("stored object is not valid JSON: %v", err)
	}
	if round.Action != "export" || round.RowCounts["users"] != 42 {
		t.Errorf("round-tripped doc wrong: %+v", round)
	}
	if round.Requester.Email != "jane@example.com" {
		t.Errorf("requester = %+v", round.Requester)
	}
	if strings.Contains(string(data), "secretpass") || strings.Contains(string(data), "top-secret-hmac") {
		t.Error("secret leaked into audit object")
	}
}
