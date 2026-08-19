package config

import (
	"strings"
	"testing"
)

func TestRedacted(t *testing.T) {
	limit := 100
	cfg := &Config{
		Source:      SourceConfig{DSN: "user:secretpass@tcp(host)/db"},
		Destination: DestinationConfig{Directory: "./exports"},
		Sync:        SyncConfig{DSN: "user:othersecret@tcp(dest)/copy", Type: "mysql"},
		Hashing:     HashingConfig{Key: "top-secret-hmac"},
		Compliance: &ComplianceConfig{Audit: AuditConfig{S3: &S3Config{
			Endpoint:        "minio.local:9000",
			Bucket:          "audits",
			AccessKeyID:     "AKIAEXAMPLE",
			SecretAccessKey: "verysecret",
			PathStyle:       true,
		}}},
		Tables: []TableConfig{
			{Name: "users", Where: "id > 0", Limit: &limit, Columns: map[string]ColumnConfig{
				"email": {Transform: "hash_email"},
			}},
		},
	}

	tree, err := cfg.Redacted()
	if err != nil {
		t.Fatal(err)
	}

	// Whole-string secret fields are redacted.
	if got := dig(t, tree, "source", "dsn"); got != RedactedMarker {
		t.Errorf("source.dsn = %q, want redacted", got)
	}
	if got := dig(t, tree, "sync", "dsn"); got != RedactedMarker {
		t.Errorf("sync.dsn = %q, want redacted", got)
	}
	if got := dig(t, tree, "hashing", "key"); got != RedactedMarker {
		t.Errorf("hashing.key = %q, want redacted", got)
	}
	if got := dig(t, tree, "compliance", "audit", "s3", "access_key_id"); got != RedactedMarker {
		t.Errorf("s3.access_key_id = %q, want redacted", got)
	}
	if got := dig(t, tree, "compliance", "audit", "s3", "secret_access_key"); got != RedactedMarker {
		t.Errorf("s3.secret_access_key = %q, want redacted", got)
	}

	// Non-secret fields survive so auditors keep the useful context.
	if got := dig(t, tree, "destination", "directory"); got != "./exports" {
		t.Errorf("destination.directory = %q, want ./exports", got)
	}
	if got := dig(t, tree, "sync", "type"); got != "mysql" {
		t.Errorf("sync.type = %q, want mysql", got)
	}
	if got := dig(t, tree, "compliance", "audit", "s3", "bucket"); got != "audits" {
		t.Errorf("s3.bucket = %q, want audits", got)
	}

	// The receiver is never mutated.
	if cfg.Source.DSN != "user:secretpass@tcp(host)/db" {
		t.Errorf("Redacted mutated the original config: %q", cfg.Source.DSN)
	}
	if cfg.Hashing.Key != "top-secret-hmac" {
		t.Errorf("Redacted mutated the original config: %q", cfg.Hashing.Key)
	}

	// No secret value leaks anywhere in the serialised tree.
	for _, secret := range []string{"secretpass", "othersecret", "top-secret-hmac", "verysecret"} {
		if strings.Contains(stringify(tree), secret) {
			t.Errorf("secret %q leaked into redacted tree", secret)
		}
	}
}

// An empty secret field stays empty rather than showing the redaction marker.
func TestRedactedLeavesEmptySecretsEmpty(t *testing.T) {
	cfg := &Config{Destination: DestinationConfig{Directory: "./out"}}
	tree, err := cfg.Redacted()
	if err != nil {
		t.Fatal(err)
	}
	if got := dig(t, tree, "source", "dsn"); got != "" {
		t.Errorf("empty source.dsn = %q, want empty", got)
	}
}

func dig(t *testing.T, tree map[string]any, path ...string) string {
	t.Helper()
	var cur any = tree
	for _, key := range path {
		m, ok := cur.(map[string]any)
		if !ok {
			t.Fatalf("path %v: %q is not a map (got %T)", path, key, cur)
		}
		cur = m[key]
	}
	if cur == nil {
		return ""
	}
	s, ok := cur.(string)
	if !ok {
		t.Fatalf("path %v is not a string (got %T)", path, cur)
	}
	return s
}

func stringify(v any) string {
	var b strings.Builder
	walk(&b, v)
	return b.String()
}

func walk(b *strings.Builder, v any) {
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			b.WriteString(k)
			b.WriteByte(' ')
			walk(b, val)
		}
	case []any:
		for _, val := range t {
			walk(b, val)
		}
	default:
		b.WriteString(strings.TrimSpace(strings.ToLower(sprint(t))))
		b.WriteByte(' ')
	}
}

func sprint(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}
