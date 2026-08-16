package writer

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/social-sync/digestive/internal/typemap"
	"github.com/social-sync/digestive/internal/value"
)

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
