// Package writer streams rows into a Parquet file. The write is atomic: rows
// are written to a temporary file that is renamed into place only on a clean
// Close, so a consumer never sees a truncated Parquet file.
package writer

import (
	"fmt"
	"os"

	"github.com/danmatthews/sql-exporter/internal/typemap"
	"github.com/danmatthews/sql-exporter/internal/value"
	"github.com/parquet-go/parquet-go"
)

// ParquetWriter writes one table to one Parquet file.
type ParquetWriter struct {
	finalPath string
	tmpPath   string
	file      *os.File
	writer    *parquet.GenericWriter[any]
	columns   []string
	mappings  []typemap.Mapping
	rows      int64
	closed    bool
}

// NewParquet creates a writer for the given columns and their type mappings.
// columns and mappings must be the same length and in the same order.
func NewParquet(finalPath string, columns []string, mappings []typemap.Mapping) (*ParquetWriter, error) {
	if len(columns) != len(mappings) {
		return nil, fmt.Errorf("columns/mappings length mismatch: %d vs %d", len(columns), len(mappings))
	}

	group := parquet.Group{}
	for i, col := range columns {
		group[col] = mappings[i].Node()
	}
	schema := parquet.NewSchema("row", group)

	tmpPath := finalPath + ".tmp"
	f, err := os.Create(tmpPath)
	if err != nil {
		return nil, fmt.Errorf("create %s: %w", tmpPath, err)
	}

	return &ParquetWriter{
		finalPath: finalPath,
		tmpPath:   tmpPath,
		file:      f,
		writer:    parquet.NewGenericWriter[any](f, schema),
		columns:   columns,
		mappings:  mappings,
	}, nil
}

// WriteRow encodes and writes a single row. cells must align with the columns
// passed to NewParquet.
func (w *ParquetWriter) WriteRow(cells []value.Value) error {
	if len(cells) != len(w.columns) {
		return fmt.Errorf("row has %d cells, expected %d", len(cells), len(w.columns))
	}
	row := make(map[string]any, len(w.columns))
	for i, col := range w.columns {
		v, err := w.mappings[i].Encode(cells[i])
		if err != nil {
			return fmt.Errorf("column %q: %w", col, err)
		}
		row[col] = v
	}
	if _, err := w.writer.Write([]any{row}); err != nil {
		return fmt.Errorf("write row: %w", err)
	}
	w.rows++
	return nil
}

// Rows returns the number of rows written so far.
func (w *ParquetWriter) Rows() int64 { return w.rows }

// Close flushes and closes the Parquet stream, then atomically renames the
// temporary file into its final path.
func (w *ParquetWriter) Close() error {
	if w.closed {
		return nil
	}
	w.closed = true

	if err := w.writer.Close(); err != nil {
		w.file.Close()
		os.Remove(w.tmpPath)
		return fmt.Errorf("close parquet writer: %w", err)
	}
	if err := w.file.Close(); err != nil {
		os.Remove(w.tmpPath)
		return fmt.Errorf("close file: %w", err)
	}
	if err := os.Rename(w.tmpPath, w.finalPath); err != nil {
		os.Remove(w.tmpPath)
		return fmt.Errorf("finalise %s: %w", w.finalPath, err)
	}
	return nil
}

// Abort closes the stream and removes the temporary file, discarding output.
func (w *ParquetWriter) Abort() {
	if w.closed {
		return
	}
	w.closed = true
	w.writer.Close()
	w.file.Close()
	os.Remove(w.tmpPath)
}
