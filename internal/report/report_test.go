package report

import (
	"context"
	"strings"
	"testing"

	"github.com/social-sync/digestive/internal/config"
	"github.com/social-sync/digestive/internal/source"
)

// fakeSource serves a fixed schema per table, enough to build a report model.
type fakeSource struct {
	cols map[string][]source.Column
}

func (f *fakeSource) Ping(context.Context) error { return nil }
func (f *fakeSource) Close() error               { return nil }
func (f *fakeSource) Columns(_ context.Context, table string) ([]source.Column, error) {
	return f.cols[table], nil
}
func (f *fakeSource) Query(context.Context, source.QuerySpec) (source.Rows, error) {
	panic("not used")
}

func strptr(s string) *string { return &s }

// sampleConfig exercises every posture: pass-through, each transform, and
// exclusion, plus a live column with no config entry.
func sampleConfig() *config.Config {
	return &config.Config{
		Tables: []config.TableConfig{{
			Name: "users",
			Columns: map[string]config.ColumnConfig{
				"email":      {Transform: "hash_email"},
				"phone":      {Transform: "mask", KeepFirst: 2, KeepLast: 2},
				"status":     {Transform: "constant", Value: strptr("REDACTED")},
				"token":      {Transform: "hash", Length: 16},
				"profile":    {Transform: "json_anonymise", JSON: &config.JSONConfig{Paths: map[string]config.ColumnConfig{"a": {}, "b": {}}}},
				"deleted_at": {Transform: "null"},
				"internal":   {Exclude: true},
			},
		}},
	}
}

func sampleSource() *fakeSource {
	return &fakeSource{cols: map[string][]source.Column{
		"users": {
			{Name: "id"}, // no config entry -> pass-through
			{Name: "email"},
			{Name: "phone"},
			{Name: "status"},
			{Name: "token"},
			{Name: "profile"},
			{Name: "deleted_at"},
			{Name: "internal"},
		},
	}}
}

func TestBuildModel(t *testing.T) {
	rep, err := Build(context.Background(), sampleSource(), sampleConfig())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(rep.Tables) != 1 {
		t.Fatalf("want 1 table, got %d", len(rep.Tables))
	}
	got := map[string]Column{}
	for _, c := range rep.Tables[0].Columns {
		got[c.Name] = c
	}

	// Live column ordering is preserved.
	if rep.Tables[0].Columns[0].Name != "id" {
		t.Errorf("want first column id, got %q", rep.Tables[0].Columns[0].Name)
	}

	cases := []struct {
		col    string
		state  State
		method string
	}{
		{"id", NotAnonymised, ""},
		{"email", Anonymised, "hash_email"},
		{"phone", Anonymised, "mask (keep first 2, last 2)"},
		{"status", Anonymised, `constant ("REDACTED")`},
		{"token", Anonymised, "hash (length 16)"},
		{"profile", Anonymised, "json_anonymise (2 paths)"},
		{"deleted_at", Anonymised, "null"},
		{"internal", Excluded, ""},
	}
	for _, tc := range cases {
		c, ok := got[tc.col]
		if !ok {
			t.Errorf("%s: missing from report", tc.col)
			continue
		}
		if c.State != tc.state {
			t.Errorf("%s: state = %v, want %v", tc.col, c.State, tc.state)
		}
		if c.Method != tc.method {
			t.Errorf("%s: method = %q, want %q", tc.col, c.Method, tc.method)
		}
	}
}

func TestSummaryPluralisation(t *testing.T) {
	if s := (Report{Tables: []Table{{}}}).Summary(); s != "1 table." {
		t.Errorf("one table summary = %q", s)
	}
	if s := (Report{Tables: []Table{{}, {}}}).Summary(); s != "2 tables." {
		t.Errorf("two table summary = %q", s)
	}
}

func TestParseFormat(t *testing.T) {
	for _, in := range []string{"html", "HTML", " html "} {
		if f, err := ParseFormat(in); err != nil || f != HTML {
			t.Errorf("ParseFormat(%q) = %v, %v", in, f, err)
		}
	}
	for _, in := range []string{"markdown", "md", "MarkDown"} {
		if f, err := ParseFormat(in); err != nil || f != Markdown {
			t.Errorf("ParseFormat(%q) = %v, %v", in, f, err)
		}
	}
	if _, err := ParseFormat("pdf"); err == nil {
		t.Error("ParseFormat(pdf): want error")
	}
}

func TestRenderMarkdown(t *testing.T) {
	rep, _ := Build(context.Background(), sampleSource(), sampleConfig())
	out := Render(rep, Markdown)

	for _, want := range []string{
		"# Anonymisation Report",
		"1 table.",
		"## users",
		"| Column | Anonymised? | Method |",
		"| id | No | — |",
		"| email | Yes | hash_email |",
		"| internal | Excluded | — |",
		"| status | Yes | constant (\"REDACTED\") |",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("markdown missing %q\n---\n%s", want, out)
		}
	}
}

func TestRenderHTML(t *testing.T) {
	rep, _ := Build(context.Background(), sampleSource(), sampleConfig())
	out := Render(rep, HTML)

	for _, want := range []string{
		"<!doctype html>",
		"<title>Anonymisation Report</title>",
		"<h2>users</h2>",
		`<td class="no">No</td>`,
		`<td class="yes">Yes</td>`,
		`<td class="excluded">Excluded</td>`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("html missing %q\n---\n%s", want, out)
		}
	}
	// Pass-through rows are flagged so leaks stand out.
	if !strings.Contains(out, `<tr class="leak"><td>id</td>`) {
		t.Errorf("html should flag the id row as a leak\n---\n%s", out)
	}
	// Anonymised rows are not flagged.
	if strings.Contains(out, `<tr class="leak"><td>email</td>`) {
		t.Errorf("html should not flag the email row as a leak\n---\n%s", out)
	}
}

func TestHTMLEscaping(t *testing.T) {
	src := &fakeSource{cols: map[string][]source.Column{"t": {{Name: "note"}}}}
	cfg := &config.Config{Tables: []config.TableConfig{{
		Name:    "t",
		Columns: map[string]config.ColumnConfig{"note": {Transform: "constant", Value: strptr("<script>")}},
	}}}
	rep, _ := Build(context.Background(), src, cfg)
	out := Render(rep, HTML)
	if strings.Contains(out, "<script>") {
		t.Errorf("html did not escape the constant value\n---\n%s", out)
	}
}
