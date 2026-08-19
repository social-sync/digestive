package audit

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/social-sync/digestive/internal/config"
	"github.com/social-sync/digestive/internal/manifest"
)

func sampleInput() BuildInput {
	return BuildInput{
		Action:    "export",
		Requester: Requester{Name: "Jane Auditor", Email: "jane@example.com"},
		Config: &config.Config{
			Source:  config.SourceConfig{DSN: "user:secretpass@tcp(host)/db"},
			Hashing: config.HashingConfig{Key: "top-secret"},
		},
		Manifest: &manifest.Manifest{
			Version:   manifest.Version,
			RunID:     "2026-08-19T14-30-00Z",
			CreatedAt: "2026-08-19T14:30:00Z",
			Complete:  true,
			Tables: []manifest.Table{
				{Name: "users", File: "users.parquet", Rows: 10432},
				{Name: "orders", File: "orders.parquet", Rows: 88123},
			},
		},
		RunName:      "2026-08-19T14-30-00Z",
		RunDirectory: "/data/exports/2026-08-19T14-30-00Z",
		ToolVersion:  "1.4.2",
		Now:          time.Date(2026, 8, 19, 14, 31, 12, 0, time.UTC),
	}
}

func TestBuild(t *testing.T) {
	doc, err := Build(sampleInput())
	if err != nil {
		t.Fatal(err)
	}

	if doc.AuditVersion != Version {
		t.Errorf("audit_version = %d, want %d", doc.AuditVersion, Version)
	}
	if doc.Action != "export" {
		t.Errorf("action = %q", doc.Action)
	}
	if doc.Requester.Email != "jane@example.com" {
		t.Errorf("requester = %+v", doc.Requester)
	}
	if doc.Hostname == "" {
		t.Error("hostname empty")
	}
	if doc.Timestamps.ExportStartedAt != "2026-08-19T14:30:00Z" {
		t.Errorf("export_started_at = %q", doc.Timestamps.ExportStartedAt)
	}
	if doc.Timestamps.AuditWrittenAt != "2026-08-19T14:31:12Z" {
		t.Errorf("audit_written_at = %q", doc.Timestamps.AuditWrittenAt)
	}
	if doc.RowCounts["users"] != 10432 || doc.RowCounts["orders"] != 88123 {
		t.Errorf("row_counts = %+v", doc.RowCounts)
	}
	if doc.ToolVersion != "1.4.2" {
		t.Errorf("tool_version = %q", doc.ToolVersion)
	}

	// Config is redacted.
	if doc.Config["source"].(map[string]any)["dsn"] != config.RedactedMarker {
		t.Errorf("config.source.dsn not redacted: %+v", doc.Config["source"])
	}
}

func TestBuildRequiresManifest(t *testing.T) {
	in := sampleInput()
	in.Manifest = nil
	if _, err := Build(in); err == nil {
		t.Fatal("expected error when manifest is nil")
	}
}

func TestWriteDirSink(t *testing.T) {
	dir := t.TempDir()
	doc, err := Build(sampleInput())
	if err != nil {
		t.Fatal(err)
	}
	sink := &dirSink{dir: filepath.Join(dir, "audit")}
	if err := Write(context.Background(), sink, doc); err != nil {
		t.Fatal(err)
	}

	entries, err := os.ReadDir(filepath.Join(dir, "audit"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("want 1 audit file, got %d", len(entries))
	}
	name := entries[0].Name()
	if !strings.HasSuffix(name, ".json") || !strings.HasPrefix(name, "2026-08-19T14-30-00Z-") {
		t.Errorf("unexpected audit filename %q", name)
	}

	data, err := os.ReadFile(filepath.Join(dir, "audit", name))
	if err != nil {
		t.Fatal(err)
	}
	var round Document
	if err := json.Unmarshal(data, &round); err != nil {
		t.Fatalf("audit file is not valid JSON: %v", err)
	}
	if round.Action != "export" || round.RowCounts["users"] != 10432 {
		t.Errorf("round-tripped doc wrong: %+v", round)
	}
	// No secret leaks to disk.
	if strings.Contains(string(data), "secretpass") || strings.Contains(string(data), "top-secret") {
		t.Error("secret leaked into audit file")
	}
}

func TestObjectNameUnique(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		name, err := objectName("run", "host")
		if err != nil {
			t.Fatal(err)
		}
		if seen[name] {
			t.Fatalf("duplicate object name %q", name)
		}
		seen[name] = true
	}
}

func TestObjectNameSanitizes(t *testing.T) {
	name, err := objectName("run/../weird name", "host:1")
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"/", " ", ":"} {
		if strings.Contains(name, bad) {
			t.Errorf("object name %q contains unsafe %q", name, bad)
		}
	}
}
