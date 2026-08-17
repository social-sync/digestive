package restore

import (
	"fmt"
	"os"
	"sort"

	"github.com/social-sync/digestive/internal/manifest"
	"gopkg.in/yaml.v3"
)

// DefaultRulesFile is the restore-rules file auto-discovered in the working
// directory. It lives with the local application and its migrations, not with
// the export artifact.
const DefaultRulesFile = "restore.yaml"

// Rules is a parsed restore.yaml: per-table schema-reconciliation rules that
// bridge drift between an export and a drifted target so the emitted INSERTs
// load. It is purely declarative — restore never connects to the target, so a
// rule that contradicts the *target* (rather than the manifest) cannot be
// detected here; it surfaces as a database error at load time. Every rule is
// validated against the manifest, and any rule that matches nothing (a typo or
// a stale rule) is a hard error rather than a silent no-op. See
// docs/adr/0006-restore-schema-reconciliation.md.
type Rules struct {
	Tables map[string]TableRules `yaml:"tables"`
}

// TableRules are the reconciliation rules for one table.
type TableRules struct {
	// RenameTable emits INSERTs into this name instead of the manifest name.
	RenameTable string `yaml:"rename_table"`
	// DropTable skips the table's INSERTs entirely. It cannot be combined with
	// any other rule for the same table.
	DropTable bool `yaml:"drop_table"`
	// RenameColumns maps a manifest column name to the target column name.
	RenameColumns map[string]string `yaml:"rename_columns"`
	// DropColumns omits these manifest columns from the INSERT.
	DropColumns []string `yaml:"drop_columns"`
	// AddColumns appends columns absent from the export, each with a constant
	// value, to satisfy a target column the export predates (typically one that
	// is non-null with no default). Emitted in sorted order for deterministic
	// output.
	AddColumns map[string]AddColumn `yaml:"add_columns"`
}

// AddColumn is the constant value for one added column.
type AddColumn struct {
	// Value is the value node exactly as written in YAML. A missing node is an
	// error (an add must state its value). An explicit null renders as the SQL
	// NULL keyword; any other scalar renders as a quoted string literal the
	// engine coerces into the real column type (matching how lossless types
	// round-trip, ADR-0005), unless Raw is set.
	Value yaml.Node `yaml:"value"`
	// Raw splices Value verbatim as a SQL expression (e.g. NOW()) instead of
	// quoting it. It is trusted config, like the export `where` fragment.
	Raw bool `yaml:"raw"`
}

// LoadRules reads and parses a restore.yaml from path.
func LoadRules(path string) (*Rules, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read restore rules: %w", err)
	}
	var r Rules
	if err := yaml.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("parse restore rules %s: %w", path, err)
	}
	return &r, nil
}

// forTable returns the rules for a table, or the zero value (a no-op) if none
// are declared.
func (r *Rules) forTable(name string) TableRules {
	if r == nil {
		return TableRules{}
	}
	return r.Tables[name]
}

