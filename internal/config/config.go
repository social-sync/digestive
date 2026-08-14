// Package config loads the YAML export configuration, expanding ${VAR}
// references (with an optional .env file) so secrets never live in the file.
package config

import (
	"fmt"
	"os"

	"github.com/joho/godotenv"
	"gopkg.in/yaml.v3"
)

// Config is the whole export configuration.
type Config struct {
	Source      SourceConfig      `yaml:"source"`
	Destination DestinationConfig `yaml:"destination"`
	Hashing     HashingConfig     `yaml:"hashing"`
	Tables      []TableConfig     `yaml:"tables"`
}

// SourceConfig describes the database to read from.
type SourceConfig struct {
	// DSN is a go-sql-driver/mysql data source name. SingleStore is MySQL
	// wire compatible, so the same DSN format applies.
	DSN string `yaml:"dsn"`
}

// DestinationConfig describes where output is written. v1 supports a local
// directory only.
type DestinationConfig struct {
	Directory string `yaml:"directory"`
}

// HashingConfig holds the secret used to key deterministic hashing.
type HashingConfig struct {
	// Key is the HMAC secret. It should be supplied via ${VAR} substitution,
	// never written in plaintext.
	Key string `yaml:"key"`
}

// TableConfig selects a table for export and, optionally, how to reduce and
// transform it. A table listed with no options exports in full, untransformed.
type TableConfig struct {
	Name    string                  `yaml:"name"`
	Where   string                  `yaml:"where"`
	OrderBy string                  `yaml:"order_by"`
	Limit   *int                    `yaml:"limit"`
	Columns map[string]ColumnConfig `yaml:"columns"`
}

// UnmarshalYAML lets a table be written either as a bare string (`- users`,
// the quick path) or as a full mapping with options.
func (t *TableConfig) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode {
		t.Name = node.Value
		return nil
	}
	// Alias to avoid recursing into this method.
	type rawTable TableConfig
	var raw rawTable
	if err := node.Decode(&raw); err != nil {
		return err
	}
	*t = TableConfig(raw)
	return nil
}

// ColumnConfig is the transform applied to one column. Which fields are
// meaningful depends on Transform.
type ColumnConfig struct {
	Transform string `yaml:"transform"`

	// constant: the literal to substitute.
	Value *string `yaml:"value"`

	// mask: how many leading/trailing runes to keep, and the fill rune.
	KeepFirst int    `yaml:"keep_first"`
	KeepLast  int    `yaml:"keep_last"`
	MaskChar  string `yaml:"mask_char"`

	// hash / hash_email: optional namespace and hex output length.
	Group  string `yaml:"group"`
	Length int    `yaml:"length"`
}

// Load reads, env-expands, and parses the config at path. A .env file in the
// working directory is consulted for substitutions, but real environment
// variables always win.
func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	lookup, err := envLookup()
	if err != nil {
		return nil, err
	}

	expanded, err := expand(string(raw), lookup)
	if err != nil {
		return nil, err
	}

	var cfg Config
	if err := yaml.Unmarshal([]byte(expanded), &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	return &cfg, nil
}

// envLookup returns a resolver over real environment variables overlaid on an
// optional .env file (real environment wins).
func envLookup() (func(string) (string, bool), error) {
	dotenv := map[string]string{}
	if _, err := os.Stat(".env"); err == nil {
		dotenv, err = godotenv.Read(".env")
		if err != nil {
			return nil, fmt.Errorf("read .env: %w", err)
		}
	}
	return func(key string) (string, bool) {
		if v, ok := os.LookupEnv(key); ok {
			return v, true
		}
		v, ok := dotenv[key]
		return v, ok
	}, nil
}
