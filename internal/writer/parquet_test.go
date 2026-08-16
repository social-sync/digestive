package writer

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/social-sync/digestive/internal/typemap"
	"github.com/social-sync/digestive/internal/value"
	"github.com/parquet-go/parquet-go"
)

// TestZeroValuesRoundTripNonNull is a regression test for a corruption where a
// zero-valued scalar (int64(0), float64(0), or "") in an optional column was
// written to Parquet as NULL, while an actual SQL NULL was indistinguishable
// from it. Each column here carries a zero value, a non-zero value, and a real
// NULL; only the last must read back as null.
func TestZeroValuesRoundTripNonNull(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "zeros.parquet")

	cols := []string{"i", "d", "s"}
	mappings := []typemap.Mapping{
		typemap.Map("tinyint", false), // KindInt64
		typemap.Map("double", false),  // KindDouble
		typemap.Map("varchar", false), // KindString
	}
	pw, err := NewParquet(path, cols, mappings)
	if err != nil {
		t.Fatalf("NewParquet: %v", err)
	}
	rows := [][]value.Value{
		{value.Text("0"), value.Text("0"), value.Text("")}, // zero values, not NULL
		{value.Text("7"), value.Text("2.5"), value.Text("hi")},
		{value.Null, value.Null, value.Null},
	}
	for _, r := range rows {
		if err := pw.WriteRow(r); err != nil {
			t.Fatalf("WriteRow: %v", err)
		}
	}
	if err := pw.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer f.Close()
	info, _ := f.Stat()
	pf, err := parquet.OpenFile(f, info.Size())
	if err != nil {
		t.Fatalf("OpenFile: %v", err)
	}
	rr := pf.RowGroups()[0].Rows()
	defer rr.Close()
	buf := make([]parquet.Row, len(rows))
	n, _ := rr.ReadRows(buf)
	if n != len(rows) {
		t.Fatalf("read %d rows, want %d", n, len(rows))
	}
	// Column order in a parquet.Row follows the schema's leaf order; find each
	// by name via the file schema.
	fields := pf.Schema().Fields()
	idx := map[string]int{}
	for i, fld := range fields {
		idx[fld.Name()] = i
	}

	// Row 0: zero values must be present, not null.
	if v := buf[0][idx["i"]]; v.IsNull() || v.Int64() != 0 {
		t.Errorf("row0 i = %v (null=%v), want 0 non-null", v, v.IsNull())
	}
	if v := buf[0][idx["d"]]; v.IsNull() || v.Double() != 0 {
		t.Errorf("row0 d = %v (null=%v), want 0 non-null", v, v.IsNull())
	}
	if v := buf[0][idx["s"]]; v.IsNull() || len(v.ByteArray()) != 0 {
		t.Errorf("row0 s = %v (null=%v), want empty non-null", v, v.IsNull())
	}
	// Row 2: genuine NULLs must read back as null.
	for _, c := range cols {
		if v := buf[2][idx[c]]; !v.IsNull() {
			t.Errorf("row2 %s = %v, want null", c, v)
		}
	}
}

// TestCompressionShrinksOutput writes highly-compressible data and asserts the
// resulting file is far smaller than the raw payload — proof zstd is applied to
// the column chunks.
func TestCompressionShrinksOutput(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "payload.parquet")

	mappings := []typemap.Mapping{typemap.Map("text", false)}
	pw, err := NewParquet(path, []string{"payload"}, mappings)
	if err != nil {
		t.Fatalf("NewParquet: %v", err)
	}

	// Repetitive text compresses dramatically.
	blob := ""
	for i := 0; i < 200; i++ {
		blob += "the quick brown fox jumps over the lazy dog "
	}
	const rows = 500
	for i := 0; i < rows; i++ {
		if err := pw.WriteRow([]value.Value{value.Text(blob)}); err != nil {
			t.Fatalf("WriteRow: %v", err)
		}
	}
	if err := pw.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	rawSize := int64(len(blob)) * rows
	// zstd on this input compresses well over 10x; a conservative bound proves
	// compression is active without being brittle.
	if info.Size() >= rawSize/2 {
		t.Errorf("file size %d not meaningfully smaller than raw %d — compression not applied?", info.Size(), rawSize)
	}
}
