package cmd

import (
	"strings"
	"testing"
)

func TestConfirmSync(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"y\n", true},
		{"yes\n", true},
		{"Y\n", true},
		{"YES\n", true},
		{"n\n", false},
		{"no\n", false},
		{"\n", false}, // bare enter defaults to no
		{"", false},   // EOF (non-interactive stdin) defaults to no
		{"maybe\n", false},
	}
	for _, c := range cases {
		got, err := confirmSync(strings.NewReader(c.in), "mysql", "db:3306", "appdb")
		if err != nil {
			t.Fatalf("confirmSync(%q): %v", c.in, err)
		}
		if got != c.want {
			t.Errorf("confirmSync(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}
