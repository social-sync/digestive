package restore

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/parquet-go/parquet-go"
)

// Dialect is the target SQL engine. It currently affects only the session
// preamble; value and identifier syntax are identical across both engines
// (both speak the MySQL wire protocol). It is the seam where cross-engine
// type mapping (e.g. Postgres) would later hook in.
type Dialect string

const (
	// SingleStore is the default round-trip target: a copy of the SingleStore
	// source an export was read from. SingleStore does not enforce foreign
	// keys and does not recognise MySQL's FOREIGN_KEY_CHECKS/UNIQUE_CHECKS
	// session variables, so its preamble omits them.
	SingleStore Dialect = "singlestore"
	// MySQL adds FOREIGN_KEY_CHECKS=0 and UNIQUE_CHECKS=0 so a subset export
	// (which has no foreign-key-aware ordering) loads without constraint
	// errors.
	MySQL Dialect = "mysql"
)

// ParseDialect validates a --dialect flag value.
func ParseDialect(s string) (Dialect, error) {
	switch Dialect(s) {
	case SingleStore:
		return SingleStore, nil
	case MySQL:
		return MySQL, nil
	default:
		return "", fmt.Errorf("unknown dialect %q: valid values are %q and %q", s, SingleStore, MySQL)
	}
}

// sessionStatements returns the session-setup statements emitted before any
// INSERTs, in order, excluding transaction control. These configure the
// connection (charset, and for MySQL the constraint-check toggles) and are safe
// to run inside a caller-managed transaction, which is how `sync` applies them.
func (d Dialect) sessionStatements() []string {
	stmts := []string{"SET NAMES utf8mb4;"}
	if d == MySQL {
		stmts = append(stmts, "SET FOREIGN_KEY_CHECKS=0;", "SET UNIQUE_CHECKS=0;")
	}
	return stmts
}

// preamble returns the session statements emitted before any INSERTs, in
// order. START TRANSACTION is included; the matching COMMIT is emitted last.
func (d Dialect) preamble() []string {
	return append(d.sessionStatements(), "START TRANSACTION;")
}

// quoteIdent backtick-quotes a table or column identifier, doubling any
// embedded backtick.
func quoteIdent(name string) string {
	return "`" + strings.ReplaceAll(name, "`", "``") + "`"
}

// quoteString renders a byte string as a single-quoted SQL string literal,
// backslash-escaping the characters MySQL/SingleStore treat specially. Bytes
// outside this set (including multi-byte UTF-8) pass through untouched; the
// SET NAMES utf8mb4 preamble keeps them intact on the wire.
func quoteString(b []byte) string {
	var sb strings.Builder
	sb.Grow(len(b) + 2)
	sb.WriteByte('\'')
	for _, c := range b {
		switch c {
		case 0:
			sb.WriteString(`\0`)
		case '\b':
			sb.WriteString(`\b`)
		case '\n':
			sb.WriteString(`\n`)
		case '\r':
			sb.WriteString(`\r`)
		case '\t':
			sb.WriteString(`\t`)
		case 0x1a:
			sb.WriteString(`\Z`)
		case '\'':
			sb.WriteString(`\'`)
		case '\\':
			sb.WriteString(`\\`)
		default:
			sb.WriteByte(c)
		}
	}
	sb.WriteByte('\'')
	return sb.String()
}

// hexLiteral renders bytes as an X'..' hex literal, which both engines accept
// and which represents the empty blob cleanly as X”.
func hexLiteral(b []byte) string {
	const hex = "0123456789ABCDEF"
	var sb strings.Builder
	sb.Grow(len(b)*2 + 3)
	sb.WriteString("X'")
	for _, c := range b {
		sb.WriteByte(hex[c>>4])
		sb.WriteByte(hex[c&0x0f])
	}
	sb.WriteByte('\'')
	return sb.String()
}

// renderKind is the physical shape a column's values take in Parquet, derived
// from the manifest's recorded parquet_type. It decides how a value becomes a
// SQL literal.
type renderKind int

const (
	renderInt renderKind = iota
	renderDouble
	renderStringLit
	renderHex
)

// kindFor maps a manifest parquet_type string to a renderKind.
func kindFor(parquetType string) (renderKind, error) {
	switch parquetType {
	case "INT64":
		return renderInt, nil
	case "DOUBLE":
		return renderDouble, nil
	case "BYTE_ARRAY(STRING)":
		return renderStringLit, nil
	case "BYTE_ARRAY":
		return renderHex, nil
	default:
		return 0, fmt.Errorf("unsupported parquet_type %q", parquetType)
	}
}

// renderValue turns a single Parquet value into a SQL literal for the given
// render kind. NULL renders as the unquoted keyword NULL regardless of kind.
func renderValue(v parquet.Value, kind renderKind) (string, error) {
	if v.IsNull() {
		return "NULL", nil
	}
	switch kind {
	case renderInt:
		return strconv.FormatInt(v.Int64(), 10), nil
	case renderDouble:
		f := v.Double()
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return "", fmt.Errorf("cannot render non-finite double %v as SQL", f)
		}
		if f == 0 && math.Signbit(f) {
			// FormatFloat renders negative zero as the bare "-0", and "-0.0" is
			// parsed as an exact-value DECIMAL (which has no signed zero) — both
			// reload as positive zero. The float-literal form "-0e0" is the one
			// spelling MySQL/SingleStore parse as an IEEE double, preserving the
			// sign across the round-trip.
			return "-0e0", nil
		}
		return strconv.FormatFloat(f, 'g', -1, 64), nil
	case renderStringLit:
		return quoteString(v.ByteArray()), nil
	case renderHex:
		return hexLiteral(v.ByteArray()), nil
	default:
		return "", fmt.Errorf("unknown render kind %d", kind)
	}
}
