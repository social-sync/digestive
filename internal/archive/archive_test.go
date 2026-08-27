package archive

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"
)

// writeFile writes content to a file, creating parent directories.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// readFile reads a file and fails the test on error.
func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestZipUnzipRoundTrip(t *testing.T) {
	src := t.TempDir()
	writeFile(t, filepath.Join(src, "manifest.json"), `{"version":1}`)
	writeFile(t, filepath.Join(src, "users.parquet"), "PARQUET-USERS")
	writeFile(t, filepath.Join(src, "sub", "nested.txt"), "nested")

	zipPath := filepath.Join(t.TempDir(), "run.zip")
	if err := Zip(src, zipPath); err != nil {
		t.Fatalf("Zip: %v", err)
	}

	out := t.TempDir()
	if err := Unzip(zipPath, out); err != nil {
		t.Fatalf("Unzip: %v", err)
	}

	if got := readFile(t, filepath.Join(out, "manifest.json")); got != `{"version":1}` {
		t.Errorf("manifest = %q", got)
	}
	if got := readFile(t, filepath.Join(out, "users.parquet")); got != "PARQUET-USERS" {
		t.Errorf("parquet = %q", got)
	}
	if got := readFile(t, filepath.Join(out, "sub", "nested.txt")); got != "nested" {
		t.Errorf("nested = %q", got)
	}
}

func TestZipStoresEntriesAtRoot(t *testing.T) {
	src := t.TempDir()
	writeFile(t, filepath.Join(src, "manifest.json"), "{}")

	zipPath := filepath.Join(t.TempDir(), "run.zip")
	if err := Zip(src, zipPath); err != nil {
		t.Fatal(err)
	}

	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	if len(zr.File) != 1 {
		t.Fatalf("want 1 entry, got %d", len(zr.File))
	}
	if zr.File[0].Name != "manifest.json" {
		t.Errorf("entry name = %q, want manifest.json (no wrapping dir)", zr.File[0].Name)
	}
}

func TestLooksLikeZip(t *testing.T) {
	dir := t.TempDir()

	realZip := filepath.Join(dir, "run.zip")
	if err := Zip(dir, realZip); err != nil {
		// Zipping an empty dir is fine; it still yields a valid archive.
		t.Fatal(err)
	}
	if ok, err := LooksLikeZip(realZip); err != nil || !ok {
		t.Errorf("LooksLikeZip(real) = %v, %v; want true, nil", ok, err)
	}

	notZip := filepath.Join(dir, "manifest.json")
	writeFile(t, notZip, `{"version":1}`)
	if ok, err := LooksLikeZip(notZip); err != nil || ok {
		t.Errorf("LooksLikeZip(json) = %v, %v; want false, nil", ok, err)
	}

	if ok, err := LooksLikeZip(dir); err != nil || ok {
		t.Errorf("LooksLikeZip(dir) = %v, %v; want false, nil", ok, err)
	}
}

func TestExtractRunFindsManifestAtRoot(t *testing.T) {
	src := t.TempDir()
	writeFile(t, filepath.Join(src, "manifest.json"), "{}")
	writeFile(t, filepath.Join(src, "t.parquet"), "x")

	zipPath := filepath.Join(t.TempDir(), "run.zip")
	if err := Zip(src, zipPath); err != nil {
		t.Fatal(err)
	}

	dir, cleanup, err := ExtractRun(zipPath)
	if err != nil {
		t.Fatalf("ExtractRun: %v", err)
	}
	defer cleanup()

	if _, err := os.Stat(filepath.Join(dir, "manifest.json")); err != nil {
		t.Errorf("manifest not found in extracted run: %v", err)
	}

	cleanup()
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("cleanup did not remove temp dir: %v", err)
	}
}

func TestExtractRunFindsManifestInWrappingDir(t *testing.T) {
	// An archive made by hand may wrap the run in a top-level folder.
	staging := t.TempDir()
	run := filepath.Join(staging, "2026-01-01T00-00-00Z")
	writeFile(t, filepath.Join(run, "manifest.json"), "{}")

	zipPath := filepath.Join(t.TempDir(), "wrapped.zip")
	if err := Zip(staging, zipPath); err != nil {
		t.Fatal(err)
	}

	dir, cleanup, err := ExtractRun(zipPath)
	if err != nil {
		t.Fatalf("ExtractRun: %v", err)
	}
	defer cleanup()

	if filepath.Base(dir) != "2026-01-01T00-00-00Z" {
		t.Errorf("run dir = %q, want the wrapping folder", dir)
	}
	if _, err := os.Stat(filepath.Join(dir, "manifest.json")); err != nil {
		t.Errorf("manifest not found: %v", err)
	}
}

func TestExtractRunRejectsNonRun(t *testing.T) {
	src := t.TempDir()
	writeFile(t, filepath.Join(src, "data.txt"), "no manifest here")

	zipPath := filepath.Join(t.TempDir(), "notarun.zip")
	if err := Zip(src, zipPath); err != nil {
		t.Fatal(err)
	}

	_, cleanup, err := ExtractRun(zipPath)
	cleanup()
	if err == nil {
		t.Fatal("want error for archive without a manifest, got nil")
	}
}

func TestUnzipRejectsZipSlip(t *testing.T) {
	// Craft an archive with an entry that tries to escape the destination.
	zipPath := filepath.Join(t.TempDir(), "evil.zip")
	f, err := os.Create(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	w, err := zw.Create("../escape.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("pwned")); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	f.Close()

	dest := filepath.Join(t.TempDir(), "out")
	if err := Unzip(zipPath, dest); err == nil {
		t.Fatal("want zip-slip to be rejected, got nil error")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(dest), "escape.txt")); err == nil {
		t.Fatal("zip-slip wrote a file outside the destination")
	}
}
