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
- `--requester-name` / `--requester-email` — the person requesting the export.
  **Required** when [compliance audit logging](#compliance-audit-logging) is
  configured; ignored otherwise.
- `--cleanup-on-audit-fail` — delete the run directory if the audit record can't
  be written (enforces "no export without an audit trail").
- `--json` — emit a JSON result on stdout instead of the TUI (see
  [JSON output](#json-output)).

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

## JSON output

Every command accepts a persistent `--json` flag for machine consumers (a web
app shelling out to the binary, CI, scripts). Under `--json`:

- **stdout carries exactly one JSON object** — pretty-printed, trailing
  newline, and nothing else. The live TUI is disabled.
- It applies to **success and failure alike**: a failing run still prints a JSON
  object (with `status: "error"`), and the process still exits non-zero, so
  `&&` chains and CI keep working. A consumer never has to scrape stderr.
- It implies **quiet**: diagnostic logging on stderr is suppressed unless you
  raise it explicitly with `--log-level` (stdout stays pure JSON regardless).

Every command emits the same envelope; the per-command payload lives in
`result`:

```json
{
  "schema_version": 1,
  "command": "export",
  "status": "ok",
  "error": null,
  "warnings": [],
  "result": {
    "run_dir": "exports/2026-08-14T15-04-05Z",
    "run_id": "2026-08-14T15-04-05Z",
    "tables": [
      { "name": "users", "rows": 48210 },
      { "name": "orders", "rows": 195003 }
    ],
    "total_rows": 243213
  }
}
```

`schema_version` lets a consumer guard against future shape changes; `status` is
`"ok"` or `"error"` and mirrors the exit code; `error` is `null` on success or a
string on failure; `warnings` is always an array. `result` per command:

- **export** — `run_dir`, `run_id`, per-table `{name, rows}`, and `total_rows`
  (drawn from the manifest the run just wrote).
- **sync** — the same table/row summary plus `applied`, `destination`
  (`{type, host, database}`), and `run_dir` (`null` when `--cleanup` removed
  it). Under `--json`, `sync` **requires `--yes`** — since a machine consumer
  can't answer the confirmation prompt, the flag must be passed explicitly to
  acknowledge the live-database write, otherwise `sync` returns a JSON error.
- **restore** — a **summary**, not SQL: `run_dir`, `dialect`, per-table
  `{name, rows, statements}`, and `total_statements`. `restore --json` emits no
  SQL on stdout; use plain `restore` (capturing stdout) when you want the script
  itself, or `sync` to apply directly.
- **validate** — `tables` and `table_count`.
- **init** — `created` (the files written). The generated hashing key is
  **never** included in the output; it lives only in `.env`.

The JSON contract covers execution-time outcomes. A malformed *invocation* that
`cobra` rejects before the command runs (an unknown flag or command, a missing
required argument) still errors conventionally on stderr — a consumer should
treat "stdout did not parse as JSON" as a failed invocation.

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
- `--ignore-restore-conf` — ignore a `restore.yaml` in the working directory
  (see *Reconciling schema drift* below).
- `--json` — emit a JSON **summary** (dialect, per-table row/statement counts)
  on stdout instead of the SQL script (see [JSON output](#json-output)).

The SQL goes to **stdout**; logs and warnings go to stderr. Table names are
emitted unqualified, so pick the target database with the client
(`mysql -D dbname`). No DDL is generated — the target is assumed to be a copy of
the source database whose schema already exists. See
[ADR-0005](./docs/adr/0005-restore-to-sql.md).

### Reconciling schema drift

An export is an immutable production snapshot, but the target you load it into
often **drifts** — you pull an export to work on locally and your migrations
have already renamed a column, dropped one, or added a non-null column with no
default. The plain `INSERT`s then fail. A **`restore.yaml`** in the working
directory declares how to reconcile the export with the drifted schema. If the
file is present, `restore` applies it and notes so on stderr; pass
`--ignore-restore-conf` to skip it.

It is **declarative** — you *state* the drift; `restore` still connects to
nothing, so it cannot verify your rules against the live target (a wrong rule
fails at the database, as it would today). It is **schema-shape only**: it
reshapes the emitted column set, never a value's meaning (anonymisation stays on
the export side). Rules live with your local app and its migrations, which is
why the file is discovered from the working directory rather than the run
directory.

```yaml
# restore.yaml
tables:
  users:
    rename_table: app_users        # optional: emit INSERT INTO app_users
    rename_columns:
      full_name: display_name      # manifest column -> target column
    drop_columns:
      - legacy_flag
    add_columns:
      tenant_id:                    # column the export predates
        value: 1                    # quoted literal; the engine coerces it
      created_at:
        value: NOW()
        raw: true                   # spliced verbatim as a SQL expression
      deleted_at:
        value: null                 # explicit SQL NULL

  audit_log:
    drop_table: true                # skip this table's INSERTs entirely
```

Five operations: rename / drop / add **column** and rename / drop **table**.
Every rule is validated against the manifest and any contradiction is a hard
error — a rule targeting a column or table that isn't in the export, an `add`
that already exists, a rename that collides with another column, and so on —
because a rule that silently matches nothing would only fail later at the
database. Added columns render as a quoted literal (default), an explicit
`null`, or a `raw` SQL expression, and are emitted in sorted order for
deterministic output. Restore-time **type changes** and adding a **brand-new
table** (which has no source data) are out of scope. See
[ADR-0006](./docs/adr/0006-restore-schema-reconciliation.md).

## Sync

`sync` runs the whole pipeline end to end — export, then apply the same
`INSERT`s `restore` would emit **directly into a destination database** over the
Go SQL driver. No `mysql` client, no intermediate piping. The destination lives
in config under a `sync` block:

```yaml
sync:
  dsn: ${SYNC_DSN}   # go-sql-driver/mysql DSN for the destination
  type: mysql        # mysql or singlestore — selects driver + restore dialect
```

```sh
digestive sync                                  # export fresh, then apply
digestive sync ./exports/2026-08-14T15-04-05Z   # skip export, apply an existing run
```

The apply runs in a **single transaction**: it either lands completely or rolls
back, leaving the destination untouched. Because it reuses `restore`, a
`restore.yaml` in the working directory is honoured identically, and the data
applied is byte-for-byte what a piped `restore` would load. The destination
**schema must already exist** — like `restore`, `sync` inserts data and does not
create tables; a missing table surfaces as a database error and rolls the sync
back.

`sync` flags:

- `--yes` — skip the confirmation prompt. `sync` is the one command that writes
  to a live database, so when stderr is a terminal it prints the destination
  host and database and asks before applying; the prompt is skipped
  automatically when non-interactive (CI, piped) or with `--yes`.
- `--cleanup` — delete the run directory after a successful apply (ignored when
  you pass an existing run directory; a failed apply always keeps it).
- `--dialect singlestore|mysql` — override the dialect derived from `sync.type`.
- `--batch-size N`, `--allow-incomplete`, `--ignore-restore-conf` — as for
  `restore`.
- `--no-tui` — as for `export`.
- `--json` — emit a JSON result on stdout instead of the TUI (see
  [JSON output](#json-output)); **requires `--yes`** since a machine consumer
  cannot answer the confirmation prompt.

DDL generation, an external-client pipe for engines without a Go driver, and
Postgres support are out of scope for now; the `type` key is the seam they would
build on. See [ADR-0007](./docs/adr/0007-sync-direct-apply.md).

## Compliance audit logging

For regulated data you can record **who** pulled an export, **when**, under
**what config**, and **how many rows** left each table. Add a `compliance:` block
to your config and every `export` and `sync` writes a per-run **audit record** (a
JSON document) to an S3-compatible bucket **or** a local directory:

```yaml
compliance:
  audit:
    # exactly one of `directory` or `s3`
    directory: ./audit-logs
    # s3:
    #   endpoint: ${AUDIT_S3_ENDPOINT}     # host[:port], no scheme
    #   bucket: ${AUDIT_S3_BUCKET}
    #   prefix: exports/
    #   region: ${AUDIT_S3_REGION:-us-east-1}
    #   access_key_id: ${AUDIT_S3_ACCESS_KEY}
    #   secret_access_key: ${AUDIT_S3_SECRET_KEY}
    #   use_ssl: true
    #   path_style: true
```

The feature is **opt-in and gated by config** — off unless `compliance:` is
present, and **mandatory** when it is:

- `export` and `sync` then **require** `--requester-name` and
  `--requester-email` (validated before any work; the email must parse).
- A present-but-malformed block is a **hard error at load time** — auditing is
  never silently disabled.

Each record captures the requester, timestamps, the run's output location, the
**resolved config with secrets redacted** (`source.dsn`, `sync.dsn`,
`hashing.key`, and S3 credentials become `***REDACTED***`), the full
`manifest.json`, and a per-table row-count map. It's written **only on success**;
if the write fails the command **exits non-zero**, and `--cleanup-on-audit-fail`
additionally deletes the run directory so a completed export is never left without
its trail. For `sync`, the record is written **before** applying to the target,
so data never lands without an audit. The S3 sink uses
[`minio-go`](https://github.com/minio/minio-go) and works with MinIO, Cloudflare
R2, Ceph, Wasabi, and AWS S3.

See [ADR-0008](./docs/adr/0008-compliance-audit-logging.md) for the design.

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

## How the dialect tests are measured

The table above is a claim; `internal/inttest` is how we verify it. It's a
Docker-backed, **same-engine** round-trip suite that pushes every
[Laravel migration column type](https://laravel.com/docs/migrations#available-column-types)
through the whole pipeline on both supported engines and checks the values
survive. It is the executable counterpart to the mapping table, and its output
is where the edge-case notes above come from.

**What it does, per column type, per engine:**

1. Spin up one throwaway database container (MySQL, and SingleStore when
   configured) — one container per engine, reused across every type.
2. `CREATE TABLE` with that type, `INSERT` a handful of representative values
   (including the awkward ones: `BIGINT UNSIGNED` max, zero-dates, empty-but-
   non-null blobs, 4-byte UTF-8, comma-bearing `ENUM`, `JSON` key ordering, …).
3. Read the values back off the wire — this is the **baseline**, the exact bytes
   digestive would export.
4. Run the real `export` path (source → typemap → Parquet + manifest), then
   `restore` to a SQL script using that engine's `--dialect`.
5. `TRUNCATE` the table and replay the restored `INSERT`s.
6. Read the values back again and make two assertions:
   - **Fidelity (hard gate):** the reloaded bytes must equal the baseline bytes,
     cell for cell. Any divergence is a real bug or an edge case to document.
   - **Golden (characterisation):** whatever came back is recorded in
     `internal/inttest/golden/<engine>/<type>.golden`, with a header noting the
     observed source type, chosen Parquet type, and lossless flag. The golden is
     both a regression lock and the raw material for the mapping table.

The column types live in a human-readable
[`internal/inttest/fixtures.yaml`](./internal/inttest/fixtures.yaml), keyed by
Laravel method, so adding a type or a value is a one-line edit.

**Running it:**

```sh
make test-integration              # MySQL always; SingleStore if configured
make test-integration UPDATE=1     # regenerate goldens after an intended change
```

Requirements and behaviour:

- **Docker** must be available. Containers bind to a **random free host port**
  (never a fixed `:3306`), so a locally-running MySQL doesn't block the tests.
- The **SingleStore** leg needs a free license key in `SINGLESTORE_LICENSE`
  (get one at <https://portal.singlestore.com>). Without it that leg **skips**
  with an explanatory message — it never fails the suite. MySQL is the always-on
  baseline; SingleStore is best-effort.
- A **missing golden** is written and passed on first run (with a log line) so a
  new type characterises itself; review the git diff, then commit it. A **changed
  golden** fails the run unless you re-run with `UPDATE=1`.
- Pin engine images with `DIGESTIVE_MYSQL_IMAGE` / `DIGESTIVE_SINGLESTORE_IMAGE`
  (e.g. MySQL 9 to exercise the native `VECTOR` type).

This suite is **local-only** for now; wiring it into CI is deferred.

## Scope

v1 implements `export`, `validate`, `restore` (export run → single SQL script of
INSERTs, same-engine round-trip), and `sync` (export → apply straight into a
destination database over the driver, in one transaction). Cross-engine type
remapping (e.g. to Postgres), an external-client pipe for engines without a Go
driver, DDL/schema creation at the destination, a **realistic faker** family
(plausible fake names/addresses), remote destinations, and FK-aware subsetting
are **deferred but designed for** — see the ADRs. Structural JSON anonymisation
(`json_anonymise`) is implemented; faker-backed *realistic* leaf values are the
deferred part.

## License

Licensed under the [MIT License](./LICENSE).
