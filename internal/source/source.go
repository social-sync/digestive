// Package source abstracts the database an export reads from. The first
// concrete implementation targets SingleStore over the MySQL wire protocol;
// the Source interface is the seam other engines slot into later.
package source

import (
	"context"

	"github.com/danmatthews/grimnir/internal/value"
)

// Column describes one column of a source table, as read from the database's
// information schema.
type Column struct {
	Name     string
	DataType string // base type, lowercase (e.g. "varchar", "bigint", "json")
	Unsigned bool   // whether the column type carries UNSIGNED
	Nullable bool
}

// QuerySpec describes a single-table read: which columns, in order, plus the
// optional row-reduction controls. Where and OrderBy are raw SQL fragments,
// treated as trusted config and interpolated literally.
type QuerySpec struct {
	Table   string
	Columns []string
	Where   string
	OrderBy string
	Limit   *int
}

// Rows is a forward-only cursor over a query result. Each Scan returns one row
// of raw cell values; the caller owns the returned slice.
type Rows interface {
	Next() bool
	Scan() ([]value.Value, error)
	Err() error
	Close() error
}

// Source is a database an export can read from.
type Source interface {
	// Ping verifies connectivity.
	Ping(ctx context.Context) error
	// Columns returns the ordered columns of a table.
	Columns(ctx context.Context, table string) ([]Column, error)
	// Query streams rows for a table according to spec.
	Query(ctx context.Context, spec QuerySpec) (Rows, error)
	// Close releases the underlying connection.
	Close() error
}
