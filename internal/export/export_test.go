package export

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/danmatthews/grimnir/internal/config"
	"github.com/danmatthews/grimnir/internal/manifest"
	"github.com/danmatthews/grimnir/internal/source"
	"github.com/danmatthews/grimnir/internal/value"
	"github.com/parquet-go/parquet-go"
)

// fakeSource is an in-memory Source for testing the pipeline end to end.
type fakeSource struct {
	cols map[string][]source.Column
	rows map[string][][]value.Value
}

func (f *fakeSource) Ping(context.Context) error { return nil }
func (f *fakeSource) Close() error               { return nil }

func (f *fakeSource) Columns(_ context.Context, table string) ([]source.Column, error) {
	c, ok := f.cols[table]
	if !ok {
		return nil, &notFound{table}
	}
	return c, nil
}

func (f *fakeSource) Query(_ context.Context, spec source.QuerySpec) (source.Rows, error) {
	// Project stored rows down to the requested columns, mirroring a real
	// SELECT that only reads the planned (non-excluded) columns.
	full := f.cols[spec.Table]
	idx := make([]int, len(spec.Columns))
	for i, name := range spec.Columns {
		idx[i] = -1
		for j, c := range full {
			if c.Name == name {
				idx[i] = j
				break
			}
		}
	}
	data := f.rows[spec.Table]
	if spec.Limit != nil && *spec.Limit < len(data) {
		data = data[:*spec.Limit]
	}
	return &fakeRows{data: data, idx: idx, i: -1}, nil
}

type notFound struct{ table string }

func (e *notFound) Error() string { return "table not found: " + e.table }

type fakeRows struct {
	data [][]value.Value
	idx  []int // stored-column index for each requested column
	i    int
}

func (r *fakeRows) Next() bool { r.i++; return r.i < len(r.data) }
func (r *fakeRows) Scan() ([]value.Value, error) {
	row := r.data[r.i]
	out := make([]value.Value, len(r.idx))
	for k, j := range r.idx {
		out[k] = row[j]
	}
	return out, nil
}
func (r *fakeRows) Err() error   { return nil }
func (r *fakeRows) Close() error { return nil }

func text(s string) value.Value { return value.Text(s) }

