package transform

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/danmatthews/grimnir/internal/value"
)

// hasher computes a keyed, deterministic pseudonym for a value. It is global
// by default: the same input yields the same output everywhere, so foreign-key
// relationships survive. A non-empty group scopes the hash to its own
// namespace to deliberately break accidental collisions.
type hasher struct {
	key   []byte
	group string
}

// digest returns the HMAC-SHA256 of the group-prefixed value as lowercase hex.
// The group is length-prefixed so two (group, value) pairs can never collide by
// concatenation (e.g. group "ab"+value "c" vs group "a"+value "bc").
func (h hasher) digest(v string) string {
	mac := hmac.New(sha256.New, h.key)
	fmt.Fprintf(mac, "%d:%s\x00", len(h.group), h.group)
	mac.Write([]byte(v))
	return hex.EncodeToString(mac.Sum(nil))
}

func requireKey(name string, key []byte) error {
	if len(key) == 0 {
		return fmt.Errorf("%s transform requires a hashing key (hashing.key)", name)
	}
	return nil
}

// newHash produces a hex pseudonym, optionally truncated to Length characters.
func newHash(spec Spec, key []byte) (Transformer, error) {
	if err := requireKey(Hash, key); err != nil {
		return nil, err
	}
	if spec.Length < 0 {
		return nil, fmt.Errorf("hash 'length' must not be negative")
	}
	h := hasher{key: key, group: spec.Group}
	length := spec.Length

	return TransformerFunc(func(v value.Value) (value.Value, error) {
		if v.Null {
			return v, nil
		}
		out := h.digest(v.String())
		if length > 0 && length < len(out) {
			out = out[:length]
		}
		return value.Text(out), nil
	}), nil
}

// newHashEmail produces an email-shaped pseudonym derived deterministically
// from the whole input value, e.g. "3f9a1c2d4e5b6a7c@8d9e0f1a2b.example".
// Because it is derived from the full value, identical emails hash identically
// across tables, preserving joins.
func newHashEmail(spec Spec, key []byte) (Transformer, error) {
	if err := requireKey(HashEmail, key); err != nil {
		return nil, err
	}
	h := hasher{key: key, group: spec.Group}

	return TransformerFunc(func(v value.Value) (value.Value, error) {
		if v.Null {
			return v, nil
		}
		d := h.digest(v.String())
		local := d[:16]
		domain := d[16:26]
		return value.Text(local + "@" + domain + ".example"), nil
	}), nil
}
