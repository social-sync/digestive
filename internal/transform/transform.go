// Package transform holds the column transformation catalogue: redaction
// (null, constant, mask) and deterministic hashing (hash, hash_email).
//
// A Transformer maps one raw cell to another. Transforms operate on the
// textual form of a value and return a textual form (or NULL); the type-
// mapping layer is responsible for turning that back into a typed value for
// the storage format.
package transform

import (
	"fmt"

	"github.com/danmatthews/sql-exporter/internal/value"
)

// Names of the built-in transforms, as written in config.
const (
	Null      = "null"
	Constant  = "constant"
	Mask      = "mask"
	Hash      = "hash"
	HashEmail = "hash_email"
)

// Transformer converts one cell value to another.
type Transformer interface {
	Transform(value.Value) (value.Value, error)
}

// TransformerFunc adapts a function to Transformer.
type TransformerFunc func(value.Value) (value.Value, error)

func (f TransformerFunc) Transform(v value.Value) (value.Value, error) { return f(v) }

// Spec is the minimal, transform-agnostic description needed to build a
// Transformer, decoupling this package from the config structs.
type Spec struct {
	Transform string
	Value     *string // constant
	KeepFirst int     // mask
	KeepLast  int     // mask
	MaskChar  string  // mask
	Group     string  // hash / hash_email
	Length    int     // hash / hash_email
}

// Build constructs a Transformer from a Spec. hashKey is the HMAC secret; it
// must be non-empty when the spec is a hashing transform.
func Build(spec Spec, hashKey []byte) (Transformer, error) {
	switch spec.Transform {
	case Null:
		return newNull(), nil
	case Constant:
		return newConstant(spec)
	case Mask:
		return newMask(spec)
	case Hash:
		return newHash(spec, hashKey)
	case HashEmail:
		return newHashEmail(spec, hashKey)
	case "":
		return nil, fmt.Errorf("no transform specified")
	default:
		return nil, fmt.Errorf("unknown transform %q", spec.Transform)
	}
}

// RequiresHashKey reports whether a transform needs the HMAC secret.
func RequiresHashKey(name string) bool {
	return name == Hash || name == HashEmail
}

// IsTextOnly reports whether a transform may only be applied to text columns.
func IsTextOnly(name string) bool {
	return name == Mask || name == Hash || name == HashEmail
}

// Known reports whether name is a built-in transform.
func Known(name string) bool {
	switch name {
	case Null, Constant, Mask, Hash, HashEmail:
		return true
	default:
		return false
	}
}