func TestRunRoundTrip(t *testing.T) {
	src := &fakeSource{
		cols: map[string][]source.Column{
			"users": {
				{Name: "id", DataType: "bigint", Nullable: false},
				{Name: "email", DataType: "varchar", Nullable: false},
				{Name: "notes", DataType: "text", Nullable: true},
			},
			"orders": {
				{Name: "id", DataType: "bigint", Nullable: false},
				{Name: "user_email", DataType: "varchar", Nullable: false},
			},
		},
		rows: map[string][][]value.Value{
			"users": {
				{text("1"), text("alice@x.com"), text("vip")},
				{text("2"), text("bob@x.com"), value.Null},
			},
			"orders": {
				{text("10"), text("alice@x.com")},
			},
		},
	}

	dir := t.TempDir()
	cfg := &config.Config{
		Destination: config.DestinationConfig{Directory: dir},
		Hashing:     config.HashingConfig{Key: "secret"},
		Tables: []config.TableConfig{
			{Name: "users", Columns: map[string]config.ColumnConfig{
				"email": {Transform: "hash_email"},
				"notes": {Transform: "null"},
			}},
			{Name: "orders", Columns: map[string]config.ColumnConfig{
				"user_email": {Transform: "hash_email"},
			}},
		},
	}

	runDir, err := Run(context.Background(), src, cfg, Options{
		RunName: "testrun",
		Now:     time.Unix(0, 0),
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	users := readParquet(t, filepath.Join(runDir, "users.parquet"))
	orders := readParquet(t, filepath.Join(runDir, "orders.parquet"))

	if len(users) != 2 || len(orders) != 1 {
		t.Fatalf("row counts: users=%d orders=%d", len(users), len(orders))
	}

	// id preserved as a native int64.
	if id, ok := users[0]["id"].(int64); !ok || id != 1 {
		t.Errorf("users.id not int64(1): %v (%T)", users[0]["id"], users[0]["id"])
	}
	// email is anonymised (not the original).
	if users[0]["email"] == "alice@x.com" {
		t.Errorf("email was not anonymised")
	}
	// notes redacted to NULL.
	if users[1]["notes"] != nil {
		t.Errorf("notes not nulled: %v", users[1]["notes"])
	}

	// FK survival: alice's email hashes identically in both tables.
	if users[0]["email"] != orders[0]["user_email"] {
		t.Errorf("shared email did not hash consistently across tables: %v vs %v",
			users[0]["email"], orders[0]["user_email"])
	}
	// Distinct emails must not collide.
	if users[0]["email"] == users[1]["email"] {
		t.Errorf("distinct emails collided")
	}

	// Manifest is complete and records the transforms + types.
	man := readManifest(t, filepath.Join(runDir, "manifest.json"))
	if !man.Complete {
		t.Errorf("manifest not marked complete")
	}
	if len(man.Tables) != 2 {
		t.Fatalf("manifest tables = %d", len(man.Tables))
	}
	usersManifest := man.Tables[0]
	col := columnByName(usersManifest.Columns, "email")
	if col == nil || col.Transform != "hash_email" {
		t.Errorf("manifest missing email transform: %+v", usersManifest.Columns)
	}
	if idCol := columnByName(usersManifest.Columns, "id"); idCol == nil || idCol.ParquetType != "INT64" {
		t.Errorf("manifest id parquet type wrong: %+v", idCol)
	}
}

func TestRunDeleteOnFailure(t *testing.T) {
	// A table referenced in config but absent from the source fails the run.
	src := &fakeSource{cols: map[string][]source.Column{}, rows: map[string][][]value.Value{}}
	dir := t.TempDir()
	cfg := &config.Config{
		Destination: config.DestinationConfig{Directory: dir},
		Tables:      []config.TableConfig{{Name: "ghost"}},
	}
	runDir := filepath.Join(dir, "run")
	_, err := Run(context.Background(), src, cfg, Options{RunName: "run", Now: time.Unix(0, 0), DeleteOnFailure: true})
	if err == nil {
		t.Fatal("expected failure")
	}
	if _, statErr := os.Stat(runDir); !os.IsNotExist(statErr) {
		t.Errorf("run directory should have been deleted, stat err = %v", statErr)
	}
}

func TestValidateCatchesTextOnlyTransform(t *testing.T) {
	src := &fakeSource{
		cols: map[string][]source.Column{
			"users": {{Name: "id", DataType: "bigint", Nullable: false}},
		},
		rows: map[string][][]value.Value{},
	}
	cfg := &config.Config{
		Hashing: config.HashingConfig{Key: "k"},
		Tables: []config.TableConfig{
			{Name: "users", Columns: map[string]config.ColumnConfig{
				"id": {Transform: "hash"}, // hashing a non-text column
			}},
		},
	}
	if err := Validate(context.Background(), src, cfg); err == nil {
		t.Fatal("expected validation error for hashing a non-text column")
	}
}

func TestValidateCatchesJSONOnlyTransform(t *testing.T) {
	src := &fakeSource{
		cols: map[string][]source.Column{
			"users": {{Name: "notes", DataType: "text", Nullable: true}},
		},
		rows: map[string][][]value.Value{},
	}
	cfg := &config.Config{
		Hashing: config.HashingConfig{Key: "k"},
		Tables: []config.TableConfig{
			{Name: "users", Columns: map[string]config.ColumnConfig{
				"notes": {Transform: "json_anonymise"}, // a text column, not json
			}},
		},
	}
	if err := Validate(context.Background(), src, cfg); err == nil {
		t.Fatal("expected validation error for json_anonymise on a non-json column")
	}
}

func TestRunJSONAnonymise(t *testing.T) {
	blob := `{"details":{"firstName":"Daryl","email":"vonaxor@mailinator.com",` +
		`"marketingConsent":{"email":true},"dob":1984,"note":""},"facebookUser":null}`
	src := &fakeSource{
		cols: map[string][]source.Column{
			"registrations": {
				{Name: "id", DataType: "bigint", Nullable: false},
				{Name: "payload", DataType: "json", Nullable: false},
			},
		},
		rows: map[string][][]value.Value{
			"registrations": {{text("1"), text(blob)}},
		},
	}
	dir := t.TempDir()
	cfg := &config.Config{
		Destination: config.DestinationConfig{Directory: dir},
		Hashing:     config.HashingConfig{Key: "secret"},
		Tables: []config.TableConfig{
			{Name: "registrations", Columns: map[string]config.ColumnConfig{
				"payload": {
					Transform: "json_anonymise",
					JSON: &config.JSONConfig{
						Keep: []string{"details.marketingConsent"},
						Paths: map[string]config.ColumnConfig{
							"details.email": {Transform: "hash_email"},
						},
					},
				},
			}},
		},
	}

	runDir, err := Run(context.Background(), src, cfg, Options{RunName: "r", Now: time.Unix(0, 0)})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	rows := readParquet(t, filepath.Join(runDir, "registrations.parquet"))
	if len(rows) != 1 {
		t.Fatalf("rows = %d", len(rows))
	}

	var doc map[string]any
	if err := json.Unmarshal([]byte(rows[0]["payload"].(string)), &doc); err != nil {
		t.Fatalf("anonymised payload is not valid JSON: %v", err)
	}
	details := doc["details"].(map[string]any)

	if details["firstName"] == "Daryl" { // unnamed string → hashed
		t.Errorf("firstName not anonymised: %v", details["firstName"])
	}
	if e, _ := details["email"].(string); e == "vonaxor@mailinator.com" ||
		!strings.Contains(e, "@") || !strings.HasSuffix(e, ".example") {
		t.Errorf("email not email-shaped: %v", details["email"])
	}
	if details["dob"] != float64(0) { // unnamed number → 0
		t.Errorf("dob not zeroed: %v", details["dob"])
	}
	if details["note"] != "" { // empty string preserved
		t.Errorf("empty string not preserved: %v", details["note"])
	}
	if mc := details["marketingConsent"].(map[string]any); mc["email"] != true { // kept subtree
		t.Errorf("kept subtree altered: %v", mc)
	}
	if doc["facebookUser"] != nil { // null preserved
		t.Errorf("null not preserved: %v", doc["facebookUser"])
	}

	// Manifest records the transform on the json column.
	man := readManifest(t, filepath.Join(runDir, "manifest.json"))
	if col := columnByName(man.Tables[0].Columns, "payload"); col == nil || col.Transform != "json_anonymise" {
		t.Errorf("manifest missing json_anonymise transform: %+v", man.Tables[0].Columns)
	}
}

func TestExcludeColumnIsDropped(t *testing.T) {
	src := &fakeSource{
		cols: map[string][]source.Column{
			"users": {
				{Name: "id", DataType: "bigint", Nullable: false},
				{Name: "full_name", DataType: "varchar", Nullable: true}, // derived, to be excluded
				{Name: "email", DataType: "varchar", Nullable: false},
			},
		},
		rows: map[string][][]value.Value{
			"users": {{text("1"), text("Ada Lovelace"), text("ada@x.com")}},
		},
	}
	dir := t.TempDir()
	cfg := &config.Config{
		Destination: config.DestinationConfig{Directory: dir},
		Hashing:     config.HashingConfig{Key: "secret"},
		Tables: []config.TableConfig{
			{Name: "users", Columns: map[string]config.ColumnConfig{
				"full_name": {Exclude: true},
				"email":     {Transform: "hash_email"},
			}},
		},
	}

	runDir, err := Run(context.Background(), src, cfg, Options{RunName: "r", Now: time.Unix(0, 0)})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	rows := readParquet(t, filepath.Join(runDir, "users.parquet"))
	if len(rows) != 1 {
		t.Fatalf("rows = %d", len(rows))
	}
	if _, present := rows[0]["full_name"]; present {
		t.Errorf("excluded column full_name was written to parquet: %v", rows[0])
	}
	if _, present := rows[0]["email"]; !present {
		t.Errorf("non-excluded column email missing")
	}

	man := readManifest(t, filepath.Join(runDir, "manifest.json"))
	if columnByName(man.Tables[0].Columns, "full_name") != nil {
		t.Errorf("excluded column recorded in manifest")
	}
	if columnByName(man.Tables[0].Columns, "email") == nil {
		t.Errorf("expected email in manifest")
	}
}

func TestExcludeWithTransformErrors(t *testing.T) {
	src := &fakeSource{
		cols: map[string][]source.Column{
			"users": {{Name: "id", DataType: "bigint", Nullable: false}},
		},
		rows: map[string][][]value.Value{},
	}
	cfg := &config.Config{
		Tables: []config.TableConfig{
			{Name: "users", Columns: map[string]config.ColumnConfig{
				"id": {Exclude: true, Transform: "null"},
			}},
		},
	}
	if err := Validate(context.Background(), src, cfg); err == nil {
		t.Fatal("expected error combining exclude with a transform")
	}
}

func readParquet(t *testing.T, path string) []map[string]any {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open parquet %s: %v", path, err)
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		t.Fatal(err)
	}
	pf, err := parquet.OpenFile(f, st.Size())
	if err != nil {
		t.Fatalf("open parquet file: %v", err)
	}
	reader := parquet.NewGenericReader[map[string]any](f, pf.Schema())
	defer reader.Close()

	out := make([]map[string]any, pf.NumRows())
	for i := range out {
		out[i] = map[string]any{}
	}
	n, err := reader.Read(out)
	if err != nil && err != io.EOF {
		t.Fatalf("read parquet rows: %v", err)
	}
	return out[:n]
}

func readManifest(t *testing.T, path string) manifest.Manifest {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var m manifest.Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("parse manifest: %v", err)
	}
	return m
}

func columnByName(cols []manifest.Column, name string) *manifest.Column {
	for i := range cols {
		if cols[i].Name == name {
			return &cols[i]
		}
	}
	return nil
}
