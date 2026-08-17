package restore

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/social-sync/digestive/internal/value"
)

// writeRules writes a restore.yaml into a temp dir and returns its path.
func writeRules(t *testing.T, yaml string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "restore.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatalf("write rules: %v", err)
	}
	return path
}

// usersFixture is a small two-table run used across the rules tests.
func usersFixture(t *testing.T) string {
	return writeFixture(t, map[string][]col{
		"users": {
			{name: "id", dataType: "int", cells: []value.Value{value.Text("1"), value.Text("2")}},
			{name: "full_name", dataType: "varchar", cells: []value.Value{value.Text("Ada"), value.Text("Grace")}},
			{name: "legacy_flag", dataType: "int", cells: []value.Value{value.Text("0"), value.Text("1")}},
		},
	})
}

func TestRulesRenameDropAddColumns(t *testing.T) {
	dir := usersFixture(t)
	rules := writeRules(t, `
tables:
  users:
    rename_columns:
      full_name: display_name
    drop_columns:
      - legacy_flag
    add_columns:
      tenant_id:
        value: 1
      created_at:
        value: "2020-01-01 00:00:00"
`)

	out := runRestore(t, dir, Options{Dialect: SingleStore, RulesPath: rules})

	// Column list: kept columns under emitted names, then added columns sorted.
	if !strings.Contains(out, "INSERT INTO `users` (`id`, `display_name`, `created_at`, `tenant_id`) VALUES") {
		t.Errorf("unexpected column list\n---\n%s", out)
	}
	// Dropped column must be gone entirely.
	if strings.Contains(out, "legacy_flag") {
		t.Errorf("dropped column still present\n---\n%s", out)
	}
	// Added constants repeat on every row; added values are quoted literals.
	if !strings.Contains(out, "(1, 'Ada', '2020-01-01 00:00:00', '1')") {
		t.Errorf("row 1 wrong\n---\n%s", out)
	}
	if !strings.Contains(out, "(2, 'Grace', '2020-01-01 00:00:00', '1')") {
		t.Errorf("row 2 wrong\n---\n%s", out)
	}
}

func TestRulesAddRawAndNull(t *testing.T) {
	dir := usersFixture(t)
	rules := writeRules(t, `
tables:
  users:
    add_columns:
      created_at:
        value: NOW()
        raw: true
      deleted_at:
        value: null
`)

	out := runRestore(t, dir, Options{Dialect: SingleStore, RulesPath: rules})

	// raw splices verbatim (no quotes); null renders as the keyword.
	if !strings.Contains(out, "(1, 'Ada', 0, NOW(), NULL)") {
		t.Errorf("raw/null rendering wrong\n---\n%s", out)
	}
}

func TestRulesRenameTable(t *testing.T) {
	dir := usersFixture(t)
	rules := writeRules(t, `
tables:
  users:
    rename_table: app_users
`)

	out := runRestore(t, dir, Options{Dialect: SingleStore, RulesPath: rules})

	if !strings.Contains(out, "INSERT INTO `app_users`") {
		t.Errorf("expected renamed table\n---\n%s", out)
	}
	if !strings.Contains(out, "-- table: users -> app_users (2 rows)") {
		t.Errorf("expected rename in header comment\n---\n%s", out)
	}
}

func TestRulesDropTable(t *testing.T) {
	dir := usersFixture(t)
	rules := writeRules(t, `
tables:
  users:
    drop_table: true
`)

	out := runRestore(t, dir, Options{Dialect: SingleStore, RulesPath: rules})

	if strings.Contains(out, "INSERT INTO `users`") {
		t.Errorf("dropped table must emit no INSERT\n---\n%s", out)
	}
}

// runRestoreErr runs a restore expecting an error, and returns it.
func runRestoreErr(t *testing.T, dir string, opts Options) error {
	t.Helper()
	var buf bytes.Buffer
	opts.RunDir = dir
	opts.Out = &buf
	err := Run(opts)
	if err == nil {
		t.Fatalf("expected error, got output:\n%s", buf.String())
	}
	return err
}

func TestRulesValidationErrors(t *testing.T) {
	cases := []struct {
		name  string
		rules string
		want  string
	}{
		{
			name:  "table not in manifest",
			rules: "tables:\n  ghosts:\n    drop_table: true\n",
			want:  "not in the manifest",
		},
		{
			name:  "drop nonexistent column",
			rules: "tables:\n  users:\n    drop_columns: [nope]\n",
			want:  "not in the manifest",
		},
		{
			name:  "rename nonexistent column",
			rules: "tables:\n  users:\n    rename_columns:\n      nope: x\n",
			want:  "not in the manifest",
		},
		{
			name:  "add existing column",
			rules: "tables:\n  users:\n    add_columns:\n      full_name:\n        value: x\n",
			want:  "already exists",
		},
		{
			name:  "rename and drop same column",
			rules: "tables:\n  users:\n    rename_columns:\n      full_name: x\n    drop_columns: [full_name]\n",
			want:  "both renames and drops",
		},
		{
			name:  "rename target collides with kept column",
			rules: "tables:\n  users:\n    rename_columns:\n      full_name: id\n",
			want:  "two columns named",
		},
		{
			name:  "add existing kept column",
			rules: "tables:\n  users:\n    add_columns:\n      id:\n        value: 1\n",
			want:  "already exists",
		},
		{
			name:  "drop_table with other rules",
			rules: "tables:\n  users:\n    drop_table: true\n    drop_columns: [id]\n",
			want:  "drop_table alongside other rules",
		},
		{
			name:  "add without value",
			rules: "tables:\n  users:\n    add_columns:\n      x:\n        raw: true\n",
			want:  "must specify a value",
		},
		{
			name:  "drop every column",
			rules: "tables:\n  users:\n    drop_columns: [id, full_name, legacy_flag]\n",
			want:  "nothing to insert",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := usersFixture(t)
			rules := writeRules(t, tc.rules)
			err := runRestoreErr(t, dir, Options{Dialect: SingleStore, RulesPath: rules})
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not contain %q", err.Error(), tc.want)
			}
		})
	}
}

// TestRulesRenameFreesNameForAdd confirms a name can be renamed away and then
// reused by an added column with a different value — a legitimate "replace the
// column's meaning" reconciliation.
func TestRulesRenameFreesNameForAdd(t *testing.T) {
	dir := usersFixture(t)
	rules := writeRules(t, `
tables:
  users:
    rename_columns:
      full_name: display_name
    add_columns:
      full_name:
        value: redacted
`)

	out := runRestore(t, dir, Options{Dialect: SingleStore, RulesPath: rules})

	if !strings.Contains(out, "INSERT INTO `users` (`id`, `display_name`, `legacy_flag`, `full_name`) VALUES") {
		t.Errorf("unexpected column list\n---\n%s", out)
	}
	if !strings.Contains(out, "(1, 'Ada', 0, 'redacted')") {
		t.Errorf("row 1 wrong\n---\n%s", out)
	}
}

func TestRulesTwoTablesSameEmittedName(t *testing.T) {
	dir := writeFixture(t, map[string][]col{
		"a": {{name: "id", dataType: "int", cells: []value.Value{value.Text("1")}}},
		"b": {{name: "id", dataType: "int", cells: []value.Value{value.Text("1")}}},
	})
	rules := writeRules(t, `
tables:
  a:
    rename_table: b
`)

	err := runRestoreErr(t, dir, Options{Dialect: SingleStore, RulesPath: rules})
	if !strings.Contains(err.Error(), "both emit into") {
		t.Errorf("expected emitted-name collision, got: %v", err)
	}
}
