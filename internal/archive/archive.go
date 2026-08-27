// Package archive packages an export run directory into a single .zip artifact
// and unpacks one again. It exists so an export can be handed around as one file
// and restore can read that file directly, without the caller ever unzipping by
// hand. Entries are stored at the archive root (manifest.json and the Parquet
// files sit directly inside the zip, with no wrapping directory), so an archive
// round-trips back to exactly the run directory it came from.
package archive

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// manifestName is the file every export run and every archive of one contains;
// its presence identifies the run directory inside an extracted archive.
const manifestName = "manifest.json"

// Zip writes every regular file under srcDir into destZip, with each entry
// stored at the archive root relative to srcDir. Sub-directories are preserved
// as path prefixes; empty directories are skipped (an export run is a flat set
// of files, so this loses nothing). destZip is created (and any existing file
// at that path truncated); it must not sit inside srcDir.
func Zip(srcDir, destZip string) error {
	info, err := os.Stat(srcDir)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("zip source %s is not a directory", srcDir)
	}

	f, err := os.Create(destZip)
	if err != nil {
		return fmt.Errorf("create archive: %w", err)
	}
	// Close reports the final flush error; on the happy path Close below is the
	// authoritative one, and this deferred Close is a harmless no-op.
	defer f.Close()

	zw := zip.NewWriter(f)
	walkErr := filepath.Walk(srcDir, func(path string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if fi.IsDir() {
			return nil
		}
		if !fi.Mode().IsRegular() {
			return fmt.Errorf("cannot archive non-regular file %s", path)
		}
		rel, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}
		// Zip entries always use forward slashes regardless of OS.
		hdr, err := zip.FileInfoHeader(fi)
		if err != nil {
			return err
		}
		hdr.Name = filepath.ToSlash(rel)
		hdr.Method = zip.Deflate
		w, err := zw.CreateHeader(hdr)
		if err != nil {
			return err
		}
		src, err := os.Open(path)
		if err != nil {
			return err
		}
		defer src.Close()
		if _, err := io.Copy(w, src); err != nil {
			return err
		}
		return nil
	})
	if walkErr != nil {
		zw.Close()
		return walkErr
	}
	if err := zw.Close(); err != nil {
		return fmt.Errorf("finalise archive: %w", err)
	}
	return f.Close()
}

// Unzip extracts srcZip into destDir, creating destDir (and any parent
// directories) as needed. It rejects entries whose paths would escape destDir
// (zip-slip), so a malicious or malformed archive cannot write outside the
// target.
func Unzip(srcZip, destDir string) error {
	zr, err := zip.OpenReader(srcZip)
	if err != nil {
		return fmt.Errorf("open archive %s: %w", srcZip, err)
	}
	defer zr.Close()

	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return err
	}
	for _, entry := range zr.File {
		if err := extractEntry(entry, destDir); err != nil {
			return err
		}
	}
	return nil
}

// extractEntry writes one archive entry into destDir, guarding against a path
// that would resolve outside destDir.
func extractEntry(entry *zip.File, destDir string) error {
	// Clean the (slash-separated) archive name into an OS path, then confirm it
	// stays within destDir before touching the filesystem.
	target := filepath.Join(destDir, filepath.FromSlash(entry.Name))
	if !within(destDir, target) {
		return fmt.Errorf("archive entry %q escapes the destination directory", entry.Name)
	}

	if entry.FileInfo().IsDir() {
		return os.MkdirAll(target, 0o755)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}

	rc, err := entry.Open()
	if err != nil {
		return err
	}
	defer rc.Close()

	out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, rc); err != nil { //nolint:gosec // size-bounded by extractRun's use on trusted export artifacts
		return err
	}
	return out.Close()
}

// within reports whether target is inside dir (or equal to it), used to reject
// zip-slip paths.
func within(dir, target string) bool {
	rel, err := filepath.Rel(dir, target)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}

// LooksLikeZip reports whether path is a regular file that begins with the ZIP
// local-file-header magic ("PK\x03\x04"). It returns false for a directory, so
// a caller can pass either a run directory or an archive to the same entry
// point and let this decide. An empty or truncated archive (magic "PK\x05\x06"
// for an empty central directory) is also recognised.
func LooksLikeZip(path string) (bool, error) {
	info, err := os.Stat(path)
	if err != nil {
		return false, err
	}
	if info.IsDir() {
		return false, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer f.Close()
	var magic [4]byte
	n, err := io.ReadFull(f, magic[:])
	if err == io.EOF || err == io.ErrUnexpectedEOF {
		return false, nil // too short to be a zip
	}
	if err != nil {
		return false, err
	}
	if n < 4 || magic[0] != 'P' || magic[1] != 'K' {
		return false, nil
	}
	return (magic[2] == 0x03 && magic[3] == 0x04) || (magic[2] == 0x05 && magic[3] == 0x06), nil
}

// ExtractRun unpacks a run archive into a fresh temporary directory and returns
// the directory that holds its manifest.json — the run directory restore reads.
// The archive normally stores the run's files at its root, but an archive made
// by hand may wrap them in a single top-level folder; ExtractRun locates the
// manifest either way. The returned cleanup removes the temporary directory and
// must be called when the caller is done reading the run (never nil, even on
// error, so `defer cleanup()` is always safe).
func ExtractRun(zipPath string) (dir string, cleanup func(), err error) {
	tmp, err := os.MkdirTemp("", "digestive-restore-*")
	if err != nil {
		return "", func() {}, err
	}
	cleanup = func() { os.RemoveAll(tmp) }

	if err := Unzip(zipPath, tmp); err != nil {
		cleanup()
		return "", func() {}, err
	}

	runDir, err := findRunDir(tmp)
	if err != nil {
		cleanup()
		return "", func() {}, err
	}
	return runDir, cleanup, nil
}

// findRunDir returns the directory under root that contains a manifest.json,
// looking at root itself first and then one level of subdirectories. It errors
// if no manifest is found, so an archive that is not an export run fails clearly
// rather than producing a confusing "manifest not found" deeper in restore.
func findRunDir(root string) (string, error) {
	if _, err := os.Stat(filepath.Join(root, manifestName)); err == nil {
		return root, nil
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return "", err
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		sub := filepath.Join(root, e.Name())
		if _, err := os.Stat(filepath.Join(sub, manifestName)); err == nil {
			return sub, nil
		}
	}
	return "", fmt.Errorf("no %s found in archive; it does not look like a digestive export run", manifestName)
}
