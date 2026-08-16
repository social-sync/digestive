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
./digestive restore ./exports/20260814T150405Z --dialect singlestore > dump.sql

# Or stream straight into a client:
./digestive restore ./exports/20260814T150405Z --dialect mysql | mysql -D mydb
```

The SQL goes to **stdout**; logs and warnings go to stderr, so redirecting or
piping stdout never mixes the two.

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
-- digestive restore — run 20260814T150405Z, exported 2026-08-14T15:04:05Z, dialect mysql
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
