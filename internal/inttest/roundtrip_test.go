//go:build integration

package inttest

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/social-sync/digestive/internal/manifest"
	"github.com/social-sync/digestive/internal/restore"
	"github.com/social-sync/digestive/internal/source"
	"github.com/social-sync/digestive/internal/typemap"
	"github.com/social-sync/digestive/internal/value"
	"github.com/social-sync/digestive/internal/writer"
)

// updateGolden is set by `-update` (see Makefile: `make test-integration UPDATE=1`).
var updateGolden bool

func init() {
	flag.BoolVar(&updateGolden, "update", false, "overwrite golden files with observed output")
}

// TestMySQLRoundTrip and TestSingleStoreRoundTrip each stand up one container,
// then run every fixture type through it as a subtest. One container per engine
// (reused across all types) keeps the suite to two slow starts, not dozens.

func TestMySQLRoundTrip(t *testing.T) {
	ctx := context.Background()
	eng, cleanup := startMySQL(ctx, t)
	defer cleanup()
	runAllFixtures(ctx, t, eng)
}

func TestSingleStoreRoundTrip(t *testing.T) {
	ctx := context.Background()
	eng, cleanup := startSingleStore(ctx, t) // t.Skip inside when unconfigured
	defer cleanup()
	runAllFixtures(ctx, t, eng)
}

func runAllFixtures(ctx context.Context, t *testing.T, eng *engine) {
	fixtures, err := loadFixtures()
	if err != nil {
		t.Fatalf("load fixtures: %v", err)
	}
	src, err := source.OpenSingleStore(eng.dsn) // MySQL-wire; works for both engines
	if err != nil {
		t.Fatalf("open source: %v", err)
	}
	defer src.Close()

	for _, f := range fixtures {
		t.Run(f.Laravel, func(t *testing.T) {
			runFixture(ctx, t, eng, src, f)
		})
	}
}

// runFixture drives one Laravel column type through the whole pipeline on one
// engine: create → insert → export → restore → reload → read back → assert
// fidelity → compare golden.
func runFixture(ctx context.Context, t *testing.T, eng *engine, src source.Source, f fixtureType) {
	ddl, skip := f.ddlFor(eng.name)
	if skip != "" {
		t.Skip(skip)
	}
	table := "tc_" + sanitizeIdent(f.Laravel)

	// Fresh table: rn is a deterministic sort/primary key, val is the type under
	// test. Every row carries exactly one value of val.
	mustExec(t, eng, "DROP TABLE IF EXISTS `"+table+"`")
	mustExec(t, eng, fmt.Sprintf("CREATE TABLE `%s` (rn INT NOT NULL PRIMARY KEY, val %s)", table, ddl))
	defer mustExec(t, eng, "DROP TABLE IF EXISTS `"+table+"`")

	for i, expr := range f.Values {
		stmt := fmt.Sprintf("INSERT INTO `%s` (rn, val) VALUES (%d, %s)", table, i, expr)
		if _, err := eng.db.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("insert row %d (%s): %v", i, expr, err)
		}
	}

	original := readVal(ctx, t, src, table)

	// Export (source → typemap → Parquet + manifest), then restore to SQL.
	runDir, valMeta := exportTable(ctx, t, src, eng, table)
	script := restoreToSQL(t, runDir, eng.dialect)

	// Reload: wipe the rows (keeping the schema) and replay digestive's INSERTs.
	mustExec(t, eng, "TRUNCATE TABLE `"+table+"`")
	if _, err := eng.db.ExecContext(ctx, script); err != nil {
		t.Fatalf("reload restore script: %v\n---\n%s", err, script)
	}

	reloaded := readVal(ctx, t, src, table)

	// Fidelity: same-engine round-trip must reproduce the exact bytes read back
	// from the original insert. Any divergence is either a digestive bug or an
	// edge case to document (then annotate skip_/note in fixtures.yaml).
	assertCellsEqual(t, original, reloaded)

	// Golden: record what the reloaded values actually are, with the mapping
	// metadata that feeds the user-facing type table.
	doc := goldenDoc{
		laravel:     f.Laravel,
		engine:      eng.name,
		sourceType:  valMeta.SourceType,
		parquetType: valMeta.ParquetType,
		lossless:    valMeta.Lossless,
		note:        f.Note,
		rows:        renderCells(reloaded),
	}
	compareOrUpdate(t, eng.name, f.Laravel, doc.String())
}

