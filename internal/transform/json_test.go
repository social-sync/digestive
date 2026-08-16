package transform

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/social-sync/grimnir/internal/value"
)

// decode parses JSON into a comparable Go value so tests can assert on structure
// without depending on key ordering.
func decode(t *testing.T, s string) any {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		t.Fatalf("decode %q: %v", s, err)
	}
	return v
}

// numberOf reads the exact numeric text of a top-level key (UseNumber avoids the
// float64 rounding that would hide large-integer corruption).
func numberOf(t *testing.T, doc, key string) string {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(doc))
	dec.UseNumber()
	var m map[string]any
	if err := dec.Decode(&m); err != nil {
		t.Fatalf("decode: %v", err)
	}
	n, ok := m[key].(json.Number)
	if !ok {
		t.Fatalf("key %q is not a number: %T", key, m[key])
	}
	return n.String()
}

func emailShaped(s string) bool { return strings.Contains(s, "@") && strings.HasSuffix(s, ".example") }

func jsonSpec(keep []string, paths map[string]Spec) Spec {
	return Spec{Transform: JSONAnonymise, JSON: &JSONSpec{Keep: keep, Paths: paths}}
}

func TestJSONAnonymiseRequiresKey(t *testing.T) {
	if _, err := Build(Spec{Transform: JSONAnonymise}, nil); err == nil {
		t.Errorf("json_anonymise without key should error")
	}
}

func TestJSONAnonymiseNullCellPassesThrough(t *testing.T) {
	tr := build(t, jsonSpec(nil, nil), []byte("k"))
	if out := apply(t, tr, value.Null); !out.Null {
		t.Errorf("NULL cell should pass through as NULL")
	}
}

// The zero-config default-deny rules on a realistic DTO: strings hashed, empty
// strings and nulls preserved, numbers zeroed, booleans kept, keys preserved.
func TestJSONAnonymiseDefaultDeny(t *testing.T) {
	tr := build(t, jsonSpec(nil, nil), []byte("k"))
	in := `{"firstName":"Daryl","empty":"","phone":null,"dob":1984,` +
		`"consent":true,"nested":{"lastName":"Dunn"},"tags":["a","b"]}`
	got := decode(t, apply(t, tr, value.Text(in)).String()).(map[string]any)

	if got["firstName"] == "Daryl" || got["firstName"] == "" {
		t.Errorf("firstName should be hashed, got %v", got["firstName"])
	}
	if got["empty"] != "" {
		t.Errorf("empty string should be preserved, got %v", got["empty"])
	}
	if got["phone"] != nil {
		t.Errorf("null should be preserved, got %v", got["phone"])
	}
	if got["dob"] != float64(0) {
		t.Errorf("number should be zeroed, got %v", got["dob"])
	}
	if got["consent"] != true {
		t.Errorf("boolean should be kept, got %v", got["consent"])
	}
	if nested := got["nested"].(map[string]any); nested["lastName"] == "Dunn" {
		t.Errorf("nested string should be hashed, got %v", nested["lastName"])
	}
	if tags := got["tags"].([]any); len(tags) != 2 || tags[0] == "a" || tags[1] == "b" {
		t.Errorf("array elements should be hashed, got %v", tags)
	}
}

func TestJSONAnonymiseKeepAndPaths(t *testing.T) {
	tr := build(t, jsonSpec(
		[]string{"details.marketingConsent", "count"},
		map[string]Spec{"details.email": {Transform: HashEmail}},
	), []byte("k"))

	in := `{"details":{"email":"vonaxor@mailinator.com","firstName":"Daryl",` +
		`"marketingConsent":{"email":true,"sms":true}},"count":42}`
	got := decode(t, apply(t, tr, value.Text(in)).String()).(map[string]any)
	details := got["details"].(map[string]any)

	if e, _ := details["email"].(string); e == "vonaxor@mailinator.com" || !emailShaped(e) {
		t.Errorf("email should be email-shaped hash, got %v", details["email"])
	}
	if details["firstName"] == "Daryl" {
		t.Errorf("firstName should be hashed, got %v", details["firstName"])
	}
	mc := details["marketingConsent"].(map[string]any)
	if mc["email"] != true || mc["sms"] != true {
		t.Errorf("kept subtree should be untouched, got %v", mc)
	}
	if got["count"] != float64(42) {
		t.Errorf("kept number should be preserved, got %v", got["count"])
	}
}

