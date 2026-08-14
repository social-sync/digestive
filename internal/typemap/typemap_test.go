package typemap

import (
	"testing"

	"github.com/danmatthews/sql-exporter/internal/value"
)

func TestMap(t *testing.T) {
	cases := []struct {
		dataType     string
		unsigned     bool
		wantKind     Kind
		wantLossless bool
	}{
		{"int", false, KindInt64, false},
		{"bigint", false, KindInt64, false},
		{"bigint", true, KindString, true}, // unsigned 64-bit overflows int64
		{"int", true, KindInt64, false},    // unsigned 32-bit still fits
		{"double", false, KindDouble, false},
		{"decimal", false, KindString, true}, // preserve precision
		{"varchar", false, KindString, false},
		{"json", false, KindString, true},
		{"vector", false, KindString, true},
		{"datetime", false, KindString, true}, // avoid zero-date / precision loss
		{"blob", false, KindBytes, true},
		{"mysterytype", false, KindBytes, true}, // unknown -> preserve bytes
	}
	for _, tc := range cases {
		m := Map(tc.dataType, tc.unsigned)
		if m.Kind != tc.wantKind || m.Lossless != tc.wantLossless {
			t.Errorf("Map(%q, unsigned=%v) = {%v, lossless=%v}, want {%v, lossless=%v}",
				tc.dataType, tc.unsigned, m.Kind, m.Lossless, tc.wantKind, tc.wantLossless)
		}
	}
}

func TestEncode(t *testing.T) {
	if got, _ := Map("int", false).Encode(value.Text("42")); got != int64(42) {
		t.Errorf("int encode = %v (%T)", got, got)
	}
	if got, _ := Map("double", false).Encode(value.Text("3.5")); got != 3.5 {
		t.Errorf("double encode = %v", got)
	}
	if got, _ := Map("varchar", false).Encode(value.Text("hi")); got != "hi" {
		t.Errorf("string encode = %v", got)
	}
	if got, _ := Map("int", false).Encode(value.Null); got != nil {
		t.Errorf("null encode = %v, want nil", got)
	}
	if _, err := Map("int", false).Encode(value.Text("not-a-number")); err == nil {
		t.Errorf("expected error encoding non-numeric into int64")
	}
	if got, _ := Map("blob", false).Encode(value.Value{Bytes: []byte{0x01, 0x02}}); string(got.([]byte)) != "\x01\x02" {
		t.Errorf("bytes encode = %v", got)
	}
}

func TestIsText(t *testing.T) {
	if !IsText("varchar") || !IsText("text") || !IsText("enum") {
		t.Errorf("text types not recognised")
	}
	if IsText("int") || IsText("json") || IsText("blob") {
		t.Errorf("non-text types misclassified as text")
	}
}
