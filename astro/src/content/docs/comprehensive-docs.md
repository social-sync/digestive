---
title: Comprehensive docs
description: The complete Digestive documentation — installation, configuration, transformers, restore, sync, JSON output, and compliance — on one page.
---

:::note[About this page]
This is the combined documentation, written by the AI agent as we built the
tool. It stitches every topic together on one page. For the hand-written,
task-focused guides, use the pages at the top of the sidebar instead.
:::

The entire Digestive documentation set on a single page: installation through
compliance. Use the page navigation on the right to jump to a section, or read
top to bottom. Prefer smaller, task-focused pages? Each section below also
exists on its own — and every command's flags are collected in the
[Command reference](/command-reference/).

## Installation

Digestive ships as a single self-contained binary. Install a prebuilt release
with Homebrew or the install script, or build from source. Then run `init` to
scaffold your configuration.

### Install a prebuilt binary

**Homebrew (macOS):**

```sh
brew install social-sync/tap/digestive
```

**Install script (macOS / Linux):**

```sh
curl -sSfL https://raw.githubusercontent.com/social-sync/digestive/main/install.sh | sh
```

The script detects your OS and architecture, downloads the matching release
asset, verifies its SHA-256 checksum, and installs `digestive` to
`/usr/local/bin` (falling back to `~/.local/bin`). Set `DIGESTIVE_BIN_DIR` to
choose the target directory, or `DIGESTIVE_VERSION=v1.2.3` to pin a version.