// A specific path transform overrides a broader ancestor keep.
func TestJSONAnonymisePathBeatsKeep(t *testing.T) {
	tr := build(t, jsonSpec(
		[]string{"details"},
		map[string]Spec{"details.email": {Transform: HashEmail}},
	), []byte("k"))

	in := `{"details":{"email":"a@b.com","firstName":"Daryl"}}`
	details := decode(t, apply(t, tr, value.Text(in)).String()).(map[string]any)["details"].(map[string]any)

	if details["firstName"] != "Daryl" {
		t.Errorf("kept sibling should be untouched, got %v", details["firstName"])
	}
	if e, _ := details["email"].(string); e == "a@b.com" || !emailShaped(e) {
		t.Errorf("named email should override keep, got %v", details["email"])
	}
}

// Implicit array traversal: a path reaches leaves inside every array element.
func TestJSONAnonymiseArrayTraversal(t *testing.T) {
	tr := build(t, jsonSpec(
		[]string{"contacts.name"},
		map[string]Spec{"contacts.email": {Transform: HashEmail}},
	), []byte("k"))

	in := `{"contacts":[{"name":"Al","email":"al@x.com"},{"name":"Bo","email":"bo@x.com"}]}`
	contacts := decode(t, apply(t, tr, value.Text(in)).String()).(map[string]any)["contacts"].([]any)

	wantNames := []any{"Al", "Bo"}
	for i, raw := range contacts {
		c := raw.(map[string]any)
		if c["name"] != wantNames[i] {
			t.Errorf("element %d name should be kept, got %v", i, c["name"])
		}
		if e, _ := c["email"].(string); !emailShaped(e) {
			t.Errorf("element %d email should be email-shaped, got %v", i, c["email"])
		}
	}
}

// Determinism across the boundary: the same value hashed inside JSON matches the
// same value hashed by a plain scalar hash in the global namespace.
func TestJSONAnonymiseSharesGlobalNamespace(t *testing.T) {
	key := []byte("k")
	scalar := apply(t, build(t, Spec{Transform: Hash}, key), value.Text("Daryl")).String()

	out := apply(t, build(t, jsonSpec(nil, nil), key), value.Text(`{"name":"Daryl"}`)).String()
	if got := decode(t, out).(map[string]any)["name"]; got != scalar {
		t.Errorf("JSON leaf hash %v should match scalar hash %v", got, scalar)
	}
}

func TestJSONAnonymiseFallbackRedactsAndCounts(t *testing.T) {
	tr := build(t, jsonSpec(nil, nil), []byte("k"))
	if out := apply(t, tr, value.Text("not json {")); !out.Null {
		t.Errorf("unparseable cell should be redacted to NULL, got %+v", out)
	}
	fr, ok := tr.(interface{ FallbackCount() int })
	if !ok {
		t.Fatalf("json_anonymise should report fallbacks")
	}
	if fr.FallbackCount() != 1 {
		t.Errorf("fallback count = %d, want 1", fr.FallbackCount())
	}
}

// Non-object roots are handled uniformly as values.
func TestJSONAnonymiseScalarRoot(t *testing.T) {
	tr := build(t, jsonSpec(nil, nil), []byte("k"))
	if out := apply(t, tr, value.Text(`"hello"`)).String(); out == `"hello"` {
		t.Errorf("root string should be hashed, got %s", out)
	}
	if out := apply(t, tr, value.Text(`42`)).String(); out != `0` {
		t.Errorf("root number should be zeroed, got %s", out)
	}
	if out := apply(t, tr, value.Text(`true`)).String(); out != `true` {
		t.Errorf("root boolean should be kept, got %s", out)
	}
}

// A kept large integer survives exactly (json.Number, not lossy float64).
func TestJSONAnonymiseKeptNumberLossless(t *testing.T) {
	tr := build(t, jsonSpec([]string{"id"}, nil), []byte("k"))
	out := apply(t, tr, value.Text(`{"id":9007199254740993}`)).String()
	if got := numberOf(t, out, "id"); got != "9007199254740993" {
		t.Errorf("kept large integer corrupted: got %s (raw %s)", got, out)
	}
}

// Empty-string preservation must not hash "" into a populated value.
func TestJSONAnonymiseEmptyStringPreserved(t *testing.T) {
	tr := build(t, jsonSpec(nil, nil), []byte("k"))
	out := apply(t, tr, value.Text(`{"a":"","b":"x"}`)).String()
	got := decode(t, out).(map[string]any)
	if got["a"] != "" {
		t.Errorf("empty string fabricated a value: %v", got["a"])
	}
	if got["b"] == "" || got["b"] == "x" {
		t.Errorf("non-empty string should be hashed: %v", got["b"])
	}
}
