package transform

import (
	"strings"
	"testing"

	"github.com/social-sync/grimnir/internal/value"
)

func build(t *testing.T, spec Spec, key []byte) Transformer {
	t.Helper()
	tr, err := Build(spec, key)
	if err != nil {
		t.Fatalf("build %+v: %v", spec, err)
	}
	return tr
}

func apply(t *testing.T, tr Transformer, in value.Value) value.Value {
	t.Helper()
	out, err := tr.Transform(in)
	if err != nil {
		t.Fatalf("transform: %v", err)
	}
	return out
}

func TestNull(t *testing.T) {
	tr := build(t, Spec{Transform: Null}, nil)
	out := apply(t, tr, value.Text("anything"))
	if !out.Null {
		t.Errorf("null transform did not null the value: %+v", out)
	}
}

func TestConstant(t *testing.T) {
	tr := build(t, Spec{Transform: Constant, Value: ptr("REDACTED")}, nil)
	if got := apply(t, tr, value.Text("secret")).String(); got != "REDACTED" {
		t.Errorf("got %q", got)
	}
	// NULL stays NULL rather than being fabricated into the constant.
	if out := apply(t, tr, value.Null); !out.Null {
		t.Errorf("constant fabricated a value from NULL")
	}
	if _, err := Build(Spec{Transform: Constant}, nil); err == nil {
		t.Errorf("constant without value should error")
	}
}

func TestMask(t *testing.T) {
	cases := []struct {
		in                  string
		keepFirst, keepLast int
		want                string
	}{
		{"alice", 0, 0, "*****"},
		{"alice", 1, 2, "a**ce"},
		{"12345678", 0, 4, "****5678"},
		{"ab", 2, 2, "ab"},       // kept regions cover whole string
		{"ab", 5, 5, "ab"},       // keep exceeds length: reveal no more than original
		{"héllo", 1, 0, "h****"}, // rune-aware
	}
	for _, tc := range cases {
		tr := build(t, Spec{Transform: Mask, KeepFirst: tc.keepFirst, KeepLast: tc.keepLast}, nil)
		if got := apply(t, tr, value.Text(tc.in)).String(); got != tc.want {
			t.Errorf("mask(%q, %d, %d) = %q, want %q", tc.in, tc.keepFirst, tc.keepLast, got, tc.want)
		}
	}
}

func TestHashDeterministicAndGlobal(t *testing.T) {
	key := []byte("secret-key")
	a := build(t, Spec{Transform: Hash}, key)
	b := build(t, Spec{Transform: Hash}, key)

	// Same input, same key -> same output (across independent instances),
	// which is what lets foreign keys survive across tables.
	out1 := apply(t, a, value.Text("user-42")).String()
	out2 := apply(t, b, value.Text("user-42")).String()
	if out1 != out2 {
		t.Errorf("hash not deterministic across instances: %q vs %q", out1, out2)
	}
	if apply(t, a, value.Text("user-43")).String() == out1 {
		t.Errorf("distinct inputs collided")
	}

	// Different key -> different output.
	c := build(t, Spec{Transform: Hash}, []byte("other-key"))
	if apply(t, c, value.Text("user-42")).String() == out1 {
		t.Errorf("hash ignored the key")
	}

	// NULL passes through.
	if out := apply(t, a, value.Null); !out.Null {
		t.Errorf("hash should leave NULL as NULL")
	}
}

func TestHashGroupsIsolate(t *testing.T) {
	key := []byte("k")
	global := build(t, Spec{Transform: Hash}, key)
	grouped := build(t, Spec{Transform: Hash, Group: "other"}, key)
	if apply(t, global, value.Text("x")).String() == apply(t, grouped, value.Text("x")).String() {
		t.Errorf("group did not isolate the namespace")
	}
}

func TestHashLength(t *testing.T) {
	tr := build(t, Spec{Transform: Hash, Length: 8}, []byte("k"))
	if got := apply(t, tr, value.Text("x")).String(); len(got) != 8 {
		t.Errorf("length not applied: %q (len %d)", got, len(got))
	}
}

func TestHashEmailShape(t *testing.T) {
	key := []byte("k")
	tr := build(t, Spec{Transform: HashEmail}, key)
	got := apply(t, tr, value.Text("alice@example.com")).String()
	if !strings.Contains(got, "@") || !strings.HasSuffix(got, ".example") {
		t.Errorf("not email-shaped: %q", got)
	}
	// Deterministic on the whole value.
	if got != apply(t, build(t, Spec{Transform: HashEmail}, key), value.Text("alice@example.com")).String() {
		t.Errorf("hash_email not deterministic")
	}
}

func TestHashRequiresKey(t *testing.T) {
	if _, err := Build(Spec{Transform: Hash}, nil); err == nil {
		t.Errorf("hash without key should error")
	}
	if _, err := Build(Spec{Transform: HashEmail}, []byte{}); err == nil {
		t.Errorf("hash_email without key should error")
	}
}

func TestUnknownTransform(t *testing.T) {
	if _, err := Build(Spec{Transform: "nope"}, nil); err == nil {
		t.Errorf("unknown transform should error")
	}
}

func ptr(s string) *string { return &s }
