// Package config loads the YAML export configuration, expanding ${VAR}
// references (with an optional .env file) so secrets never live in the file.
package config

import (
	"fmt"
	"os"
	"strings"

	"github.com/joho/godotenv"
	"gopkg.in/yaml.v3"
)

// Config is the whole export configuration.
type Config struct {
	Source      SourceConfig      `yaml:"source"`
	Destination DestinationConfig `yaml:"destination"`
	Sync        SyncConfig        `yaml:"sync"`
	Hashing     HashingConfig     `yaml:"hashing"`
	Tables      []TableConfig     `yaml:"tables"`
	// Compliance, when present, turns on audit logging: export and sync then
	// require a named requester and write a per-run audit record. Its absence
	// (nil) leaves the tool behaving exactly as before.
	Compliance *ComplianceConfig `yaml:"compliance"`
}

// SourceConfig describes the database to read from.
type SourceConfig struct {
	// DSN is a go-sql-driver/mysql data source name. SingleStore is MySQL
	// wire compatible, so the same DSN format applies. Sensitive: redacted in
	// audit records.
	DSN string `yaml:"dsn" redact:"true"`
}

// DestinationConfig describes where output is written. v1 supports a local
// directory only.
type DestinationConfig struct {
	Directory string `yaml:"directory"`
}

// SyncConfig describes the destination database `digestive sync` applies an
// export into. It is read only by the sync command; export, validate, and
// restore ignore it entirely.
type SyncConfig struct {
	// DSN is the destination database, in go-sql-driver/mysql format. Supply it
	// via ${VAR} substitution so credentials never live in the file. Sensitive:
	// redacted in audit records.
	DSN string `yaml:"dsn" redact:"true"`
	// Type selects the destination engine, which fixes both the SQL driver and
	// the restore dialect. Currently "mysql" or "singlestore" (both MySQL-wire).
	Type string `yaml:"type"`
	// BatchSize is the number of rows packed into each multi-row INSERT
	// statement. Nil uses the built-in default (1000). Lower it to shrink the
	// individual statements a large-row table produces. The --batch-size flag
	// overrides it.
	BatchSize *int `yaml:"batch_size"`
	// MaxPacketBytes bounds how many bytes of a table's INSERT SQL are sent to
	// the destination in a single round trip: a table's statements are split
	// into chunks no larger than this, so a large table no longer overflows the
	// server/driver max_allowed_packet limit ("packet for query is too large").
	// Nil uses the built-in default. The --max-packet-bytes flag overrides it.
	MaxPacketBytes *int `yaml:"max_packet_bytes"`
}

// HashingConfig holds the secret used to key deterministic hashing.
type HashingConfig struct {
	// Key is the HMAC secret. It should be supplied via ${VAR} substitution,
	// never written in plaintext. Sensitive: redacted in audit records.
	Key string `yaml:"key" redact:"true"`
}

// ComplianceConfig turns on audit logging. Its mere presence makes export and
// sync require a requester; a present-but-invalid block is a hard error, never
// a silent no-op.
type ComplianceConfig struct {
	Audit AuditConfig `yaml:"audit"`
}

// AuditConfig describes where audit records are written. Exactly one of
// Directory or S3 must be set.
type AuditConfig struct {
	// Directory writes audit JSON files to a local directory.
	Directory string `yaml:"directory"`
	// S3 writes audit JSON objects to an S3-compatible bucket.
	S3 *S3Config `yaml:"s3"`
}

// S3Config describes an S3-compatible destination for audit records.
type S3Config struct {
	// Endpoint is the host[:port] of the S3-compatible service, with no scheme
	// (the scheme is controlled by UseSSL).
	Endpoint string `yaml:"endpoint"`
	// Bucket is the target bucket. It must already exist.
	Bucket string `yaml:"bucket"`
	// Prefix is an optional key prefix (e.g. "exports/") for written objects.
	Prefix string `yaml:"prefix"`
	// Region is the signing region; default "us-east-1". Use "auto" for R2.
	Region string `yaml:"region"`
	// AccessKeyID and SecretAccessKey are the service credentials. Supply them
	// via ${VAR} substitution. Sensitive: redacted in audit records.
	AccessKeyID     string `yaml:"access_key_id" redact:"true"`
	SecretAccessKey string `yaml:"secret_access_key" redact:"true"`
	// UseSSL selects https (maps to minio Options.Secure).
	UseSSL bool `yaml:"use_ssl"`
	// PathStyle forces path-style bucket addressing (MinIO/Ceph/custom domains).
	PathStyle bool `yaml:"path_style"`
}

// Validate checks that a present compliance block is well-formed. It is called
// at load time so a malformed block fails loudly rather than silently disabling
// the audit gate.
func (c *ComplianceConfig) Validate() error {
	hasDir := c.Audit.Directory != ""
	hasS3 := c.Audit.S3 != nil
	switch {
	case !hasDir && !hasS3:
		return fmt.Errorf("compliance.audit requires exactly one of `directory` or `s3`; neither is set")
	case hasDir && hasS3:
		return fmt.Errorf("compliance.audit sets both `directory` and `s3`; set exactly one")
	}
	if hasS3 {
		s3 := c.Audit.S3
		var missing []string
		if s3.Endpoint == "" {
			missing = append(missing, "endpoint")
		}
		if s3.Bucket == "" {
			missing = append(missing, "bucket")
		}
		if s3.AccessKeyID == "" {
			missing = append(missing, "access_key_id")
		}
		if s3.SecretAccessKey == "" {
			missing = append(missing, "secret_access_key")
		}
		if len(missing) > 0 {
			return fmt.Errorf("compliance.audit.s3 is missing required field(s): %s", strings.Join(missing, ", "))
		}
	}
	return nil
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

	// Exclude drops the column from the export entirely — it is not read, not
	// written to Parquet, and not recorded in the manifest. Useful for derived
	// or generated columns that cannot be reconstructed. Cannot be combined
	// with a transform.
	Exclude bool `yaml:"exclude"`

	// constant: the literal to substitute.
	Value *string `yaml:"value"`

	// mask: how many leading/trailing runes to keep, and the fill rune.
	KeepFirst int    `yaml:"keep_first"`
	KeepLast  int    `yaml:"keep_last"`
	MaskChar  string `yaml:"mask_char"`

	// hash / hash_email: optional namespace and hex output length.
	Group  string `yaml:"group"`
	Length int    `yaml:"length"`

	// json_anonymise: per-path rules for anonymising inside a JSON document.
	JSON *JSONConfig `yaml:"json"`
}

// JSONConfig holds the per-path rules for a json_anonymise transform. Keep lists
// paths (leaf or subtree) passed through untouched; Paths maps a path to the
// transform applied to the leaf there. Anything not named is anonymised by the
// built-in default-deny rules.
type JSONConfig struct {
	Keep  []string                `yaml:"keep"`
	Paths map[string]ColumnConfig `yaml:"paths"`
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
	// A present compliance block is validated eagerly: "on but broken" must be a
	// hard error, never a silent no-op that skips the audit gate.
	if cfg.Compliance != nil {
		if err := cfg.Compliance.Validate(); err != nil {
			return nil, err
		}
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