// exportTable runs the real export path for one table into a temp run directory
// and returns the directory plus the manifest metadata for the `val` column.
func exportTable(ctx context.Context, t *testing.T, src source.Source, eng *engine, table string) (string, manifest.Column) {
	t.Helper()
	cols, err := src.Columns(ctx, table)
	if err != nil {
		t.Fatalf("read columns: %v", err)
	}

	names := make([]string, len(cols))
	mappings := make([]typemap.Mapping, len(cols))
	mcols := make([]manifest.Column, len(cols))
	var valMeta manifest.Column
	for i, c := range cols {
		m := typemap.Map(c.DataType, c.Unsigned)
		names[i] = c.Name
		mappings[i] = m
		st := c.DataType
		if c.Unsigned {
			st += " unsigned"
		}
		mcols[i] = manifest.Column{
			Name:        c.Name,
			SourceType:  st,
			Nullable:    c.Nullable,
			ParquetType: m.Physical(),
			Lossless:    m.Lossless,
		}
		if c.Name == "val" {
			valMeta = mcols[i]
		}
	}

	dir := t.TempDir()
	file := table + ".parquet"
	pw, err := writer.NewParquet(filepath.Join(dir, file), names, mappings)
	if err != nil {
		t.Fatalf("new parquet: %v", err)
	}
	rows, err := src.Query(ctx, source.QuerySpec{Table: table, Columns: names, OrderBy: "rn"})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	var n int64
	for rows.Next() {
		cells, err := rows.Scan()
		if err != nil {
			rows.Close()
			t.Fatalf("scan: %v", err)
		}
		if err := pw.WriteRow(cells); err != nil {
			rows.Close()
			t.Fatalf("write row: %v", err)
		}
		n++
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		t.Fatalf("rows: %v", err)
	}
	rows.Close()
	if err := pw.Close(); err != nil {
		t.Fatalf("close parquet: %v", err)
	}

	man := &manifest.Manifest{
		Version:   manifest.Version,
		RunID:     "inttest",
		CreatedAt: "2026-01-01T00:00:00Z",
		Source:    manifest.SourceInfo{Engine: eng.name},
		Complete:  true,
		Tables: []manifest.Table{{
			Name: table, File: file, Rows: n, Columns: mcols,
		}},
	}
	if err := man.Write(filepath.Join(dir, "manifest.json")); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	return dir, valMeta
}

func restoreToSQL(t *testing.T, runDir string, dialect restore.Dialect) string {
	t.Helper()
	var buf bytes.Buffer
	if err := restore.Run(restore.Options{RunDir: runDir, Dialect: dialect, Out: &buf}); err != nil {
		t.Fatalf("restore: %v", err)
	}
	return buf.String()
}

// readVal reads the `val` column for a table, ordered by rn, as raw cells.
func readVal(ctx context.Context, t *testing.T, src source.Source, table string) []value.Value {
	t.Helper()
	rows, err := src.Query(ctx, source.QuerySpec{Table: table, Columns: []string{"val"}, OrderBy: "rn"})
	if err != nil {
		t.Fatalf("read val: %v", err)
	}
	defer rows.Close()
	var out []value.Value
	for rows.Next() {
		cells, err := rows.Scan()
		if err != nil {
			t.Fatalf("scan val: %v", err)
		}
		out = append(out, cells[0])
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows val: %v", err)
	}
	return out
}

func renderCells(cells []value.Value) []string {
	out := make([]string, len(cells))
	for i, c := range cells {
		out[i] = renderCell(c)
	}
	return out
}

// assertCellsEqual is the fidelity gate: original and reloaded must match cell
// for cell (NULL-ness and exact bytes).
func assertCellsEqual(t *testing.T, original, reloaded []value.Value) {
	t.Helper()
	if len(original) != len(reloaded) {
		t.Fatalf("row count changed across round-trip: original %d, reloaded %d", len(original), len(reloaded))
	}
	for i := range original {
		o, r := original[i], reloaded[i]
		if o.Null != r.Null || !bytes.Equal(o.Bytes, r.Bytes) {
			t.Errorf("row %d not faithful:\n  original: %s\n  reloaded: %s",
				i, renderCell(o), renderCell(r))
		}
	}
}

func mustExec(t *testing.T, eng *engine, stmt string) {
	t.Helper()
	if _, err := eng.db.Exec(stmt); err != nil {
		t.Fatalf("exec %q: %v", stmt, err)
	}
}

// sanitizeIdent keeps table names to a safe [a-z0-9_] slug derived from the
// Laravel method name.
func sanitizeIdent(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	return b.String()
}
