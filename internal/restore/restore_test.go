package restore

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/danmatthews/grimnir/internal/manifest"
	"github.com/danmatthews/grimnir/internal/typemap"
	"github.com/danmatthews/grimnir/internal/value"
	"github.com/danmatthews/grimnir/internal/writer"
)

// col describes one column for a test fixture: its source type and the raw
// cells (one per row) to write.
type col struct {
	name     string
	dataType string
	unsigned bool
	cells    []value.Value
}

// writeFixture builds a manifest.json and one Parquet file per table in a temp
// run directory and returns the directory.
func writeFixture(t *testing.T, tables map[string][]col) string {
	t.Helper()
	dir := t.TempDir()

	man := &manifest.Manifest{
		Version:   manifest.Version,
		RunID:     "testrun",
		CreatedAt: "2026-08-14T00:00:00Z",
		Source:    manifest.SourceInfo{Engine: "singlestore"},
		Complete:  true,
	}

	for table, cols := range tables {
		names := make([]string, len(cols))
		mappings := make([]typemap.Mapping, len(cols))
		mcols := make([]manifest.Column, len(cols))
		nRows := 0
		for i, c := range cols {
			names[i] = c.name
			m := typemap.Map(c.dataType, c.unsigned)
			mappings[i] = m
			st := c.dataType
			if c.unsigned {
				st += " unsigned"
			}
			mcols[i] = manifest.Column{
				Name:        c.name,
				SourceType:  st,
				Nullable:    true,
				ParquetType: m.Physical(),
				Lossless:    m.Lossless,
			}
			if len(c.cells) > nRows {
				nRows = len(c.cells)
			}
		}

		file := table + ".parquet"
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
			Name:    table,
			File:    file,
			Rows:    int64(nRows),
			Columns: mcols,
		})
	}

	if err := man.Write(filepath.Join(dir, "manifest.json")); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	return dir
}

func runRestore(t *testing.T, dir string, opts Options) string {
	t.Helper()
	var buf bytes.Buffer
	opts.RunDir = dir
	opts.Out = &buf
	if err := Run(opts); err != nil {
		t.Fatalf("restore: %v", err)
	}
	return buf.String()
}

func TestRestoreRendersValues(t *testing.T) {
	dir := writeFixture(t, map[string][]col{
		"users": {
			{name: "id", dataType: "int", cells: []value.Value{value.Text("1"), value.Text("2")}},
			{name: "name", dataType: "varchar", cells: []value.Value{value.Text("O'Brien\n"), value.Null}},
			{name: "balance", dataType: "decimal", cells: []value.Value{value.Text("10.50"), value.Text("0.00")}},
			{name: "avatar", dataType: "blob", cells: []value.Value{{Bytes: []byte{0xDE, 0xAD}}, {Bytes: []byte{}}}},
			{name: "score", dataType: "double", cells: []value.Value{value.Text("1.5"), value.Text("2.5")}},
		},
	})

	out := runRestore(t, dir, Options{Dialect: MySQL})

	wants := []string{
		"dialect mysql",
		"SET NAMES utf8mb4;",
		"SET FOREIGN_KEY_CHECKS=0;",
		"SET UNIQUE_CHECKS=0;",
		"START TRANSACTION;",
		"-- table: users (2 rows)",
		"INSERT INTO `users` (`id`, `name`, `balance`, `avatar`, `score`) VALUES",
		`(1, 'O\'Brien\n', '10.50', X'DEAD', 1.5)`,
		`(2, NULL, '0.00', X'', 2.5)`,
		"COMMIT;",
	}
	for _, w := range wants {
		if !strings.Contains(out, w) {
			t.Errorf("output missing %q\n---\n%s", w, out)
		}
	}
}

func TestSingleStoreDialectOmitsMySQLPreamble(t *testing.T) {
	dir := writeFixture(t, map[string][]col{
		"t": {{name: "id", dataType: "int", cells: []value.Value{value.Text("1")}}},
	})
	out := runRestore(t, dir, Options{Dialect: SingleStore})

	if strings.Contains(out, "FOREIGN_KEY_CHECKS") || strings.Contains(out, "UNIQUE_CHECKS") {
		t.Errorf("singlestore preamble must not contain MySQL-only checks\n%s", out)
	}
	for _, w := range []string{"SET NAMES utf8mb4;", "START TRANSACTION;", "COMMIT;"} {
		if !strings.Contains(out, w) {
			t.Errorf("output missing %q\n%s", w, out)
		}
	}
}

func TestBatchSizeSplitsStatements(t *testing.T) {
	dir := writeFixture(t, map[string][]col{
		"t": {{name: "id", dataType: "int", cells: []value.Value{value.Text("1"), value.Text("2"), value.Text("3")}}},
	})
	out := runRestore(t, dir, Options{Dialect: SingleStore, BatchSize: 2})

	if got := strings.Count(out, "INSERT INTO `t`"); got != 2 {
		t.Errorf("expected 2 INSERT statements with batch size 2 over 3 rows, got %d\n%s", got, out)
	}
}

func TestEmptyTableEmitsCommentOnly(t *testing.T) {
	dir := writeFixture(t, map[string][]col{
		"empties": {{name: "id", dataType: "int", cells: []value.Value{}}},
	})
	out := runRestore(t, dir, Options{Dialect: SingleStore})

	if !strings.Contains(out, "-- table: empties (0 rows)") {
		t.Errorf("expected empty-table comment\n%s", out)
	}
	if strings.Contains(out, "INSERT INTO `empties`") {
		t.Errorf("empty table must not emit an INSERT\n%s", out)
	}
}

func TestIncompleteManifestRefusedUnlessAllowed(t *testing.T) {
	dir := writeFixture(t, map[string][]col{
		"t": {{name: "id", dataType: "int", cells: []value.Value{value.Text("1")}}},
	})
	// Rewrite the manifest with complete=false.
	man, err := manifest.Load(filepath.Join(dir, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	man.Complete = false
	if err := man.Write(filepath.Join(dir, "manifest.json")); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	err = Run(Options{RunDir: dir, Dialect: SingleStore, Out: &buf})
	if err == nil {
		t.Fatal("expected refusal on incomplete manifest")
	}
	if !strings.Contains(err.Error(), "incomplete") {
		t.Errorf("error should mention incompleteness, got: %v", err)
	}

	// With the override it should succeed.
	buf.Reset()
	if err := Run(Options{RunDir: dir, Dialect: SingleStore, AllowIncomplete: true, Out: &buf}); err != nil {
		t.Errorf("expected success with --allow-incomplete, got: %v", err)
	}
}

func TestParseDialect(t *testing.T) {
	for _, s := range []string{"mysql", "singlestore"} {
		if _, err := ParseDialect(s); err != nil {
			t.Errorf("ParseDialect(%q) unexpected error: %v", s, err)
		}
	}
	if _, err := ParseDialect("postgres"); err == nil {
		t.Error("ParseDialect(postgres) should error")
	}
}
