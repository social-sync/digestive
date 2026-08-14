// Package typemap maps SingleStore / MySQL column types onto Parquet leaf
// types, following ADR-0003: use a native Parquet type where it is safe, and
// fall back to a lossless string or byte-array representation where a native
// type would risk corrupting the value.
//
// The Manifest records, per column, the source type and the physical Parquet
// type chosen here, so reconstruction can interpret values correctly.
package typemap

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/danmatthews/sql-exporter/internal/value"
	"github.com/parquet-go/parquet-go"
)

// Kind is the physical Parquet representation chosen for a column.
type Kind int

const (
	KindInt64 Kind = iota
	KindDouble
	KindString
	KindBytes
)

// Mapping is the result of mapping one source column type.
type Mapping struct {
	Kind Kind
	// Lossless is true when the source type had no safe native Parquet
	// equivalent and was stored as its exact string/byte representation.
	Lossless bool
}

// Physical returns a human-readable name for the Parquet physical type, for
// recording in the Manifest.
func (m Mapping) Physical() string {
	switch m.Kind {
	case KindInt64:
		return "INT64"
	case KindDouble:
		return "DOUBLE"
	case KindString:
		return "BYTE_ARRAY(STRING)"
	case KindBytes:
		return "BYTE_ARRAY"
	default:
		return "UNKNOWN"
	}
}

// Node returns the Parquet schema node for this mapping. All columns are
// optional so NULLs are representable uniformly; nullability is recorded in
// the Manifest.
func (m Mapping) Node() parquet.Node {
	switch m.Kind {
	case KindInt64:
		return parquet.Optional(parquet.Int(64))
	case KindDouble:
		return parquet.Optional(parquet.Leaf(parquet.DoubleType))
	case KindBytes:
		return parquet.Optional(parquet.Leaf(parquet.ByteArrayType))
	default: // KindString
		return parquet.Optional(parquet.String())
	}
}

// Encode converts a raw cell into the Go value the Parquet writer expects for
// this mapping. NULL becomes nil.
func (m Mapping) Encode(v value.Value) (any, error) {
	if v.Null {
		return nil, nil
	}
	switch m.Kind {
	case KindInt64:
		n, err := strconv.ParseInt(strings.TrimSpace(v.String()), 10, 64)
		if err != nil {
			return nil, fmt.Errorf("value %q is not a valid int64: %w", v.String(), err)
		}
		return n, nil
	case KindDouble:
		f, err := strconv.ParseFloat(strings.TrimSpace(v.String()), 64)
		if err != nil {
			return nil, fmt.Errorf("value %q is not a valid float64: %w", v.String(), err)
		}
		return f, nil
	case KindBytes:
		b := make([]byte, len(v.Bytes))
		copy(b, v.Bytes)
		return b, nil
	default: // KindString
		return v.String(), nil
	}
}

// Map chooses a Parquet representation for a source column type. dataType is
// the base type from INFORMATION_SCHEMA.DATA_TYPE (lowercase, no length);
// unsigned reports whether the column type carries the UNSIGNED attribute.
func Map(dataType string, unsigned bool) Mapping {
	switch strings.ToLower(strings.TrimSpace(dataType)) {
	case "tinyint", "smallint", "mediumint", "int", "integer":
		// All fit in int64 even when unsigned.
		return Mapping{Kind: KindInt64}
	case "bigint":
		if unsigned {
			// Unsigned 64-bit exceeds signed INT64 range: keep exact text.
			return Mapping{Kind: KindString, Lossless: true}
		}
		return Mapping{Kind: KindInt64}
	case "float", "double", "real":
		return Mapping{Kind: KindDouble}
	case "decimal", "numeric", "dec", "fixed":
		// Preserve precision/scale exactly as text.
		return Mapping{Kind: KindString, Lossless: true}
	case "char", "varchar", "tinytext", "text", "mediumtext", "longtext",
		"enum", "set":
		return Mapping{Kind: KindString}
	case "date", "datetime", "timestamp", "time", "year":
		// Lossless text avoids zero-date ('0000-00-00') and fractional-second
		// precision problems that a native TIMESTAMP would introduce.
		return Mapping{Kind: KindString, Lossless: true}
	case "json", "vector", "geography", "geographypoint":
		// No native Parquet equivalent: store exact text.
		return Mapping{Kind: KindString, Lossless: true}
	case "binary", "varbinary", "tinyblob", "blob", "mediumblob", "longblob",
		"bit", "bson":
		return Mapping{Kind: KindBytes, Lossless: true}
	default:
		// Unknown type: preserve raw bytes rather than risk corruption.
		return Mapping{Kind: KindBytes, Lossless: true}
	}
}

// IsText reports whether a source type is a textual/string type, used to
// enforce that hashing transforms only target text columns.
func IsText(dataType string) bool {
	switch strings.ToLower(strings.TrimSpace(dataType)) {
	case "char", "varchar", "tinytext", "text", "mediumtext", "longtext",
		"enum", "set":
		return true
	default:
		return false
	}
}
