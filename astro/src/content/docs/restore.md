---
title: Restore
description: Turn an export run back into a single SQL script of INSERT statements.
---

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

## The `--dialect` flag is required

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

## What the output looks like

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

## How values become SQL literals

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

## Guardrails

- **Incomplete exports are refused.** If `manifest.json` reports
  `complete: false` (a partial run), `restore` stops rather than emit a
  dump that silently looks complete. Pass `--allow-incomplete` to override.
- **Version and file checks are fatal.** A manifest written by a newer Digestive
  than your binary understands, or a Parquet file named in the manifest but
  missing from the directory, is a hard error.
- **Row-count mismatch is a warning.** If a Parquet file holds a different
  number of rows than the manifest recorded, `restore` warns on stderr but still
  emits every row it finds in the file.

## Reconciling schema drift

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
  [export side](/transformers/), where the key and the source types live.

:::note[Where the file lives]
`restore.yaml` is discovered from the **working directory**, not the run
directory. The rules describe *your local* drift, so they belong with your
application and its migrations — version-controlled alongside the migrations that
caused the drift, not inside the immutable export artifact.
:::

### The five operations

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

### Values for added columns

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

### Every contradiction is a hard error

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

### What's out of scope

- **Type changes** — re-rendering a value into a different column type. Many
  widenings already round-trip because lossless types are emitted as quoted
  literals the engine coerces, so this is deferred rather than needed.
- **Adding a brand-new table** — it has no source data in the export, so there is
  nothing for `restore` to emit; that is a migration's job.
- **Auto-diffing the live target** — `restore` stays connection-free; detecting
  drift automatically would be a separate, larger capability.
