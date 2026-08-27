// Package report builds and renders an anonymisation report: for every
// whitelisted table it lists each live column, whether that column is
// anonymised, excluded, or passing through untouched, and — when anonymised —
// which transform does it. The model is pure (no database) so it can be
// rendered and tested without a connection; Build is the only part that reads
// the live schema.
package report

import (
	"context"
	"fmt"
	"strings"

	"github.com/social-sync/digestive/internal/config"
	"github.com/social-sync/digestive/internal/source"
)

// State is a column's anonymisation posture in the report.
type State int

const (
	// NotAnonymised means the column is exported as-is, with no transform.
	NotAnonymised State = iota
	// Anonymised means a transform is applied before export.
	Anonymised
	// Excluded means the column is dropped from the export entirely.
	Excluded
)

// Column is one row of a table's report: a live column and its posture.
type Column struct {
	Name   string
	State  State
	Method string // transform summary; empty unless State is Anonymised
}

// Table is one section of the report: a whitelisted table and its columns, in
// live schema order.
type Table struct {
	Name    string
	Columns []Column
}

// Report is the whole document model: every configured table, in config order.
type Report struct {
	Tables []Table
}

// Build reads the live schema for every configured table and cross-references it
// with the config to produce the report model. It assumes the config has
// already been validated against the schema (the caller runs export.Validate
// first), so it never re-checks transform legality — it only describes.
func Build(ctx context.Context, src source.Source, cfg *config.Config) (Report, error) {
	var rep Report
	for _, t := range cfg.Tables {
		cols, err := src.Columns(ctx, t.Name)
		if err != nil {
			return Report{}, err
		}
		tbl := Table{Name: t.Name}
		for _, c := range cols {
			cc, configured := t.Columns[c.Name]
			col := Column{Name: c.Name}
			switch {
			case configured && cc.Exclude:
				col.State = Excluded
			case configured && cc.Transform != "":
				col.State = Anonymised
				col.Method = describeMethod(cc)
			default:
				col.State = NotAnonymised
			}
			tbl.Columns = append(tbl.Columns, col)
		}
		rep.Tables = append(rep.Tables, tbl)
	}
	return rep, nil
}

// Summary is the one-line header describing the report's scope.
func (r Report) Summary() string {
	n := len(r.Tables)
	if n == 1 {
		return "1 table."
	}
	return fmt.Sprintf("%d tables.", n)
}

// NotAnonymised reports whether the column passes through untouched, used to
// highlight potential leaks in the rendered output.
func (c Column) NotAnonymised() bool { return c.State == NotAnonymised }

// StateLabel is the human-readable posture shown in the Anonymised? cell.
func (c Column) StateLabel() string {
	switch c.State {
	case Anonymised:
		return "Yes"
	case Excluded:
		return "Excluded"
	default:
		return "No"
	}
}

// StateClass is the CSS class the HTML renderer applies to the state cell.
func (c Column) StateClass() string {
	switch c.State {
	case Anonymised:
		return "yes"
	case Excluded:
		return "excluded"
	default:
		return "no"
	}
}

// MethodLabel is the Method cell text: the transform summary, or an em dash when
// the column is not anonymised (or excluded, which has no method).
func (c Column) MethodLabel() string {
	if c.Method == "" {
		return "—"
	}
	return c.Method
}

// describeMethod renders a concise summary of the transform on a column: the
// transform name plus its salient parameters, so a reviewer sees the "how"
// without the report becoming a config dump.
func describeMethod(cc config.ColumnConfig) string {
	switch cc.Transform {
	case "constant":
		if cc.Value != nil {
			return fmt.Sprintf("constant (%q)", *cc.Value)
		}
		return "constant"
	case "mask":
		var parts []string
		if cc.KeepFirst > 0 {
			parts = append(parts, fmt.Sprintf("keep first %d", cc.KeepFirst))
		}
		if cc.KeepLast > 0 {
			parts = append(parts, fmt.Sprintf("last %d", cc.KeepLast))
		}
		if len(parts) == 0 {
			return "mask"
		}
		return fmt.Sprintf("mask (%s)", strings.Join(parts, ", "))
	case "hash", "hash_email":
		if cc.Length > 0 {
			return fmt.Sprintf("%s (length %d)", cc.Transform, cc.Length)
		}
		return cc.Transform
	case "json_anonymise":
		n := 0
		if cc.JSON != nil {
			n = len(cc.JSON.Paths)
		}
		if n == 1 {
			return "json_anonymise (1 path)"
		}
		return fmt.Sprintf("json_anonymise (%d paths)", n)
	default:
		return cc.Transform
	}
}
