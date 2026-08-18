//go:build integration

package inttest

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/social-sync/digestive/internal/manifest"
	"github.com/social-sync/digestive/internal/restore"
	"github.com/social-sync/digestive/internal/target"
	"github.com/social-sync/digestive/internal/typemap"
	"github.com/social-sync/digestive/internal/value"
	"github.com/social-sync/digestive/internal/writer"
)

// syncCol is one column of a hand-built run directory: a name, a source type,
// and one cell per row.
type syncCol struct {
	name     string
	dataType string
	cells    []value.Value
}

// buildRun writes a manifest.json and one Parquet file per table into a temp
// run directory, preserving the given table order in the manifest.
func buildRun(t *testing.T, order []string, tables map[string][]syncCol) string {
	t.Helper()
	dir := t.TempDir()

	man := &manifest.Manifest{
		Version:   manifest.Version,
		RunID:     "synctest",
		CreatedAt: "2026-01-01T00:00:00Z",
		Source:    manifest.SourceInfo{Engine: "mysql"},
		Complete:  true,
	}

	for _, tbl := range order {
		cols := tables[tbl]
		names := make([]string, len(cols))
		mappings := make([]typemap.Mapping, len(cols))
		mcols := make([]manifest.Column, len(cols))
		nRows := 0
		for i, c := range cols {
			m := typemap.Map(c.dataType, false)
			names[i] = c.name
			mappings[i] = m
			mcols[i] = manifest.Column{
				Name:        c.name,
				SourceType:  c.dataType,
				Nullable:    true,
				ParquetType: m.Physical(),
				Lossless:    m.Lossless,
			}
			if len(c.cells) > nRows {
				nRows = len(c.cells)
			}
		}

		file := tbl + ".parquet"
		pw, err := writer.NewParquet(filepath.Join(dir, file), names, mappings)
		if err != nil {
			t.Fatalf("new parquet: %v", err)
		}
		for r := 0; r < nRows; r++ {
			row := make([]value.Value, len(cols))
			for i, c := range cols {
				row[i] = c.cells[r]
			}
			if err := pw.WriteRow(row); err != nil {
				t.Fatalf("write row: %v", err)
			}
		}
		if err := pw.Close(); err != nil {
			t.Fatalf("close parquet: %v", err)
		}

		man.Tables = append(man.Tables, manifest.Table{
			Name: tbl, File: file, Rows: int64(nRows), Columns: mcols,
		})
	}

	if err := man.Write(filepath.Join(dir, "manifest.json")); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	return dir
}

func countRows(ctx context.Context, t *testing.T, eng *engine, table string) int {
	t.Helper()
	var n int
	if err := eng.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM `"+table+"`").Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

// TestMySQLSyncApply drives the sync apply path (internal/target) against a real
// MySQL: it opens the destination from a DSN, applies a prepared restore inside
// one transaction, and asserts both the happy path (rows land) and the
// all-or-nothing rollback (a mid-apply failure leaves the destination untouched).
func TestMySQLSyncApply(t *testing.T) {
	ctx := context.Background()
	eng, cleanup := startMySQL(ctx, t)
	defer cleanup()

	tgt, err := target.Open(engineMySQL, eng.dsn)
	if err != nil {
		t.Fatalf("open target: %v", err)
	}
	defer tgt.Close()
	if err := tgt.Ping(ctx); err != nil {
		t.Fatalf("ping target: %v", err)
	}

	t.Run("applies rows in a transaction", func(t *testing.T) {
		mustExec(t, eng, "DROP TABLE IF EXISTS `sync_users`")
		mustExec(t, eng, "CREATE TABLE `sync_users` (id INT NOT NULL PRIMARY KEY, name VARCHAR(255))")
		defer mustExec(t, eng, "DROP TABLE IF EXISTS `sync_users`")

		dir := buildRun(t, []string{"sync_users"}, map[string][]syncCol{
			"sync_users": {
				{name: "id", dataType: "int", cells: []value.Value{value.Text("1"), value.Text("2")}},
				{name: "name", dataType: "varchar", cells: []value.Value{value.Text("Ann"), value.Null}},
			},
		})

		p, err := restore.Prepare(restore.Options{RunDir: dir, Dialect: tgt.Dialect()})
		if err != nil {
			t.Fatalf("prepare: %v", err)
		}
		if err := tgt.Apply(ctx, p, nil); err != nil {
			t.Fatalf("apply: %v", err)
		}
		if got := countRows(ctx, t, eng, "sync_users"); got != 2 {
			t.Errorf("sync_users row count = %d, want 2", got)
		}
	})

	t.Run("rolls back completely when a later table fails", func(t *testing.T) {
		mustExec(t, eng, "DROP TABLE IF EXISTS `sync_ok`")
		mustExec(t, eng, "DROP TABLE IF EXISTS `sync_missing`")
		mustExec(t, eng, "CREATE TABLE `sync_ok` (id INT NOT NULL PRIMARY KEY)")
		defer mustExec(t, eng, "DROP TABLE IF EXISTS `sync_ok`")

		// sync_missing has no destination table, so its INSERT fails after
		// sync_ok's has already run within the same transaction.
		dir := buildRun(t, []string{"sync_ok", "sync_missing"}, map[string][]syncCol{
			"sync_ok": {
				{name: "id", dataType: "int", cells: []value.Value{value.Text("1"), value.Text("2")}},
			},
			"sync_missing": {
				{name: "id", dataType: "int", cells: []value.Value{value.Text("9")}},
			},
		})

		p, err := restore.Prepare(restore.Options{RunDir: dir, Dialect: tgt.Dialect()})
		if err != nil {
			t.Fatalf("prepare: %v", err)
		}
		if err := tgt.Apply(ctx, p, nil); err == nil {
			t.Fatal("apply should fail when a table is missing at the destination")
		}
		if got := countRows(ctx, t, eng, "sync_ok"); got != 0 {
			t.Errorf("sync_ok row count = %d after failed apply, want 0 (rolled back)", got)
		}
	})
}