**Manual:** grab an archive for your platform from the
[releases page](https://github.com/social-sync/digestive/releases), extract it,
and put the `digestive` binary on your `PATH`.

Once installed, skip ahead to [Scaffold a config](#scaffold-a-config).

### Build from source

Prefer building yourself? Read on.

#### Prerequisites

- **Go 1.26 or newer** (the module targets Go 1.26). Check with `go version`.
- Network access to your source database (SingleStore, or any MySQL-wire-compatible database).

No C toolchain is required — the build is pure Go (`CGO_ENABLED=0`), so it
produces one self-contained binary with no runtime dependencies.

#### Clone and build

```sh
git clone https://github.com/social-sync/digestive.git
cd digestive

# Build the single static binary into ./digestive
make build
```

`make build` runs `CGO_ENABLED=0 go build -o digestive .`. If you prefer, run
that directly, or install it onto your `PATH`:

```sh
go install github.com/social-sync/digestive@latest
```

Verify it works:

```sh
./digestive --help
```

### Scaffold a config

From the directory you want to work in, run `init`. It writes two files:

```sh
./digestive init
```

- **`config.yaml`** — a starter configuration you edit to describe your export.
- **`.env`** — holds your secrets, created with a freshly generated random
  hashing key (`EXPORT_HASH_KEY`) and a placeholder database DSN. It is written
  with `0600` permissions because it contains secrets.

:::caution[init never overwrites]
If either `config.yaml` or `.env` already exists, `init` fails and writes
nothing — so re-running it can't clobber your config or regenerate (and thereby
invalidate) your hashing key. Delete the files yourself if you truly want to
start over.
:::

### Point it at your database

Edit `.env` and set your connection string. The DSN uses the
[go-sql-driver/mysql](https://github.com/go-sql-driver/mysql#dsn-data-source-name)
format; SingleStore is MySQL wire compatible, so the same format applies:

```ini
# .env
SINGLESTORE_DSN=root:password@tcp(127.0.0.1:3306)/mydb
EXPORT_HASH_KEY=<generated for you by init — keep it stable>
```

:::note[Keep the key stable]
`EXPORT_HASH_KEY` is the secret that keys deterministic hashing. If it changes
between runs, every hashed value changes too, and previously-exported data will
no longer join. Keep it constant (and out of source control).
:::

### Validate, then export

Describe the tables you want in `config.yaml` (see
[Configuration](#configuration)), then:

```sh
# Check the config against the live schema without exporting anything.
./digestive validate

# Run the export.
./digestive export
```

A successful export prints the run directory it produced, e.g.
`./exports/2026-08-14T15-04-05Z/`, containing one Parquet file per table plus a
`manifest.json`.

To turn that run back into loadable SQL, see [Restore](#restore):

```sh
./digestive restore ./exports/2026-08-14T15-04-05Z --dialect singlestore > dump.sql
```

## Configuration

Digestive is driven by a single YAML file (`config.yaml` by default). This
section documents every option.

### A complete example

```yaml
source:
  dsn: ${SINGLESTORE_DSN}

destination:
  directory: ./exports

# Optional — only used by `digestive sync`.
sync:
  dsn: ${SYNC_DSN}
  type: mysql

hashing:
  key: ${EXPORT_HASH_KEY}

tables:
  # Quick path: a bare table name exports the whole table, untransformed.
  - countries

  # Full form: row reduction plus per-column transforms.
  - name: users
    where: "created_at > '2024-01-01'"
    order_by: "id"
    limit: 10000
    columns:
      email:
        transform: hash_email
      name:
        transform: mask
        keep_first: 1
      password:
        transform: constant
        value: "password"
      api_token:
        transform: "null"
```

### Environment variable substitution

Before the YAML is parsed, `${VAR}` references are substituted from the
environment. This keeps secrets — your DSN and hashing key — out of the file.

| Syntax | Meaning |
| --- | --- |
| `${VAR}` | Replaced with the value of `VAR`. **Fails the run** if `VAR` is unset. |
| `${VAR:-default}` | Replaced with `VAR` if set, otherwise `default`. |

Values are resolved from **real environment variables first**, then from a
`.env` file in the current working directory if present. Real environment
variables always win, so you can override a `.env` value on the command line:

```sh
SINGLESTORE_DSN='...' ./digestive export
```

:::caution[Substitution runs over the whole file, including comments]
Expansion happens on the raw text before YAML parsing, so a literal `${...}`
in a **comment** is still treated as a variable and will fail the run if it is
unset. Avoid writing `${SOMETHING}` in prose inside your config.
:::

### `source`

The database to read from.

```yaml
source:
  dsn: ${SINGLESTORE_DSN}
```

| Key | Type | Required | Description |
| --- | --- | --- | --- |
| `dsn` | string | yes | A [go-sql-driver/mysql DSN](https://github.com/go-sql-driver/mysql#dsn-data-source-name). SingleStore is MySQL wire compatible. |

A DSN looks like `user:password@tcp(host:port)/dbname?param=value`. The
database name in the DSN is the schema exports read from. Add `?tls=true`
(or a custom TLS config) for encrypted connections.

### `destination`

Where output is written.

```yaml
destination:
  directory: ./exports
```

| Key | Type | Required | Description |
| --- | --- | --- | --- |
| `directory` | string | yes | Base directory for run output. |

The path may be **relative** (resolved against the directory you run the
command from) or **absolute**. It is created if it does not exist, including
parent directories. Each run writes into a sub-directory of this path — see
[Output layout](#output-layout).

Remote destinations (object storage, etc.) are not supported yet; v1 writes to
a local directory only.

### The `sync` block

The destination database [`digestive sync`](#sync) applies an export into. It
is **optional** — read only by `sync`; `export`, `validate`, and `restore`
ignore it entirely.

```yaml
sync:
  dsn: ${SYNC_DSN}
  type: mysql
```

| Key | Type | Required | Description |
| --- | --- | --- | --- |
| `dsn` | string | for `sync` | A [go-sql-driver/mysql DSN](https://github.com/go-sql-driver/mysql#dsn-data-source-name) for the destination. Supply via `${VAR}`. |
| `type` | string | for `sync` | The destination engine: `mysql` or `singlestore`. Selects both the driver and the [restore dialect](#the---dialect-flag-is-required). |

`sync` applies data into an **existing schema** — it never creates tables — and
loads it in a single all-or-nothing transaction. See [Sync](#sync) for the full
behaviour, the confirmation guard, and the flags.

### The `compliance` block

Turns on **audit logging**. When this block is present, `export` and `sync` write
a per-run audit record and **require** `--requester-name` / `--requester-email`.
It is **optional** — absent, the tool behaves exactly as before. See
[Compliance](#compliance) for the full behaviour and the record schema.

```yaml
compliance:
  audit:
    # Set exactly one of `directory` or `s3`.
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

| Key | Type | Required | Description |
| --- | --- | --- | --- |
| `audit.directory` | string | one of `directory`/`s3` | Local directory to write audit JSON files into. |
| `audit.s3` | map | one of `directory`/`s3` | An S3-compatible destination (see below). |
| `audit.s3.endpoint` | string | for `s3` | Host `host[:port]`, no scheme (TLS via `use_ssl`). |
| `audit.s3.bucket` | string | for `s3` | Target bucket. Must already exist. |
| `audit.s3.prefix` | string | no | Optional key prefix for written objects. |
| `audit.s3.region` | string | no | Signing region. Default `us-east-1`; use `auto` for R2. |
| `audit.s3.access_key_id` | string | for `s3` | Access key. Supply via `${VAR}`. |
| `audit.s3.secret_access_key` | string | for `s3` | Secret key. Supply via `${VAR}`. |
| `audit.s3.use_ssl` | bool | no | Use HTTPS to reach the endpoint. |
| `audit.s3.path_style` | bool | no | Force path-style addressing (MinIO/Ceph/custom domains). |

A present-but-invalid block (neither or both of `directory`/`s3`, or an `s3`
block missing a required field) is a **hard error at load time** — auditing is
never silently disabled.

### `hashing`

The secret that keys deterministic hashing.

```yaml
hashing:
  key: ${EXPORT_HASH_KEY}
```

| Key | Type | Required | Description |
| --- | --- | --- | --- |
| `key` | string | only if a `hash`, `hash_email`, or `json_anonymise` transform is used | HMAC secret for deterministic hashing. |

Supply this via `${VAR}` substitution rather than writing it inline. The same
key must be reused across runs, or hashed values (and the joins that depend on
them) will change. See [Transformers](#deterministic-hashing) for
how the key is used. If you configure a hashing transform without setting a
key, validation fails.

### `tables`

A list of the tables to export. **Export is opt-in**: only tables listed here
are exported.

Each entry is either a **bare table name** (the quick path — export the whole
table, untransformed) or a **mapping** with options:

```yaml
tables:
  - countries          # bare: full table, no transforms

  - name: users        # full form
    where: "status = 'active'"
    order_by: "id"
    limit: 5000
    columns:
      email:
        transform: hash_email
```

#### Table options

| Key | Type | Required | Description |
| --- | --- | --- | --- |
| `name` | string | yes | The table name. |
| `where` | string | no | A raw SQL `WHERE` fragment (without the `WHERE` keyword) appended to the query. |
| `order_by` | string | no | A raw SQL `ORDER BY` fragment (without the keyword). Makes `limit` deterministic. |
| `limit` | integer | no | Maximum number of rows to export from this table. |
| `columns` | map | no | Per-column transforms. Columns not listed here pass through untouched. |

`where` and `order_by` are interpolated **literally** into the query as trusted
configuration. They give you SingleStore's full expressiveness — but they are
not sanitised, so treat your config as trusted input.

:::caution[Filters do not follow foreign keys]
Row filters are applied per table with **no automatic foreign-key following**.
If you `limit` a parent table but export a child table in full, the child will
reference parent rows that were never exported. Keeping filtered tables
mutually consistent is your responsibility — write consistent `where` clauses
across related tables.
:::

#### Columns

`columns` maps a column name to a transform. Only the columns you want to
change need an entry; **every other column is exported unchanged**.

```yaml
columns:
  email:
    transform: hash_email
  ssn:
    transform: constant
    value: "REDACTED"
```

Each entry has a `transform` key naming the transform, plus any options that
transform accepts. See [Transformers](#transformers) for the full catalogue
and each transform's options and limits.

##### Excluding a column

Instead of a transform, a column entry may set `exclude: true` to drop the
column from the export entirely — it is not read from the source, not written
to Parquet, and not recorded in the manifest:

```yaml
columns:
  full_name:      # a generated/derived column
    exclude: true
```

This is intended for **generated or computed columns** (which can't be
reconstructed) and columns you simply never want to leave the database.
`exclude: true` cannot be combined with a `transform` on the same column, and a
table must have at least one column left after exclusions.

##### Anonymising inside a JSON column

A `json` column can be anonymised *in place* with the
[`json_anonymise`](#json_anonymise) transform, which keeps the
document's shape but replaces the values inside it. It takes a `json` block with
`keep` (paths passed through untouched) and `paths` (a path → transform map):

```yaml
columns:
  payload:                      # a native json column
    transform: json_anonymise
    json:
      paths:
        details.email: { transform: hash_email }
        details.phone: { transform: mask, keep_last: 3 }
      keep:
        - details.marketingConsent
```

It is **default-deny**: every leaf you don't `keep` or name in `paths` is
anonymised automatically. See [Transformers](#json_anonymise) for
the full behaviour, path syntax, and worked examples.

:::note[No schema-drift detection]
If someone later adds a new sensitive column to a table and you don't add it to
`columns`, it will be exported untransformed. There is no drift warning in v1 —
review your config when the source schema changes.
:::

### Output layout

Each run creates a sub-directory under `destination.directory`:

```
<destination>/<run-name>/
  manifest.json       # metadata; written last
  users.parquet       # one Parquet file per table
  orders.parquet
```

- **Run name** defaults to a UTC timestamp (e.g. `2026-08-14T15-04-05Z`); override
  it with `--run-name`.
- **`manifest.json`** records, per table, the ordered columns, their source
  types, nullability, the Parquet type chosen, and any transform applied. It is
  the source of truth for reconstructing exact `INSERT`s later.
- The manifest is written **only after every table succeeds**, and carries a
  `"complete": true` flag. A run directory without a `manifest.json`, or with
  `"complete": false`, is an incomplete run and should not be trusted.

### Commands and flags

```sh
digestive init                 # scaffold config.yaml + .env
digestive validate             # check config against the live schema, no export
digestive export               # run the export
digestive restore <run-dir>    # export run -> SQL script of INSERTs
digestive sync                 # export, then apply straight into a database
```

For a complete flag-by-flag matrix of every command, see the
[Command reference](/command-reference/).

#### Global flags

| Flag | Default | Description |
| --- | --- | --- |
| `--config`, `-c` | `config.yaml` | Path to the config file. |
| `--log-level` | `info` | Log verbosity: `debug`, `info`, `warn`, `error`. |
| `--json` | `false` | Emit a single JSON result on stdout and disable the TUI. See [JSON output](#json-output). |

#### `export` flags

| Flag | Default | Description |
| --- | --- | --- |
| `--run-name` | timestamp | Name of the run sub-directory. |
| `--delete-on-failure` | `false` | Remove the run directory entirely if the export fails, so repeated failures don't accumulate partial output. |
| `--no-tui` | `false` | Disable the live progress UI and log plainly instead. |
| `--json` | `false` | Emit a JSON result on stdout instead of the TUI ([JSON output](#json-output)). |
| `--requester-name` | — | Name of the requester. Required when [`compliance`](#the-compliance-block) is configured. |
| `--requester-email` | — | Email of the requester. Required when `compliance` is configured; must be a valid address. |
| `--cleanup-on-audit-fail` | `false` | Delete the run directory if the [audit record](#compliance) can't be written. |

#### `sync` flags

| Flag | Default | Description |
| --- | --- | --- |
| `--yes` | `false` | Skip the confirmation prompt (auto-skipped when non-interactive). |
| `--cleanup` | `false` | Delete the run directory after a successful apply. Ignored when a run directory is given. |
| `--dialect` | from `type` | Override the restore dialect: `singlestore` or `mysql`. |
| `--batch-size` | `1000` | Rows per multi-row `INSERT`. |
| `--allow-incomplete` | `false` | Apply even if the manifest reports an incomplete export. |
| `--ignore-restore-conf` | `false` | Ignore a `restore.yaml` in the working directory. |
| `--no-tui` | `false` | Disable the live progress UI and log plainly instead. |
| `--json` | `false` | Emit a JSON result on stdout instead of the TUI ([JSON output](#json-output)). Requires `--yes`. |
| `--requester-name` | — | Name of the requester. Required when [`compliance`](#the-compliance-block) is configured. |
| `--requester-email` | — | Email of the requester. Required when `compliance` is configured; must be a valid address. |
| `--cleanup-on-audit-fail` | `false` | Delete the run directory if the [audit record](#compliance) can't be written. |

See [Sync](#sync) for the full command reference.

#### Failure behaviour

Exports are **fail-fast**: the first error stops the run. Each Parquet file is
written atomically (to a temporary file, renamed on success), so you never get
a truncated file. Because the manifest is written last, a failed run simply
lacks a manifest. Add `--delete-on-failure` to clean up the whole run directory
on error.

## Transformers

A **transform** changes a column's values on the way out. You attach one to a
column in the [`columns`](#columns) section of a table:

```yaml
columns:
  email:
    transform: hash_email
```

Transforms fall into three families:

- **Redaction** — destroy or obscure a value: [`null`](#null),
  [`constant`](#constant), [`mask`](#mask).
- **Deterministic hashing** — replace a value with a stable pseudonym:
  [`hash`](#hash), [`hash_email`](#hash_email).
- **Anonymisation** — anonymise the values *inside* a structured value while
  keeping its shape: [`json_anonymise`](#json_anonymise).

### Summary

| Transform | Family | Applies to | NULL handling | Needs `hashing.key` |
| --- | --- | --- | --- | --- |
| [`null`](#null) | redaction | any **nullable** column | — | no |
| [`constant`](#constant) | redaction | any column | left as NULL | no |
| [`mask`](#mask) | redaction | text columns only | left as NULL | no |
| [`hash`](#hash) | hashing | text columns only | left as NULL | yes |
| [`hash_email`](#hash_email) | hashing | text columns only | left as NULL | yes |
| [`json_anonymise`](#json_anonymise) | anonymisation | `json` columns only | left as NULL | yes |

:::note[Text-only transforms]
`mask`, `hash`, and `hash_email` may only target **text columns** — `char`,
`varchar`, `text` (all sizes), `enum`, and `set`. If you point one at a numeric,
date, or binary column, `validate` and `export` fail with a clear error. This
is deliberate: numeric primary/foreign keys are best left untouched so
relationships are preserved exactly.
:::

### `null`

Sets the value to SQL `NULL`.

```yaml
deleted_reason:
  transform: "null"
```

:::note
`null` must be quoted in YAML (`"null"`), otherwise YAML parses it as an actual
null value rather than the string naming the transform.
:::

**Options:** none.

**Limits:**

- The column **must be nullable**. Targeting a `NOT NULL` column fails
  validation — otherwise the reconstructed data would violate the constraint.

### `constant`

Replaces every non-NULL value with a fixed literal.

```yaml
password:
  transform: constant
  value: "password"
```

**Options:**

| Option | Type | Required | Description |
| --- | --- | --- | --- |
| `value` | string | yes | The literal to substitute. |

**Behaviour & limits:**

- **NULL is left as NULL** — `constant` never fabricates a value where there
  wasn't one. Use `null` if you want to force NULL.
- `value` is written as text. For a **non-text** column the literal must be
  valid for that column's type (e.g. a number for an integer column), or the
  export fails when writing the value. For text columns, any string works.
- The literal is stored as-is. If you need a value the target system will treat
  specially — for example a bcrypt password hash Laravel will accept — compute
  it yourself and put the finished string in `value`.

### `mask`

Keeps a few characters at the start and/or end of a string and replaces the
middle with a fill character. Good for values you want to keep recognisable in
shape without revealing them (names, card-like strings).

```yaml
name:
  transform: mask
  keep_first: 1
  keep_last: 0
  mask_char: "*"
```

`"Super Admin"` → `"S**********"`.

**Options:**

| Option | Type | Default | Description |
| --- | --- | --- | --- |
| `keep_first` | integer | `0` | Number of leading characters to keep. |
| `keep_last` | integer | `0` | Number of trailing characters to keep. |
| `mask_char` | string | `"*"` | Single character used for the mask. |

**Behaviour & limits:**

- Text columns only.
- **NULL passes through** unchanged.
- Operates on Unicode characters (runes), not bytes, so multi-byte text masks
  correctly.
- If `keep_first + keep_last` is greater than or equal to the value's length,
  the value is returned **unchanged** — masking never reveals more characters
  than the original, and never lengthens it. (Short values are therefore not
  guaranteed to be obscured; combine with a `where` filter or a different
  transform if that matters.)
- `mask_char` must be exactly one character.

### Deterministic hashing

`hash` and `hash_email` both compute a keyed
[HMAC-SHA256](https://en.wikipedia.org/wiki/HMAC) of the value using
[`hashing.key`](#hashing). Two properties make them useful for
anonymisation:

- **Deterministic** — the same input always produces the same output, so a
  value that appears in several places is pseudonymised consistently.
- **Global by default** — hashing is keyed only on the value (and the optional
  `group`), *not* on the table or column name. So a foreign key hashed in one
  table matches the same value hashed in another, and **joins survive** in the
  exported copy.

```yaml
# users.public_id and orders.user_public_id will still join, because the same
# input hashes to the same output everywhere.
```

:::caution[The key is load-bearing]
Because the output depends on `hashing.key`, the key must stay **stable across
runs** (and across any later reconstruction). Rotating it produces a completely
different, non-joinable dataset. Keep it secret and constant.
:::

#### Hash groups

By default all hashing shares one global namespace, so two unrelated columns
that happen to hold the same literal value hash to the same output. To isolate
a column into its own namespace, give it a `group`:

```yaml
internal_ref:
  transform: hash
  group: internal   # hashed separately from the global namespace
```

Columns sharing a `group` hash consistently with each other, but differently
from other groups and from the default global namespace. Use this to
deliberately break an accidental collision.

### `hash`

Replaces a value with a hex pseudonym.

```yaml
facebook_user_id:
  transform: hash
```

`"8830682673669963"` → `"d05271e91851e2462aab3f14710ead16d1e596f85316cc6d0497411fc2ea8eac"`.

**Options:**

| Option | Type | Default | Description |
| --- | --- | --- | --- |
| `group` | string | `""` (global) | Hashing namespace — see [Hash groups](#hash-groups). |
| `length` | integer | `0` (full) | Truncate the hex output to this many characters. `0` keeps the full 64. |

**Behaviour & limits:**

- Text columns only; requires `hashing.key`.
- **NULL passes through** unchanged.
- The full output is **64 hex characters**. If the target column is narrower
  (e.g. `varchar(20)`), set `length` so the pseudonym fits — but note that a
  shorter hash increases the chance of collisions between distinct values.

### `hash_email`

Like `hash`, but the output is **email-shaped** so it satisfies validation and
formatting that expects an email address.

```yaml
email:
  transform: hash_email
```

`"admin@socialmind.io"` → `"a0b6cea60325c4fe@a11628f170.example"`.

**Options:**

| Option | Type | Default | Description |
| --- | --- | --- | --- |
| `group` | string | `""` (global) | Hashing namespace — see [Hash groups](#hash-groups). |

**Behaviour & limits:**

- Text columns only; requires `hashing.key`.
- **NULL passes through** unchanged.
- The pseudonym is derived from the **whole input value**, so identical
  addresses map to identical outputs (joins on email survive).
- The output always uses the reserved `.example` domain and is roughly 35
  characters long — it is a well-formed, non-routable placeholder, not a real
  address. It does not preserve the original local part or domain.

### `json_anonymise`

Anonymises the values *inside* a `json` column while keeping the document's
**exact shape** — every key, every level of nesting, and the null-vs-populated
distinction are preserved, so the export still deserializes into the same DTO
your application expects. Use it instead of whole-column redaction when a JSON
column mixes PII with structure you need to keep.

```yaml
columns:
  registration:              # a native json column
    transform: json_anonymise
    json:
      paths:                 # only where you want a nicer transform than the default
        details.email:     { transform: hash_email }
        details.metaEmail: { transform: hash_email }
        details.phone:     { transform: mask, keep_last: 3 }
      keep:                  # non-PII structure you want to stay readable
        - details.termsAccepted
        - details.marketingConsent
```

:::note[`json` columns only]
`json_anonymise` may only target a native `json` column, and it needs
`hashing.key`. Pointing it at any other type fails validation. The database
guarantees `json` columns are valid JSON, so anonymisation can rely on the
structure being well-formed.
:::

#### How it works: default-deny

Unlike every other transform, `json_anonymise` is **default-deny**: every leaf
you do *not* explicitly `keep` or name in `paths` is anonymised automatically.
Nothing is ever silently passed through, and nothing is ever removed — so a PII
field added to the document upstream is anonymised even if you never configured
it, and the document's shape is never altered.

This is the **opposite** of how scalar columns work (those pass through unless
you name them). It is deliberate: a JSON blob's inner fields aren't visible in
the table schema and change over time, so the safe default is to anonymise them.

The built-in rule for an unnamed leaf preserves its JSON type:

| Leaf | Becomes | Why |
| --- | --- | --- |
| a key | unchanged | keys are structure, never PII |
| `null` | `null` | never hashed — null-vs-populated semantics preserved |
| `""` (empty string) | `""` | no PII in zero bytes; hashing would *fabricate* data |
| non-empty string | a `hash` pseudonym | safe, deterministic, join-preserving |
| number | `0` | numbers can be PII (dates of birth, coordinates) |
| boolean | unchanged | a boolean is ~1 bit — realistically never PII |

:::caution[Numbers that aren't PII]
Because numbers default to `0`, a numeric field you want to **keep** (a count, an
enum-as-int, a numeric flag) must be named in `keep`. Booleans are kept
automatically; numbers are not.
:::

#### `keep` and `paths`

- **`keep`** — a list of paths passed through **untouched**. A path can name a
  single leaf or a whole subtree (naming an object/array keeps everything under
  it). This is how you expose non-PII structure, or preserve a number.
- **`paths`** — a map of path → an ordinary transform (any of `null`,
  `constant`, `mask`, `hash`, `hash_email`, with all their usual options). Use it
  when the default hash isn't good enough — e.g. an email that must stay
  email-shaped so your DTO still validates it.

Paths are **dotted**, and arrays are traversed implicitly: `contacts.email`
matches the `email` leaf of *every* element of the `contacts` array. There are no
indices or filters.

A specific `paths` entry wins over a broader `keep`: `keep: [details]` together
with `paths: { details.email: hash_email }` keeps all of `details` readable
*except* `details.email`, which is still hashed. A broad `keep` can never
accidentally expose a named PII child.

Hashes inside JSON share the **same global namespace** as scalar-column hashes
(see [Deterministic hashing](#deterministic-hashing)), so an email hashed inside
a JSON blob matches the same email hashed in a plain `email` column — joins
survive across the boundary. Use `group` on a path to isolate it.

#### Worked example

Given this cell (a typical registration DTO):

```json
{"tickets":null,"details":{"title":null,"firstName":"Daryl","lastName":"Dunn",
"email":"vonaxor@mailinator.com","metaEmail":null,"phone":null,
"address":{"address":"","address2":"","city":"","state":"","zip":"","country":""},
"dateOfBirthDay":14,"dateOfBirthMonth":7,"dateOfBirthYear":1984,
"termsAccepted":false,
"marketingConsent":{"email":true,"phone":null,"post":null,"sms":true,"whatsapp":null},
"healthInfo":{"choice":null,"details":null},
"emergencyContact":{"name":null,"relationship":null,"phone":null}},
"facebookToken":null,"facebookUser":null}
```

the config above produces:

```json
{"tickets":null,"details":{"title":null,"firstName":"c1f4a9e2b7…","lastName":"7b3d0a5518…",
"email":"a1b2c3d4e5f6a7b8@9c0d1e2f3a.example","metaEmail":null,"phone":null,
"address":{"address":"","address2":"","city":"","state":"","zip":"","country":""},
"dateOfBirthDay":0,"dateOfBirthMonth":0,"dateOfBirthYear":0,
"termsAccepted":false,
"marketingConsent":{"email":true,"phone":null,"post":null,"sms":true,"whatsapp":null},
"healthInfo":{"choice":null,"details":null},
"emergencyContact":{"name":null,"relationship":null,"phone":null}},
"facebookToken":null,"facebookUser":null}
```

Note what happened without naming most fields: `firstName`/`lastName` were hashed
(unnamed strings), the date-of-birth **numbers** became `0`, the empty `address`
strings stayed empty, every `null` stayed `null`, `marketingConsent` was kept
whole, and `email` is email-shaped because it was named with `hash_email`.

:::note[Zero-config is safe, but name your emails]
With no rules at all (`transform: json_anonymise` alone) the output is still
safe — but an `email` leaf becomes a *plain* hash with no `@`, which will fail
DTO validation. Always name email fields with `hash_email`.
:::

**Behaviour & limits:**

- `json` columns only; requires `hashing.key`.
- **NULL columns pass through** unchanged (a NULL cell, not a JSON `null`).
- The whole document is re-serialized each row, so keys may be re-ordered
  relative to the source.
- If a cell somehow isn't valid JSON, the **entire cell is redacted** (never
  passed through raw), and the run logs a count of such fallbacks — a large count
  usually means the transform is pointed at the wrong column.

## Restore

`restore` reads an export run — a `manifest.json` plus one Parquet file per
table — and writes a **single SQL script of `INSERT` statements** to standard
output, ready to pipe into the `mysql` client or paste into a SQL editor.

It connects to nothing and needs no config: the manifest and Parquet files are
the only inputs. Types are preserved for a **same-engine round-trip** — the SQL
loads into a copy of the database the export was read from.

```sh
# Write a .sql file you can hand to a SQL editor:
./digestive restore ./exports/2026-08-14T15-04-05Z --dialect singlestore > dump.sql

# Or stream straight into a client:
./digestive restore ./exports/2026-08-14T15-04-05Z --dialect mysql | mysql -D mydb
```

The SQL goes to **stdout**; logs and warnings go to stderr, so redirecting or
piping stdout never mixes the two.

:::tip[Target schema drifted?]
If the database you're loading into has changed since the export — a renamed,
added, or dropped column — the plain `INSERT`s will fail. A `restore.yaml`
reconciles the export with the drifted schema declaratively, and is picked up
automatically. See [Reconciling schema drift](#reconciling-schema-drift) below.
:::

### The `--dialect` flag is required

You must pass `--dialect singlestore` or `--dialect mysql`. There is no default —
you always declare the target engine. Today it selects the **session preamble**
wrapping the inserts; value and identifier syntax are identical across both
engines (both speak the MySQL wire protocol).

| Preamble statement | `singlestore` | `mysql` |
| --- | :---: | :---: |
| `SET NAMES utf8mb4;` | ✅ | ✅ |
| `START TRANSACTION;` … `COMMIT;` | ✅ | ✅ |
| `SET FOREIGN_KEY_CHECKS=0;` | — | ✅ |
| `SET UNIQUE_CHECKS=0;` | — | ✅ |

:::note[Why the split?]
The export applies per-table filters with no foreign-key-aware ordering, so
`INSERT`s in manifest order can reference rows that load later. On **MySQL**,
disabling foreign-key and unique checks makes the load order-independent.
**SingleStore** does not enforce foreign keys and does not recognise those
session variables — setting them would error — so its preamble omits them.
:::

`--dialect` is deliberately the seam where cross-engine type mapping (e.g. to
Postgres) would hook in later. That mapping is not built yet.

### What the output looks like

```sql
-- digestive restore — run 2026-08-14T15-04-05Z, exported 2026-08-14T15:04:05Z, dialect mysql
-- source engine: singlestore

SET NAMES utf8mb4;
SET FOREIGN_KEY_CHECKS=0;
SET UNIQUE_CHECKS=0;
START TRANSACTION;

-- table: users (2 rows)
INSERT INTO `users` (`id`, `email`, `balance`, `avatar`) VALUES
(1, 'a@example.com', '10.50', X'DEAD'),
(2, NULL, '0.00', X'');

COMMIT;
```

- **Multi-row batched `INSERT`s** with an explicit column list, so the load is
  fast and robust against column ordering in the target. Tune the batch with
  `--batch-size N` (default `1000`).
- **No DDL.** The target is assumed to be a copy of the source whose schema
  already exists; the manifest does not record enough to recreate tables
  (keys, indexes, defaults, engine, charset), so `restore` emits data only.
- **Unqualified table names.** Pick the target database with the client, e.g.
  `mysql -D dbname`.

For a machine-readable run, `restore --json` emits a **summary** (dialect and
per-table row/statement counts) instead of the SQL — see
[JSON output](#json-output).

### How values become SQL literals

Each value is rendered by the physical type the manifest recorded for its
column:

| Stored as | Rendered as | Example |
| --- | --- | --- |
| `INT64` | bare integer | `42` |
| `DOUBLE` | shortest round-trip decimal | `1.5` |
| `BYTE_ARRAY(STRING)` | single-quoted, escaped string | `'O\'Brien'` |
| `BYTE_ARRAY` (binary) | hex literal | `X'DEAD'` (empty: `X''`) |
| SQL `NULL` | the unquoted keyword | `NULL` |

The lossless-fallback types — `decimal`, `bigint unsigned`, dates and times,
`json`, `vector`, geography — are stored in Parquet as their **exact text**, so
`restore` emits them as quoted string literals and lets the engine coerce
`'10.50'` or `'2026-08-14 12:00:00'` back into the real column type. That avoids
re-formatting (and any precision loss) because the exact bytes are already in
hand.

### Guardrails

- **Incomplete exports are refused.** If `manifest.json` reports
  `complete: false` (a partial run), `restore` stops rather than emit a
  dump that silently looks complete. Pass `--allow-incomplete` to override.
- **Version and file checks are fatal.** A manifest written by a newer Digestive
  than your binary understands, or a Parquet file named in the manifest but
  missing from the directory, is a hard error.
- **Row-count mismatch is a warning.** If a Parquet file holds a different
  number of rows than the manifest recorded, `restore` warns on stderr but still
  emits every row it finds in the file.

### Reconciling schema drift

An export is an immutable production snapshot, but the database you load it into
often **drifts**. You pull an export to work on locally and your migrations have
already renamed a column, dropped one, or added a non-null column with no
default — so the plain `INSERT`s above fail at the database.

A **`restore.yaml`** in your working directory declares how to reconcile the
export with the drifted schema. If the file is present, `restore` applies it and
notes so on stderr; pass `--ignore-restore-conf` to skip it.

```sh
# restore.yaml in the current directory is picked up automatically:
./digestive restore ./exports/2026-08-14T15-04-05Z --dialect mysql | mysql -D mydb

# ...or ignore it for a run:
./digestive restore ./exports/2026-08-14T15-04-05Z --dialect mysql --ignore-restore-conf
```

Two ideas shape it:

- **Declarative — restore still connects to nothing.** You *state* the drift;
  `restore` never reads the target's schema, so it **cannot verify** your rules
  against the live database. A wrong rule fails at load time, exactly as it would
  today. This keeps `restore` runnable on a dev box with no database reachable.
- **Schema-shape only.** Reconciliation reshapes the *column set* — it never
  transforms a value's meaning. Anonymisation, hashing, and masking stay on the
  [export side](#transformers), where the key and the source types live.

:::note[Where the file lives]
`restore.yaml` is discovered from the **working directory**, not the run
directory. The rules describe *your local* drift, so they belong with your
application and its migrations — version-controlled alongside the migrations that
caused the drift, not inside the immutable export artifact.
:::

#### The five operations

```yaml
# restore.yaml
tables:
  users:
    rename_table: app_users        # emit INSERT INTO `app_users`
    rename_columns:
      full_name: display_name      # manifest column -> target column
    drop_columns:
      - legacy_flag
    add_columns:
      tenant_id:                    # a column the export predates
        value: 1                    # quoted literal; the engine coerces it
      created_at:
        value: NOW()
        raw: true                   # spliced verbatim as a SQL expression
      deleted_at:
        value: null                 # explicit SQL NULL

  audit_log:
    drop_table: true                # skip this table's INSERTs entirely
```

| Operation | Key | Effect on the emitted SQL |
| --- | --- | --- |
| Rename a column | `rename_columns` | emit the target name in the column list |
| Drop a column | `drop_columns` | omit the column — its name and every row's value |
| Add a column | `add_columns` | append a column with a constant value on every row |
| Rename a table | `rename_table` | emit `INSERT INTO <new>` |
| Drop a table | `drop_table` | skip the table's `INSERT`s entirely |

The load-bearing case for **add** is a target column that is **non-null with no
default** — omit it and the `INSERT` fails. (A *nullable* added column usually
needs no rule at all: leaving it out of the `INSERT` lets the database apply its
own default or `NULL`.)

#### Values for added columns

Each added column supplies one value, repeated on every row, rendered three ways:

| Form | YAML | Rendered as |
| --- | --- | --- |
| Literal (default) | `value: 1` | `'1'` — a quoted string literal the engine coerces |
| Explicit null | `value: null` | the unquoted `NULL` keyword |
| Raw expression | `value: NOW()` + `raw: true` | `NOW()` — spliced verbatim |

Quoting by default mirrors how `restore` emits lossless types (decimals, dates):
the engine coerces `'1'` or `'2020-01-01'` into the real column type. Use
`raw: true` only when a constant cannot express what you need (`NOW()`,
`UUID()`); like the export `where` fragment, a raw value is **trusted config**,
spliced without escaping.

Added columns are emitted in **sorted order**, so the same `restore.yaml` always
produces byte-identical SQL.

#### Every contradiction is a hard error

`restore` validates the rules against the manifest **before writing any SQL**.
Because it cannot see the target, a rule that silently matches nothing would only
fail cryptically at the database later — so a rule that applies to nothing, or
that would produce invalid SQL, stops the run:

- a **rename-source / drop / rename-table / drop-table** targeting a column or
  table **absent from the manifest** (a typo or a stale rule);
- an **`add_columns`** name that **already exists** and is not being renamed or
  dropped away (that is a rename, not an add);
- a rename **target**, or an added column, that **collides** with another
  emitted column (two columns, one name);
- a column named in both `rename_columns` and `drop_columns`;
- **`drop_table`** combined with any other rule for the same table;
- two source tables **emitting into the same target name**;
- a table left with **no columns** after drops.

:::tip[Reusing a freed name]
Renaming or dropping a column *frees* its name, so you can rename `full_name` to
`display_name` **and** add a fresh `full_name` with a different value in the same
table — a clean way to replace a column's meaning during reconciliation.
:::

#### What's out of scope

- **Type changes** — re-rendering a value into a different column type. Many
  widenings already round-trip because lossless types are emitted as quoted
  literals the engine coerces, so this is deferred rather than needed.
- **Adding a brand-new table** — it has no source data in the export, so there is
  nothing for `restore` to emit; that is a migration's job.
- **Auto-diffing the live target** — `restore` stays connection-free; detecting
  drift automatically would be a separate, larger capability.

## Sync

`sync` runs the whole pipeline end to end: it **exports** the configured tables,
generates the same `INSERT`s [`restore`](#restore) would, and **applies them
directly into a destination database** over the Go SQL driver — no `mysql`
client, no intermediate `.sql` file, no piping.

```sh
# Export fresh, then apply into the destination from your config:
./digestive sync

# Skip the export and apply an existing run directory (retry a failed apply):
./digestive sync ./exports/2026-08-14T15-04-05Z
```

The destination lives in your [config](#the-sync-block) under a `sync`
block — a DSN and a type:

```yaml
sync:
  dsn: ${SYNC_DSN}   # go-sql-driver/mysql DSN for the destination
  type: mysql        # mysql or singlestore
```

### What `sync` does

1. **Resolves and opens the destination** from `sync.dsn` / `sync.type`, and
   verifies it's reachable — so a missing config block or an unreachable target
   fails **before** any export work happens.
2. **Confirms** (when attached to a terminal) that you mean to write to that
   database — see [The confirmation guard](#the-confirmation-guard).
3. **Exports** the configured tables to a run directory — unless you passed one,
   in which case it uses that and skips the export.
4. **Applies** the run's `INSERT`s into the destination inside a **single
   transaction**.

Because step 4 reuses `restore`, the data applied is **byte-for-byte** what a
piped `restore` would load, and a `restore.yaml` in your working directory is
honoured identically. Anything `restore` does, `sync` does — the only difference
is the sink: a live database connection instead of stdout.

### Applied in a single transaction

The whole apply runs in one transaction: session setup, every table's `INSERT`s,
then `COMMIT`. **Any error rolls the whole thing back**, so the destination ends
up either fully synced or completely untouched — never half-populated.

:::note[The destination schema must already exist]
Like `restore`, `sync` emits **`INSERT`s only — it never creates tables**. Point
it at a database whose schema already exists (run your migrations first). A
missing table surfaces as a database error and rolls the sync back. If your local
schema has drifted from the export, reconcile it with a
[`restore.yaml`](#reconciling-schema-drift), exactly as you would for
`restore`.
:::

### The confirmation guard

`sync` is the one command that writes to a **live database**, so a mistyped DSN
could hit the wrong server. When stderr is a terminal it prints the resolved
destination — host and database name, never the password — and asks before
applying:

```text
About to sync into mysql database "app_staging" on db.internal:3306.
This INSERTs into existing tables in a single transaction. Continue? [y/N]
```

The prompt is **skipped automatically** when the run is non-interactive (CI, or
stderr redirected) or when you pass `--yes`, so automation is never blocked.

### Export fresh, or apply an existing run

- **`digestive sync`** (no argument) exports fresh, then applies. This needs
  `source.dsn` in your config, just like `export`.
- **`digestive sync <run-dir>`** skips the export and applies an existing run
  directory. It needs no source connection — handy for **retrying a failed
  apply** (fix the destination, re-run) without re-querying the source.

The run directory is **kept by default** — a reusable, inspectable artifact you
can re-apply or hand to `restore`. Pass `--cleanup` to delete it after a
successful apply. Cleanup only ever removes a directory `sync` created this run:
a run directory you passed in is never deleted, and a **failed apply always keeps
the directory** for inspection and retry.

### `type` selects the driver and dialect

`sync.type` resolves to both the SQL driver and the [restore
dialect](#the---dialect-flag-is-required):

| `type` | Driver | Dialect | Preamble |
| --- | --- | --- | --- |
| `mysql` | go-sql-driver/mysql | `mysql` | disables foreign-key & unique checks |
| `singlestore` | go-sql-driver/mysql | `singlestore` | charset only |

Both supported engines speak the MySQL wire protocol today, so they share one
driver. `type` is the seam a future engine (e.g. Postgres) would slot into with
its own driver and dialect. Pass `--dialect` to override the dialect `type`
implies.

### Flags

| Flag | Default | Description |
| --- | --- | --- |
| `--yes` | `false` | Skip the confirmation prompt. |
| `--cleanup` | `false` | Delete the run directory after a successful apply. Ignored when you pass an existing run directory. |
| `--dialect` | from `type` | Override the restore dialect: `singlestore` or `mysql`. |
| `--batch-size` | `1000` | Rows per multi-row `INSERT`. |
| `--allow-incomplete` | `false` | Apply even if the manifest reports an incomplete export. |
| `--ignore-restore-conf` | `false` | Ignore a `restore.yaml` in the working directory. |
| `--no-tui` | `false` | Disable the live progress UI and log plainly instead. |
| `--json` | `false` | Emit a JSON result on stdout instead of the TUI ([JSON output](#json-output)). Requires `--yes`. |
| `--requester-name` | — | Name of the requester. Required when [compliance](#compliance) is configured. |
| `--requester-email` | — | Email of the requester. Required when compliance is configured; must be a valid address. |
| `--cleanup-on-audit-fail` | `false` | Delete the run directory if the [audit record](#compliance) can't be written. |

Plus the [global flags](#global-flags) `--config` and
`--log-level`.

When a [`compliance:`](#compliance) block is configured, `sync` requires a
requester and writes an audit record **after the export but before applying** to
the destination — so data never lands without a trail. See
[Compliance](#compliance).

### What's out of scope

- **Creating the destination schema (DDL).** `sync` inserts data; it does not
  create tables. Cross-engine type translation makes DDL its own, larger
  feature.
- **Piping into an external client.** `sync` uses the Go driver directly. An
  external-client escape hatch (for engines without a Go driver) is a possible
  future addition; `type` is where it would hook in.
- **Postgres.** Designed for — `type` is the seam — but not built yet.

## JSON output

Every command accepts a persistent `--json` flag for machine consumers — a web
app shelling out to the binary, CI, or a script that needs to read back what
happened rather than parse human text.

Under `--json`:

- **stdout carries exactly one JSON object** — pretty-printed, with a trailing
  newline, and nothing else. The live TUI is disabled.
- It applies to **success and failure alike**. A failing run still prints a JSON
  object (with `"status": "error"`) and the process still exits non-zero, so
  `&&` chains and CI keep working. A consumer never has to scrape stderr.
- It implies **quiet**: diagnostic logging on stderr is suppressed unless you
  raise it explicitly with `--log-level`. stdout stays pure JSON regardless.

### The envelope

Every command emits the same top-level shape, so a consumer has one parse path.
The per-command payload lives in `result`:

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

| Field            | Meaning                                                            |
| ---------------- | ----------------------------------------------------------------- |
| `schema_version` | Envelope version; bumps only on a breaking shape change.          |
| `command`        | Which subcommand ran.                                             |
| `status`         | `"ok"` or `"error"`; mirrors the exit code.                       |
| `error`          | `null` on success, a string on failure.                          |
| `warnings`       | Always an array (empty when there are none).                      |
| `result`         | Command-specific payload; `null` on error.                        |

### Per-command `result`

#### `export`

`run_dir`, `run_id`, per-table `{name, rows}`, and `total_rows` — drawn from the
manifest the run just wrote, so the payload never diverges from the artifact on
disk.

#### `sync`

The same table/row summary as `export`, plus:

```json
"result": {
  "run_dir": "exports/2026-08-14T15-04-05Z",
  "run_id": "2026-08-14T15-04-05Z",
  "tables": [{ "name": "users", "rows": 48210 }],
  "total_rows": 48210,
  "applied": true,
  "destination": { "type": "mysql", "host": "db.internal", "database": "app" }
}
```

`run_dir` is `null` when `--cleanup` removed it after a successful apply.

Because `sync` writes to a live database, under `--json` it **requires `--yes`**:
a machine consumer cannot answer the interactive confirmation prompt, so the
flag must be passed explicitly to acknowledge the write. Without it, `sync`
returns a JSON error (`"--yes is required with --json"`) and does nothing.

#### `restore`

`restore --json` emits a **summary**, not SQL:

```json
"result": {
  "run_dir": "exports/2026-08-14T15-04-05Z",
  "dialect": "mysql",
  "tables": [{ "name": "users", "rows": 48210, "statements": 49 }],
  "total_statements": 49
}
```

No SQL is written to stdout under `--json`. When you want the script itself, use
plain [`restore`](#restore) and capture stdout; to apply the data directly, use
[`sync`](#sync).

#### `validate`

`tables` (the names that validated, in config order) and `table_count`.

#### `init`

`created` — the files written (`.env`, `config.yaml`). The freshly generated
hashing key is **never** included in the output; it lives only in `.env`.

### The invocation boundary

The JSON contract covers **execution-time outcomes**. A malformed *invocation*
that `cobra` rejects before the command runs — an unknown flag or command, a
missing required argument like `restore`'s `--dialect` — still errors
conventionally on stderr. A consumer should treat "stdout did not parse as JSON"
as a failed invocation and read stderr for the reason.

## Compliance

For regulated data, you often need to answer — after the fact — **who** pulled an
export, **when**, under **what config**, and **how much** data left each table.
Digestive's compliance mode writes a single **audit record** (a JSON document)
for every [`export`](#export-flags) and [`sync`](#sync), to
either an **S3-compatible bucket** or a **local directory**.

The feature is **opt-in and gated by config**. It is off unless a `compliance:`
block is present — and when it is present, it is **mandatory**: you can't run an
export or sync without recording who requested it.

### Turning it on

Add a `compliance:` block to your `config.yaml`. Set **exactly one** of
`directory` (local) or `s3` (S3-compatible):

```yaml
compliance:
  audit:
    # Local directory:
    directory: ./audit-logs

    # …or an S3-compatible bucket (set one, not both):
    s3:
      endpoint: ${AUDIT_S3_ENDPOINT}       # host[:port], no scheme
      bucket: ${AUDIT_S3_BUCKET}
      prefix: exports/                     # optional key prefix
      region: ${AUDIT_S3_REGION:-us-east-1}
      access_key_id: ${AUDIT_S3_ACCESS_KEY}
      secret_access_key: ${AUDIT_S3_SECRET_KEY}
      use_ssl: true
      path_style: true                     # MinIO / Ceph / custom domains
```

Once this block is present, `export` and `sync` **require** a requester:

```sh
./digestive export \
  --requester-name  "Jane Auditor" \
  --requester-email "jane@example.com"
```

Both flags are validated **before any work starts** — the name must be non-empty
and the email must parse as an address. Omit either and the command fails
immediately.

:::note[Present means on; broken means error]
The mere presence of `compliance:` switches auditing on — there is no separate
"enable" flag to forget. A present-but-malformed block (neither or both of
`directory`/`s3`, or an `s3` block missing a required field) is a **hard error at
load time**, never a silent no-op. "On but broken" fails loudly.
:::

### What's in the record

One JSON document is written per run. All actions share the same schema; the
`action` field (`export`, `sync`, …) distinguishes them.

```json
{
  "audit_version": 1,
  "action": "export",
  "requester": { "name": "Jane Auditor", "email": "jane@example.com" },
  "hostname": "worker-03.internal",
  "timestamps": {
    "export_started_at": "2026-08-19T14:30:00Z",
    "audit_written_at":  "2026-08-19T14:31:12Z"
  },
  "output": {
    "run_name": "2026-08-19T14-30-00Z",
    "run_directory": "/data/exports/2026-08-19T14-30-00Z"
  },
  "config":   { "…": "the effective config, secrets redacted" },
  "manifest": { "…": "the full manifest.json, embedded inline" },
  "row_counts": { "users": 10432, "orders": 88123 },
  "tool_version": "1.4.2"
}
```

| Field | What it captures |
| --- | --- |
| `action` | `export` or `sync` (one record per command run). |
| `requester` | The name and email from the required flags. |
| `hostname` | The machine that produced the export. |
| `timestamps` | When the export started (`manifest.created_at`) and when the record was written, both RFC3339 UTC. |
| `output` | The run name and directory the export produced. |
| `config` | The **resolved, effective** config that governed the run — with secrets redacted (see below). |
| `manifest` | The full [`manifest.json`](#output-layout) embedded inline. |
| `row_counts` | A flat `table → rows` map. |
| `tool_version` | The `digestive` build that ran. |

#### Secrets are redacted

The config is the compliance-relevant part of the record — which tables, columns,
and transforms governed what left the database — but it also holds secrets.
Digestive **redacts** them before embedding, replacing each with
`***REDACTED***`:

- `source.dsn` and `sync.dsn` (the whole DSN),
- `hashing.key`,
- the S3 `access_key_id` and `secret_access_key`.

Everything else — table/column config, transforms, the destination directory,
`sync.type` — is preserved. The audit record never contains a live credential.

### When the record is written

The record needs the completed manifest and row counts, so it is written **only
on success**, right after the export finishes.

- **`export`** writes the record after the export completes, then prints the run
  directory as usual.
- **`sync`** writes the record after the export succeeds but **before applying**
  to the destination — so data can never land in the target without an audit
  trail having been written first. A sync that reuses an existing run directory
  still records a `sync` entry.

#### If the audit write fails

If the record can't be written (bucket unreachable, permission denied, disk
full), the command **fails with a non-zero exit**. A silently-missing audit trail
is the worst outcome for a compliance control, so this is deliberate — a
momentary outage turns a good export into a failed command. The exported files
still exist on disk; you're simply told the record didn't land.

To go further and **enforce** "no export without an audit," pass
`--cleanup-on-audit-fail`: on an audit-write failure, the run directory is
deleted, so a completed export is never left behind without its trail.

```sh
./digestive export \
  --requester-name "Jane" --requester-email "jane@example.com" \
  --cleanup-on-audit-fail
```

This composes with `--delete-on-failure`, which handles *export* failure;
`--cleanup-on-audit-fail` handles *audit* failure.

### The audit object name

Each record is written as `<run-name>-<hostname>-<random>.json`. The hostname
attributes origin and the random suffix guarantees uniqueness, so many people or
machines can write into **one shared bucket or directory** without colliding —
even when two runs share a timestamp-based run name. (For S3, the configured
`prefix` is prepended to the key.)

### S3-compatible storage

The S3 sink works against any S3-compatible service — MinIO, Cloudflare R2, Ceph,
Wasabi, and AWS S3. A few notes:

- **`endpoint`** is a bare `host[:port]` with **no scheme** — TLS is controlled by
  `use_ssl`, not the URL. (A leading `http(s)://` is tolerated and stripped.)
- **`path_style: true`** forces path-style bucket addressing, which MinIO, Ceph,
  and custom domains generally require. Leave it off for AWS S3.
- **`region`** defaults to `us-east-1`; use `auto` for Cloudflare R2.
- The **bucket must already exist** — Digestive uploads the record but never
  creates the bucket.
- Supply `access_key_id` / `secret_access_key` via `${VAR}` substitution so
  credentials stay out of the file.

### Compliance flags

These flags are added to both `export` and `sync`:

| Flag | Default | Description |
| --- | --- | --- |
| `--requester-name` | — | Name of the person requesting the export. **Required** when compliance is configured. |
| `--requester-email` | — | Email of the requester. **Required** when compliance is configured; must be a valid address. |
| `--cleanup-on-audit-fail` | `false` | Delete the run directory if the audit record can't be written. |

When no `compliance:` block is present, these flags are ignored and everything
behaves exactly as before.
