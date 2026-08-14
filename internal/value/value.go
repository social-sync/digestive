// Package value defines the raw cell representation shared across the export
// pipeline. Every value read from a Source arrives as its raw bytes (or NULL);
// transforms and type-mapping operate on this common shape.
package value

// Value is a single raw cell read from a Source. Bytes holds the value's
// textual/binary representation as it came off the wire; Null is true when the
// source value was SQL NULL (in which case Bytes is nil and meaningless).
type Value struct {
	Bytes []byte
	Null  bool
}

// Null is the SQL NULL value.
var Null = Value{Null: true}

// String returns the value's bytes as a string. It returns "" for NULL, so
// callers that care about NULL must check Null first.
func (v Value) String() string {
	if v.Null {
		return ""
	}
	return string(v.Bytes)
}

// Text builds a non-null Value from a string.
func Text(s string) Value {
	return Value{Bytes: []byte(s)}
}