// validate checks every rule against the manifest and rejects contradictions.
// It is deliberately strict: a rule that applies to nothing, or that would
// produce structurally invalid SQL (two columns with one name, a table with no
// columns), is an operator error worth stopping for, because restore cannot see
// the target and a silent no-op here defeats the purpose.
func (r *Rules) validate(man *manifest.Manifest) error {
	if r == nil {
		return nil
	}

	manTables := make(map[string]manifest.Table, len(man.Tables))
	manCols := make(map[string]map[string]bool, len(man.Tables))
	for _, t := range man.Tables {
		manTables[t.Name] = t
		cols := make(map[string]bool, len(t.Columns))
		for _, c := range t.Columns {
			cols[c.Name] = true
		}
		manCols[t.Name] = cols
	}

	// Detect two source tables emitting into the same target name (a rename
	// colliding with another table's emitted name). Considers every manifest
	// table, not just those with rules.
	emitted := make(map[string]string, len(man.Tables))
	for _, t := range man.Tables {
		tr := r.forTable(t.Name)
		if tr.DropTable {
			continue
		}
		name := t.Name
		if tr.RenameTable != "" {
			name = tr.RenameTable
		}
		if src, dup := emitted[name]; dup {
			return fmt.Errorf("restore rules: tables %q and %q both emit into %q", src, t.Name, name)
		}
		emitted[name] = t.Name
	}

	// Per-table rules, in sorted order for stable error messages.
	for _, tname := range sortedKeys(r.Tables) {
		tr := r.Tables[tname]
		cols, ok := manCols[tname]
		if !ok {
			return fmt.Errorf("restore rules: table %q is not in the manifest (typo or stale rule)", tname)
		}

		if tr.DropTable {
			if tr.RenameTable != "" || len(tr.RenameColumns) > 0 || len(tr.DropColumns) > 0 || len(tr.AddColumns) > 0 {
				return fmt.Errorf("restore rules: table %q sets drop_table alongside other rules; drop_table drops the whole table, so the rest is meaningless", tname)
			}
			continue
		}

		// vacated tracks manifest names freed by a drop or a rename, so an add
		// may legitimately reuse one (dropping/renaming X, then adding a fresh X
		// with a different value).
		dropped := make(map[string]bool, len(tr.DropColumns))
		vacated := make(map[string]bool, len(tr.DropColumns)+len(tr.RenameColumns))
		for _, c := range tr.DropColumns {
			if !cols[c] {
				return fmt.Errorf("restore rules: table %q drops column %q, which is not in the manifest", tname, c)
			}
			dropped[c] = true
			vacated[c] = true
		}

		for _, src := range sortedKeys(tr.RenameColumns) {
			dst := tr.RenameColumns[src]
			if !cols[src] {
				return fmt.Errorf("restore rules: table %q renames column %q, which is not in the manifest", tname, src)
			}
			if dropped[src] {
				return fmt.Errorf("restore rules: table %q both renames and drops column %q", tname, src)
			}
			if dst == "" {
				return fmt.Errorf("restore rules: table %q renames column %q to an empty name", tname, src)
			}
			vacated[src] = true
		}

		for _, name := range sortedKeys(tr.AddColumns) {
			if cols[name] && !vacated[name] {
				return fmt.Errorf("restore rules: table %q adds column %q, which already exists in the manifest (use rename_columns, not add_columns)", tname, name)
			}
			if err := tr.AddColumns[name].validate(); err != nil {
				return fmt.Errorf("restore rules: table %q add column %q: %w", tname, name, err)
			}
		}

		// The emitted column set: surviving columns under their emitted names,
		// then added columns. Two columns sharing a name is invalid SQL, and a
		// table left with no columns cannot be inserted into.
		seen := make(map[string]bool)
		for _, c := range manTables[tname].Columns {
			if dropped[c.Name] {
				continue
			}
			emit := c.Name
			if dst, ok := tr.RenameColumns[c.Name]; ok {
				emit = dst
			}
			if seen[emit] {
				return fmt.Errorf("restore rules: table %q would emit two columns named %q", tname, emit)
			}
			seen[emit] = true
		}
		for name := range tr.AddColumns {
			if seen[name] {
				return fmt.Errorf("restore rules: table %q would emit two columns named %q (add_columns collides with a kept column)", tname, name)
			}
			seen[name] = true
		}
		if len(seen) == 0 {
			return fmt.Errorf("restore rules: table %q drops every column and adds none, leaving nothing to insert", tname)
		}
	}

	return nil
}

// validate checks a single added column's value node.
func (a AddColumn) validate() error {
	if a.Value.Kind == 0 {
		return fmt.Errorf("must specify a value (use `value: null` for an explicit NULL)")
	}
	if a.Value.Kind != yaml.ScalarNode {
		return fmt.Errorf("value must be a scalar")
	}
	return nil
}

// literal renders the added column's value as a SQL literal. An explicit null
// becomes the NULL keyword; a raw value is spliced verbatim; anything else is a
// quoted string literal the engine coerces.
func (a AddColumn) literal() string {
	if a.Value.Tag == "!!null" {
		return "NULL"
	}
	if a.Raw {
		return a.Value.Value
	}
	return quoteString([]byte(a.Value.Value))
}

// sortedKeys returns the keys of a string-keyed map in sorted order, for
// deterministic iteration.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
