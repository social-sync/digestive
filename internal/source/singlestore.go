package source

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/social-sync/grimnir/internal/value"
	_ "github.com/go-sql-driver/mysql"
)

// SingleStore reads from a SingleStore (or any MySQL-wire-compatible) database.
type SingleStore struct {
	db *sql.DB
}

// OpenSingleStore opens a connection pool to the given DSN. The DSN uses the
// go-sql-driver/mysql format.
func OpenSingleStore(dsn string) (*SingleStore, error) {
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	return &SingleStore{db: db}, nil
}

func (s *SingleStore) Ping(ctx context.Context) error {
	return s.db.PingContext(ctx)
}

func (s *SingleStore) Close() error {
	return s.db.Close()
}

func (s *SingleStore) Columns(ctx context.Context, table string) ([]Column, error) {
	const q = `
		SELECT COLUMN_NAME, DATA_TYPE, COLUMN_TYPE, IS_NULLABLE
		FROM INFORMATION_SCHEMA.COLUMNS
		WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ?
		ORDER BY ORDINAL_POSITION`

	rows, err := s.db.QueryContext(ctx, q, table)
	if err != nil {
		return nil, fmt.Errorf("read columns for %q: %w", table, err)
	}
	defer rows.Close()

	var cols []Column
	for rows.Next() {
		var name, dataType, columnType, isNullable string
		if err := rows.Scan(&name, &dataType, &columnType, &isNullable); err != nil {
			return nil, fmt.Errorf("scan column metadata for %q: %w", table, err)
		}
		cols = append(cols, Column{
			Name:     name,
			DataType: strings.ToLower(dataType),
			Unsigned: strings.Contains(strings.ToLower(columnType), "unsigned"),
			Nullable: strings.EqualFold(isNullable, "YES"),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate columns for %q: %w", table, err)
	}
	if len(cols) == 0 {
		return nil, fmt.Errorf("table %q not found or has no columns", table)
	}
	return cols, nil
}

func (s *SingleStore) Query(ctx context.Context, spec QuerySpec) (Rows, error) {
	sqlText := buildSelect(spec)
	rows, err := s.db.QueryContext(ctx, sqlText)
	if err != nil {
		return nil, fmt.Errorf("query %q: %w", spec.Table, err)
	}
	cols, err := rows.Columns()
	if err != nil {
		rows.Close()
		return nil, fmt.Errorf("read result columns for %q: %w", spec.Table, err)
	}
	return &sqlRows{rows: rows, ncols: len(cols)}, nil
}

// buildSelect assembles the SELECT. Identifiers are backtick-quoted; the WHERE
// and ORDER BY fragments are interpolated raw as trusted config.
func buildSelect(spec QuerySpec) string {
	var b strings.Builder
	b.WriteString("SELECT ")
	for i, c := range spec.Columns {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(quoteIdent(c))
	}
	b.WriteString(" FROM ")
	b.WriteString(quoteIdent(spec.Table))
	if strings.TrimSpace(spec.Where) != "" {
		b.WriteString(" WHERE ")
		b.WriteString(spec.Where)
	}
	if strings.TrimSpace(spec.OrderBy) != "" {
		b.WriteString(" ORDER BY ")
		b.WriteString(spec.OrderBy)
	}
	if spec.Limit != nil {
		fmt.Fprintf(&b, " LIMIT %d", *spec.Limit)
	}
	return b.String()
}

// quoteIdent backtick-quotes a SQL identifier, escaping any embedded backticks.
func quoteIdent(id string) string {
	return "`" + strings.ReplaceAll(id, "`", "``") + "`"
}

// sqlRows adapts *sql.Rows to the Rows interface, copying each cell's raw bytes
// so the returned values outlive the cursor's internal buffer.
type sqlRows struct {
	rows  *sql.Rows
	ncols int
}

func (r *sqlRows) Next() bool { return r.rows.Next() }

func (r *sqlRows) Scan() ([]value.Value, error) {
	raw := make([]sql.RawBytes, r.ncols)
	dest := make([]any, r.ncols)
	for i := range raw {
		dest[i] = &raw[i]
	}
	if err := r.rows.Scan(dest...); err != nil {
		return nil, err
	}
	out := make([]value.Value, r.ncols)
	for i, rb := range raw {
		if rb == nil {
			out[i] = value.Null
			continue
		}
		b := make([]byte, len(rb))
		copy(b, rb)
		out[i] = value.Value{Bytes: b}
	}
	return out, nil
}

func (r *sqlRows) Err() error   { return r.rows.Err() }
func (r *sqlRows) Close() error { return r.rows.Close() }
