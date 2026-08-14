// Package manifest defines the per-run metadata file. The Manifest is the
// source of truth for reconstructing exact INSERTs from the Parquet data: it
// records each table's ordered columns, their precise source types, nullability,
// the physical Parquet type chosen, and any transform applied.
package manifest

import (
	"encoding/json"
	"fmt"
	"os"
)

// Version is the manifest schema version.
const Version = 1

// Manifest is the metadata for one export run.
type Manifest struct {
	Version   int        `json:"version"`
	RunID     string     `json:"run_id"`
	CreatedAt string     `json:"created_at"`
	Source    SourceInfo `json:"source"`
	Tables    []Table    `json:"tables"`
	// Complete is written true only once every table has been exported
	// successfully. A consumer must treat a run with Complete=false as partial.
	Complete bool `json:"complete"`
}

// SourceInfo identifies the database engine a run read from.
type SourceInfo struct {
	Engine string `json:"engine"`
}

// Table is one exported table's metadata.
type Table struct {
	Name    string   `json:"name"`
	File    string   `json:"file"`
	Rows    int64    `json:"rows"`
	Where   string   `json:"where,omitempty"`
	OrderBy string   `json:"order_by,omitempty"`
	Limit   *int     `json:"limit,omitempty"`
	Columns []Column `json:"columns"`
}

// Column records how one column was exported.
type Column struct {
	Name        string `json:"name"`
	SourceType  string `json:"source_type"`
	Nullable    bool   `json:"nullable"`
	ParquetType string `json:"parquet_type"`
	// Lossless is true when the source type was stored as its exact
	// string/byte representation rather than a native Parquet type.
	Lossless bool `json:"lossless"`
	// Transform is the transform applied to this column, if any.
	Transform string `json:"transform,omitempty"`
}

// Write serialises the manifest as indented JSON to path.
func (m *Manifest) Write(path string) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal manifest: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write manifest: %w", err)
	}
	return nil
}
