package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExpand(t *testing.T) {
	lookup := func(k string) (string, bool) {
		m := map[string]string{"DSN": "user:pass@tcp(host)/db", "EMPTY": ""}
		v, ok := m[k]
		return v, ok
	}

	cases := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{"simple", "dsn: ${DSN}", "dsn: user:pass@tcp(host)/db", false},
		{"default used", "x: ${MISSING:-fallback}", "x: fallback", false},
		{"default ignored when set", "x: ${DSN:-fallback}", "x: user:pass@tcp(host)/db", false},
		{"empty var is a value", "x: ${EMPTY:-fallback}", "x: ", false},
		{"missing no default errors", "x: ${NOPE}", "", true},
		{"no refs", "plain: text", "plain: text", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := expand(tc.in, lookup)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestLoadBareAndFullTables(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	yaml := `
source:
  dsn: ${TEST_DSN}
destination:
  directory: ./out
hashing:
  key: ${TEST_KEY}
tables:
  - users
  - name: orders
    where: "created_at > '2024-01-01'"
    limit: 100
    columns:
      email:
        transform: hash_email
`
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TEST_DSN", "u:p@tcp(h)/d")
	t.Setenv("TEST_KEY", "secret")

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Source.DSN != "u:p@tcp(h)/d" {
		t.Errorf("dsn not expanded: %q", cfg.Source.DSN)
	}
	if cfg.Hashing.Key != "secret" {
		t.Errorf("key not expanded: %q", cfg.Hashing.Key)
	}
	if len(cfg.Tables) != 2 {
		t.Fatalf("want 2 tables, got %d", len(cfg.Tables))
	}
	if cfg.Tables[0].Name != "users" {
		t.Errorf("bare table not parsed: %+v", cfg.Tables[0])
	}
	if cfg.Tables[0].Columns != nil {
		t.Errorf("bare table should have no columns")
	}
	orders := cfg.Tables[1]
	if orders.Name != "orders" || orders.Limit == nil || *orders.Limit != 100 {
		t.Errorf("full table not parsed: %+v", orders)
	}
	if orders.Columns["email"].Transform != "hash_email" {
		t.Errorf("column transform not parsed: %+v", orders.Columns)
	}
}

func TestLoadSyncBlock(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	yaml := `
source:
  dsn: ${TEST_DSN}
destination:
  directory: ./out
sync:
  dsn: ${TEST_SYNC_DSN}
  type: mysql
hashing:
  key: ${TEST_KEY}
tables:
  - users
`
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TEST_DSN", "u:p@tcp(h)/d")
	t.Setenv("TEST_SYNC_DSN", "u:p@tcp(dest)/copy")
	t.Setenv("TEST_KEY", "secret")

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Sync.DSN != "u:p@tcp(dest)/copy" {
		t.Errorf("sync.dsn not expanded: %q", cfg.Sync.DSN)
	}
	if cfg.Sync.Type != "mysql" {
		t.Errorf("sync.type = %q, want mysql", cfg.Sync.Type)
	}
}

func TestLoadWithoutSyncBlockLeavesItZero(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	yaml := `
source:
  dsn: ${TEST_DSN}
destination:
  directory: ./out
hashing:
  key: ${TEST_KEY}
tables:
  - users
`
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TEST_DSN", "u:p@tcp(h)/d")
	t.Setenv("TEST_KEY", "secret")

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Sync != (SyncConfig{}) {
		t.Errorf("sync block should be zero when absent, got %+v", cfg.Sync)
	}
}
