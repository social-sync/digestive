//go:build integration

// Package inttest holds Docker-backed, same-engine round-trip integration tests
// that verify every Laravel migration column type survives digestive's
// export → restore pipeline on MySQL and SingleStore. It is compiled only under
// the `integration` build tag (see the Makefile's `test-integration` target) so
// the default `go test ./...` unit run stays fast and Docker-free.
package inttest

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// fixtureType is one row of fixtures.yaml: a Laravel column type and the values
// to round-trip through it.
type fixtureType struct {
	Laravel         string   `yaml:"laravel"`
	Aka             []string `yaml:"aka"`
	DDL             string   `yaml:"ddl"`
	DDLMySQL        string   `yaml:"ddl_mysql"`
	DDLSingleStore  string   `yaml:"ddl_singlestore"`
	SkipMySQL       string   `yaml:"skip_mysql"`
	SkipSingleStore string   `yaml:"skip_singlestore"`
	Values          []string `yaml:"values"`
	Note            string   `yaml:"note"`
}

// ddlFor returns the column definition for an engine, honouring per-engine
// overrides, and whether the engine skips this type (and why).
func (f fixtureType) ddlFor(engine string) (ddl, skip string) {
	switch engine {
	case engineMySQL:
		if f.SkipMySQL != "" {
			return "", f.SkipMySQL
		}
		if f.DDLMySQL != "" {
			return f.DDLMySQL, ""
		}
	case engineSingleStore:
		if f.SkipSingleStore != "" {
			return "", f.SkipSingleStore
		}
		if f.DDLSingleStore != "" {
			return f.DDLSingleStore, ""
		}
	}
	if f.DDL == "" {
		return "", fmt.Sprintf("no ddl defined for engine %q", engine)
	}
	return f.DDL, ""
}

type fixtureFile struct {
	Types []fixtureType `yaml:"types"`
}

// loadFixtures reads and parses fixtures.yaml, relative to this package.
func loadFixtures() ([]fixtureType, error) {
	path := filepath.Join("fixtures.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read fixtures: %w", err)
	}
	var ff fixtureFile
	if err := yaml.Unmarshal(data, &ff); err != nil {
		return nil, fmt.Errorf("parse fixtures: %w", err)
	}
	if len(ff.Types) == 0 {
		return nil, fmt.Errorf("fixtures.yaml declares no types")
	}
	return ff.Types, nil
}
