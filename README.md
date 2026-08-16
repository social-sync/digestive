# Digestive

A single-binary database exporter that pulls tables from a SingleStore
(MySQL-wire-compatible) database, anonymises / redacts / deterministically
hashes columns on the way out, and writes the result as **Parquet** files plus
a **manifest** describing how to reconstruct exact `INSERT`s later.

The domain model lives in [CONTEXT.md](./CONTEXT.md); the load-bearing design
decisions are recorded in [docs/adr/](./docs/adr/).

## Install

**Homebrew (macOS):**

```sh
brew install social-sync/tap/digestive
```

**Install script (macOS / Linux):**

```sh
curl -sSfL https://raw.githubusercontent.com/social-sync/digestive/main/install.sh | sh
```

The script detects your OS/arch, verifies the SHA-256 checksum, and installs to
`/usr/local/bin` (falling back to `~/.local/bin`). Override the target with
`DIGESTIVE_BIN_DIR=... ` or pin a version with `DIGESTIVE_VERSION=v1.2.3`.

**Manual:** download a prebuilt archive for your platform from the
[releases page](https://github.com/social-sync/digestive/releases), extract it,
and put the `digestive` binary on your `PATH`.

**Go:**

```sh
go install github.com/social-sync/digestive@latest
```

## Build from source

```sh
make build        # CGO-free static binary: ./digestive
make test
```

## Usage

```sh
digestive init                             # create starter .env + config.yaml (won't overwrite)
digestive validate --config config.yaml    # check config against live schema, no export
digestive export   --config config.yaml    # run the export
digestive restore  <run-dir> --dialect singlestore > dump.sql   # export run -> SQL INSERTs
```

`init` writes a `config.yaml` and a `.env` containing a freshly generated
random hashing key (`EXPORT_HASH_KEY`). It never overwrites: if either file
already exists it fails and writes nothing. Edit `.env` to set your
`SINGLESTORE_DSN`, then edit `config.yaml`.

`export` flags:

- `--run-name NAME` — name the run directory (default: UTC timestamp).
- `--delete-on-failure` — remove the whole run directory if the export fails,
  so repeated failures don't accumulate partial output.
- `--no-tui` — disable the live progress UI and emit plain structured log lines
  instead.

When stderr is an interactive terminal, `export` renders a live progress
display (one line per table, with row counts and timings) that stays in
scrollback when the run finishes. When stderr is piped or redirected (CI,
`2>file`), or with `--no-tui` or `--log-level debug`, it falls back to plain
structured logging. Either way the machine-readable run directory is printed on
its own line to stdout, so `RUN=$(digestive export)` keeps working.

Each run writes:

```
<destination>/<run-name>/
  manifest.json        # written last; its absence means the run is incomplete
  <table>.parquet      # one file per table
```

Parquet files are compressed with **zstd**. The compression is stored inside
each file, so any standard reader (DuckDB, pandas, Spark, …) opens it directly —
there is no separate decompression step.

## Restore

`restore` turns an export run back into a single SQL script of `INSERT`
statements — ready to pipe into the `mysql` client or paste into a SQL editor.
It reads **only** the run directory (manifest + Parquet); no config file, no
database connection. Types are preserved for a same-engine round-trip.

```sh
digestive restore ./exports/2026-08-14T15-04-05Z --dialect singlestore > dump.sql
# or stream straight into a client:
digestive restore ./exports/2026-08-14T15-04-05Z --dialect mysql | mysql -D mydb
```

`restore` flags:

- `--dialect singlestore|mysql` — **required**, no default. It selects the
  session preamble: both wrap the load in `SET NAMES utf8mb4` and a single
  transaction; `mysql` additionally emits `SET FOREIGN_KEY_CHECKS=0` /
  `SET UNIQUE_CHECKS=0` (SingleStore neither needs nor recognises those). Value
  and identifier syntax are identical across both.
- `--batch-size N` — rows per multi-row `INSERT` (default `1000`).
- `--allow-incomplete` — restore a run whose `manifest.json` reports
  `complete: false` (refused by default).

The SQL goes to **stdout**; logs and warnings go to stderr. Table names are
emitted unqualified, so pick the target database with the client
(`mysql -D dbname`). No DDL is generated — the target is assumed to be a copy of
the source database whose schema already exists. See
[ADR-0005](./docs/adr/0005-restore-to-sql.md).

## Configuration

See the template at [internal/templates/config.yaml](./internal/templates/config.yaml)
(what `init` writes). Config is YAML with `${VAR}`
substitution (a `.env` file is consulted; real environment variables win).
Missing variables with no `${VAR:-default}` fail the run.

- **Table selection is opt-in.** Only listed tables are exported. A bare table
  name (`- users`) exports the whole table untransformed.
- **Columns pass through by default.** You only name columns that need a
  transform; other columns are exported unchanged.
- **Row reduction** per table via a raw `where` fragment, `order_by`, and
  `limit`. Filters are applied per table with **no automatic foreign-key
  following** — keeping filtered tables mutually consistent is your
  responsibility.

### Transforms

| Transform        | Family        | Notes |
|------------------|---------------|-------|
| `null`           | redaction     | Sets the value to NULL. Column must be nullable. |
| `constant`       | redaction     | Replaces with a fixed `value`. NULL stays NULL. |
| `mask`           | redaction     | Keeps `keep_first` / `keep_last` runes, fills the rest with `mask_char` (default `*`). Text columns only. |
| `hash`           | hashing       | HMAC-keyed hex pseudonym; optional `length`, optional `group`. Text columns only. |
| `hash_email`     | hashing       | HMAC-keyed but email-shaped (`local@domain.example`). Text columns only. |
| `json_anonymise` | anonymisation | Anonymises the values *inside* a JSON document while keeping its shape. JSON columns only. See [ADR-0004](./docs/adr/0004-json-anonymise-structural-in-place-anonymisation.md). |

Deterministic hashing is **global by default**: the same input yields the same
pseudonym everywhere (keyed by `hashing.key`), so foreign-key relationships
survive. Set a `group` on a column to isolate it into a separate namespace. The
`hashing.key` must stay stable across runs, or pseudonyms change.

`json_anonymise` keeps a `json` column's structure (keys, nesting, null-vs-set)
intact so it still deserializes, but anonymises the values within. It is
**default-deny**: every leaf you do not `keep` or name in `paths` is anonymised
automatically (non-empty strings hashed, numbers → `0`; nulls, empty strings and
booleans preserved), so PII added to the document upstream is anonymised even if
you never configured it. Leaf hashes share the same global namespace as scalar
hashes, so joins survive across the boundary.

## Type mapping (SingleStore → Parquet)

Per [ADR-0003](./docs/adr/0003-hybrid-type-mapping-with-lossless-fallback.md):
native Parquet types where safe, lossless string/bytes fallback otherwise. The
manifest records the source type and whether the column was stored losslessly.

| Source type | Parquet | Lossless fallback? |
|-------------|---------|--------------------|
| `tinyint`, `smallint`, `mediumint`, `int`, `integer` (any sign) | `INT64` | no |
| `bigint` (signed) | `INT64` | no |
| `bigint unsigned` | `BYTE_ARRAY(STRING)` | yes — exceeds signed 64-bit range |
| `float`, `double`, `real` | `DOUBLE` | no |
| `decimal` / `numeric` | `BYTE_ARRAY(STRING)` | yes — preserves precision/scale |
| `char`, `varchar`, `*text`, `enum`, `set` | `BYTE_ARRAY(STRING)` | no |
| `date`, `datetime`, `timestamp`, `time`, `year` | `BYTE_ARRAY(STRING)` | yes — avoids zero-dates & precision loss |
| `json`, `vector`, `geography`, `geographypoint` | `BYTE_ARRAY(STRING)` | yes — no native equivalent |
| `binary`, `varbinary`, `*blob`, `bit`, `bson` | `BYTE_ARRAY` | yes |
| anything else | `BYTE_ARRAY` | yes — preserved raw to avoid corruption |

Only text columns (`char`/`varchar`/`*text`/`enum`/`set`) may be hashed or
masked; the tool rejects a config that targets any other type.

## Scope

v1 implements `export`, `validate`, and `restore` (export run → single SQL
script of INSERTs, same-engine round-trip). Streaming load piped directly into a
destination DB, cross-engine type remapping (e.g. to Postgres), a **realistic
faker** family (plausible fake names/addresses), remote destinations, and
FK-aware subsetting are **deferred but designed for** — see the ADRs. Structural
JSON anonymisation (`json_anonymise`) is implemented; faker-backed *realistic*
leaf values are the deferred part.

## License

Licensed under the [MIT License](./LICENSE).
