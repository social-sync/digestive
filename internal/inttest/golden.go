//go:build integration

package inttest

import (
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/social-sync/digestive/internal/value"
)

// renderCell turns a raw cell read back off the wire into a stable, single-line,
// human-readable golden token:
//   - NULL              → the literal NULL
//   - valid UTF-8 text  → a Go-quoted string ("O'Brien\n", "😀 4-byte 𝕏")
//   - anything else      → hex:<uppercase-hex> (binary / WKB / BSON …)
//
// Quoting keeps multi-byte text legible while still escaping control characters,
// so a JSON blob or an embedded newline stays on one line and diffs cleanly.
func renderCell(v value.Value) string {
	if v.Null {
		return "NULL"
	}
	if utf8.Valid(v.Bytes) {
		return strconv.Quote(string(v.Bytes))
	}
	return "hex:" + strings.ToUpper(hex.EncodeToString(v.Bytes))
}

// goldenDoc is what we write per (engine, Laravel type): a metadata header that
// documents the mapping decision, followed by one rendered value per row. The
// header lines double as the source material for the user-facing type-mapping
// table, which is why they carry the source type, chosen Parquet type, and
// lossless flag.
type goldenDoc struct {
	laravel     string
	engine      string
	sourceType  string // observed INFORMATION_SCHEMA.DATA_TYPE
	parquetType string // typemap physical type
	lossless    bool
	note        string
	rows        []string // renderCell per reloaded row
}

func (g goldenDoc) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "# laravel: %s\n", g.laravel)
	fmt.Fprintf(&b, "# engine: %s\n", g.engine)
	fmt.Fprintf(&b, "# source_type: %s | parquet: %s | lossless: %t\n",
		g.sourceType, g.parquetType, g.lossless)
	if g.note != "" {
		fmt.Fprintf(&b, "# note: %s\n", collapseWS(g.note))
	}
	b.WriteString("---\n")
	for _, r := range g.rows {
		b.WriteString(r)
		b.WriteByte('\n')
	}
	return b.String()
}

func collapseWS(s string) string { return strings.Join(strings.Fields(s), " ") }

// goldenPath is golden/<engine>/<laravel>.golden, relative to the package.
func goldenPath(engine, laravel string) string {
	return filepath.Join("golden", engine, laravel+".golden")
}

// compareOrUpdate compares got against the on-disk golden. Behaviour:
//   - golden missing  → write it and pass, logging that it must be reviewed.
//     (This is the discovery step: a brand-new type characterises itself, the
//     author eyeballs the git diff, then commits it.)
//   - -update set      → overwrite unconditionally.
//   - otherwise        → fail on any mismatch, showing the diff-ready contents.
//
// updateGolden is wired to the `-update` flag in roundtrip_test.go.
func compareOrUpdate(t goldenT, engine, laravel, got string) {
	path := goldenPath(engine, laravel)
	want, err := os.ReadFile(path)
	switch {
	case os.IsNotExist(err):
		writeGolden(t, path, got)
		t.Logf("created golden %s — review it before committing", path)
		return
	case err != nil:
		t.Fatalf("read golden %s: %v", path, err)
	}
	if updateGolden {
		writeGolden(t, path, got)
		return
	}
	if got != string(want) {
		t.Errorf("golden mismatch for %s/%s\n--- want ---\n%s\n--- got ---\n%s\n"+
			"(run `make test-integration UPDATE=1` to accept if this change is intended)",
			engine, laravel, want, got)
	}
}

func writeGolden(t goldenT, path, content string) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir golden dir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write golden %s: %v", path, err)
	}
}

// goldenT is the slice of *testing.T that golden helpers need, kept small so the
// helpers stay easy to reason about.
type goldenT interface {
	Logf(string, ...any)
	Errorf(string, ...any)
	Fatalf(string, ...any)
}
