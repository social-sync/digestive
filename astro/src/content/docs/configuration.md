---
title: Configuration
description: An exhaustive guide to the Digestive config file.
---

Digestive is driven by a single YAML file (`config.yaml` by default). This
page documents every option.

## A complete example

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

## Environment variable substitution

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

## `source`

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

## `destination`

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

## `sync`

The destination database [`digestive sync`](/sync/) applies an export into. It
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
| `type` | string | for `sync` | The destination engine: `mysql` or `singlestore`. Selects both the driver and the [restore dialect](/restore/#the---dialect-flag-is-required). |

`sync` applies data into an **existing schema** — it never creates tables — and
loads it in a single all-or-nothing transaction. See [Sync](/sync/) for the full
behaviour, the confirmation guard, and the flags.

## `compliance`

Turns on **audit logging**. When this block is present, `export` and `sync` write
a per-run audit record and **require** `--requester-name` / `--requester-email`.
It is **optional** — absent, the tool behaves exactly as before. See
[Compliance](/compliance/) for the full behaviour and the record schema.

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

## `hashing`

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
them) will change. See [Transformers](/transformers/#deterministic-hashing) for
how the key is used. If you configure a hashing transform without setting a
key, validation fails.

## `tables`

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

### Table options

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

### Columns

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
transform accepts. See [Transformers](/transformers/) for the full catalogue
and each transform's options and limits.

#### Excluding a column

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

#### Anonymising inside a JSON column

A `json` column can be anonymised *in place* with the
[`json_anonymise`](/transformers/#json_anonymise) transform, which keeps the
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
anonymised automatically. See [Transformers](/transformers/#json_anonymise) for
the full behaviour, path syntax, and worked examples.

:::note[No schema-drift detection]
If someone later adds a new sensitive column to a table and you don't add it to
`columns`, it will be exported untransformed. There is no drift warning in v1 —
review your config when the source schema changes.
:::

## Output layout

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

## Commands and flags

```sh
digestive init                 # scaffold config.yaml + .env
digestive validate             # check config against the live schema, no export
digestive export               # run the export
digestive restore <run-dir>    # export run -> SQL script of INSERTs
digestive sync                 # export, then apply straight into a database
```

### Global flags

| Flag | Default | Description |
| --- | --- | --- |
| `--config`, `-c` | `config.yaml` | Path to the config file. |
| `--log-level` | `info` | Log verbosity: `debug`, `info`, `warn`, `error`. |

### `export` flags

| Flag | Default | Description |
| --- | --- | --- |
| `--run-name` | timestamp | Name of the run sub-directory. |
| `--delete-on-failure` | `false` | Remove the run directory entirely if the export fails, so repeated failures don't accumulate partial output. |
| `--requester-name` | — | Name of the requester. Required when [`compliance`](#compliance) is configured. |
| `--requester-email` | — | Email of the requester. Required when `compliance` is configured; must be a valid address. |
| `--cleanup-on-audit-fail` | `false` | Delete the run directory if the [audit record](/compliance/) can't be written. |

### `sync` flags

| Flag | Default | Description |
| --- | --- | --- |
| `--yes` | `false` | Skip the confirmation prompt (auto-skipped when non-interactive). |
| `--cleanup` | `false` | Delete the run directory after a successful apply. Ignored when a run directory is given. |
| `--dialect` | from `type` | Override the restore dialect: `singlestore` or `mysql`. |
| `--batch-size` | `1000` | Rows per multi-row `INSERT`. |
| `--allow-incomplete` | `false` | Apply even if the manifest reports an incomplete export. |
| `--ignore-restore-conf` | `false` | Ignore a `restore.yaml` in the working directory. |
| `--no-tui` | `false` | Disable the live progress UI and log plainly instead. |
| `--requester-name` | — | Name of the requester. Required when [`compliance`](#compliance) is configured. |
| `--requester-email` | — | Email of the requester. Required when `compliance` is configured; must be a valid address. |
| `--cleanup-on-audit-fail` | `false` | Delete the run directory if the [audit record](/compliance/) can't be written. |

See [Sync](/sync/) for the full command reference.

### Failure behaviour

Exports are **fail-fast**: the first error stops the run. Each Parquet file is
written atomically (to a temporary file, renamed on success), so you never get
a truncated file. Because the manifest is written last, a failed run simply
lacks a manifest. Add `--delete-on-failure` to clean up the whole run directory
on error.
