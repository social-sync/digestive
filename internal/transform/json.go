package transform

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/social-sync/grimnir/internal/value"
)

// jsonAnonymiser anonymises the values inside a JSON document in place, keeping
// its shape. It is default-deny: every leaf not named in a keep/path rule is
// anonymised by a built-in, type-preserving rule (see leaf). The rule set is
// compiled once (into a segment trie) and reused for every row; only the encode
// buffer is scratch state, which is safe because the export pipeline runs one
// goroutine per table.
type jsonAnonymiser struct {
	root          *trieNode
	defaultString Transformer // hashes non-empty unnamed strings (global namespace)

	buf       bytes.Buffer
	enc       *json.Encoder
	fallbacks int // cells that failed to parse and were redacted whole
}

// trieNode is one segment of the compiled path rules. A node is reached by
// descending object keys (arrays are transparent — see walk). keep passes the
// whole subtree through untouched; xform (from a paths entry) transforms the
// leaf at this node.
type trieNode struct {
	children map[string]*trieNode
	keep     bool
	xform    Transformer
}

func (n *trieNode) child(key string) *trieNode {
	if n == nil {
		return nil
	}
	return n.children[key]
}

// ensure walks/creates the trie path for segs and returns the final node.
func (n *trieNode) ensure(segs []string) *trieNode {
	cur := n
	for _, s := range segs {
		next := cur.children[s]
		if next == nil {
			next = &trieNode{children: map[string]*trieNode{}}
			cur.children[s] = next
		}
		cur = next
	}
	return cur
}

func splitPath(p string) []string { return strings.Split(p, ".") }

// newJSONAnonymise compiles the keep/paths rules into a trie. The HMAC key is
// required because the default rule hashes non-empty strings; leaf transforms
// in paths are built with the same key so hashes share the global namespace.
func newJSONAnonymise(spec Spec, key []byte) (Transformer, error) {
	if err := requireKey(JSONAnonymise, key); err != nil {
		return nil, err
	}
	js := spec.JSON
	if js == nil {
		js = &JSONSpec{}
	}

	root := &trieNode{children: map[string]*trieNode{}}
	for _, p := range js.Keep {
		if p == "" {
			return nil, fmt.Errorf("json_anonymise: empty keep path")
		}
		root.ensure(splitPath(p)).keep = true
	}
	for p, sub := range js.Paths {
		if p == "" {
			return nil, fmt.Errorf("json_anonymise: empty transform path")
		}
		xf, err := Build(sub, key)
		if err != nil {
			return nil, fmt.Errorf("json_anonymise path %q: %w", p, err)
		}
		root.ensure(splitPath(p)).xform = xf
	}

	def, err := newHash(Spec{Transform: Hash}, key)
	if err != nil {
		return nil, err
	}

	j := &jsonAnonymiser{root: root, defaultString: def}
	j.enc = json.NewEncoder(&j.buf)
	j.enc.SetEscapeHTML(false) // don't mangle <, >, & inside string values
	return j, nil
}

// Transform decodes the cell, walks it applying the rules, and re-encodes it. A
// NULL cell passes through. A cell that will not parse is redacted whole (never
// passed through raw) and counted, so the run continues but the fallback is
// visible via FallbackCount.
func (j *jsonAnonymiser) Transform(v value.Value) (value.Value, error) {
	if v.Null {
		return v, nil
	}

	dec := json.NewDecoder(bytes.NewReader(v.Bytes))
	dec.UseNumber() // keep exact numeric text so kept numbers round-trip losslessly
	var doc any
	if err := dec.Decode(&doc); err != nil {
		j.fallbacks++
		return value.Null, nil
	}

	out := j.walk(doc, j.root, false)

	j.buf.Reset()
	if err := j.enc.Encode(out); err != nil {
		j.fallbacks++
		return value.Null, nil
	}
	// Encoder appends a newline; trim it. Copy the bytes out before the buffer
	// is reused on the next row.
	b := bytes.TrimRight(j.buf.Bytes(), "\n")
	return value.Text(string(b)), nil
}

// FallbackCount reports how many cells failed to parse and were redacted whole.
func (j *jsonAnonymiser) FallbackCount() int { return j.fallbacks }

// walk recursively rebuilds the document. Object keys are preserved verbatim
// and their values descend the trie by key. Arrays are transparent to paths:
// each element descends with the *same* node, so `contacts.email` matches the
// email leaf of every element of the contacts array. Anything else is a leaf.
//
// kept carries the keep context down: a `keep` node keeps its whole subtree, but
// a more specific path rule below it still applies (path beats keep). Inside a
// kept subtree the default for unnamed leaves flips to keep, not default-deny.
func (j *jsonAnonymiser) walk(v any, node *trieNode, kept bool) any {
	if node != nil && node.keep {
		kept = true
	}
	// Nothing more specific below: return the (already-decoded) subtree as-is.
	// A node with its own xform is an exception — it must reach leaf().
	if kept && (node == nil || (len(node.children) == 0 && node.xform == nil)) {
		return v
	}
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			out[k] = j.walk(val, node.child(k), kept)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, el := range t {
			out[i] = j.walk(el, node, kept)
		}
		return out
	default:
		return j.leaf(v, node, kept)
	}
}

// leaf anonymises a scalar. A named path transform wins (even under a keep);
// then a keep context passes the value through; otherwise the built-in
// default-deny rule applies, preserving JSON type: null stays null, empty string
// stays empty (no PII in zero bytes, and hashing would fabricate data), a
// non-empty string is hashed, a number becomes 0, a boolean is kept.
func (j *jsonAnonymiser) leaf(v any, node *trieNode, kept bool) any {
	if node != nil && node.xform != nil {
		return applyLeaf(node.xform, v)
	}
	if kept {
		return v
	}
	switch t := v.(type) {
	case string:
		if t == "" {
			return t
		}
		out, err := j.defaultString.Transform(value.Text(t))
		if err != nil || out.Null {
			return nil
		}
		return out.String()
	case json.Number:
		return json.Number("0")
	case bool:
		return t
	default: // nil, and any unexpected type
		return v
	}
}

// applyLeaf runs a named transform over a scalar. The scalar is presented to the
// transform as its textual form (JSON null as SQL NULL), and the result becomes
// a JSON string — except NULL, which stays JSON null. This means naming a number
// with, say, hash yields a string; that is the operator's explicit choice.
func applyLeaf(xf Transformer, v any) any {
	var in value.Value
	switch t := v.(type) {
	case nil:
		in = value.Null
	case string:
		in = value.Text(t)
	case json.Number:
		in = value.Text(t.String())
	case bool:
		if t {
			in = value.Text("true")
		} else {
			in = value.Text("false")
		}
	default:
		in = value.Text(fmt.Sprint(v))
	}
	out, err := xf.Transform(in)
	if err != nil || out.Null {
		return nil
	}
	return out.String()
}
