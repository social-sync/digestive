package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestComplianceValidate(t *testing.T) {
	cases := []struct {
		name    string
		cfg     ComplianceConfig
		wantErr string // substring; "" means no error
	}{
		{
			name:    "neither directory nor s3",
			cfg:     ComplianceConfig{},
			wantErr: "exactly one",
		},
		{
			name: "both directory and s3",
			cfg: ComplianceConfig{Audit: AuditConfig{
				Directory: "./audit",
				S3:        &S3Config{Endpoint: "e", Bucket: "b", AccessKeyID: "a", SecretAccessKey: "s"},
			}},
			wantErr: "both",
		},
		{
			name: "directory only ok",
			cfg:  ComplianceConfig{Audit: AuditConfig{Directory: "./audit"}},
		},
		{
			name: "s3 complete ok",
			cfg: ComplianceConfig{Audit: AuditConfig{S3: &S3Config{
				Endpoint: "e", Bucket: "b", AccessKeyID: "a", SecretAccessKey: "s",
			}}},
		},
		{
			name:    "s3 missing fields",
			cfg:     ComplianceConfig{Audit: AuditConfig{S3: &S3Config{Endpoint: "e"}}},
			wantErr: "bucket, access_key_id, secret_access_key",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.cfg.Validate()
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error %q does not contain %q", err.Error(), tc.wantErr)
			}
		})
	}
}

// A malformed compliance block must fail Load, not silently disable the gate.
func TestLoadRejectsInvalidCompliance(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	yaml := `
source:
  dsn: u:p@tcp(h)/d
destination:
  directory: ./out
hashing:
  key: secret
compliance:
  audit: {}
tables:
  - users
`
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("expected Load to reject an empty compliance.audit block")
	}
}

func TestLoadAcceptsValidCompliance(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	yaml := `
source:
  dsn: u:p@tcp(h)/d
destination:
  directory: ./out
hashing:
  key: secret
compliance:
  audit:
    s3:
      endpoint: ${AUDIT_ENDPOINT}
      bucket: audits
      access_key_id: ${AUDIT_KEY}
      secret_access_key: ${AUDIT_SECRET}
      path_style: true
tables:
  - users
`
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AUDIT_ENDPOINT", "minio.local:9000")
	t.Setenv("AUDIT_KEY", "AK")
	t.Setenv("AUDIT_SECRET", "SK")

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Compliance == nil {
		t.Fatal("compliance block not parsed")
	}
	s3 := cfg.Compliance.Audit.S3
	if s3 == nil {
		t.Fatal("s3 block not parsed")
	}
	if s3.Endpoint != "minio.local:9000" || s3.AccessKeyID != "AK" || s3.SecretAccessKey != "SK" {
		t.Errorf("s3 fields not expanded: %+v", s3)
	}
	if !s3.PathStyle {
		t.Error("path_style not parsed")
	}
}
