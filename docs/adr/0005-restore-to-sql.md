---
status: accepted
---

# Restore an export run to a single SQL script of INSERTs

## Context and decision

An export run is a self-describing artifact: one `manifest.json` plus one
Parquet file per table (ADR-0001). The `restore` command turns that artifact
back into data you can load — a **single SQL script of `INSERT` statements**
streamed to stdout, ready to pipe into the `mysql` client or paste into a SQL
editor. It reads only the run directory: no `config.yaml`, no database
connection. The manifest is the source of truth for column order and types; the
Parquet files hold the values.

Two decisions here are load-bearing.

### Required `--dialect`, with engine-specific preambles

`restore` requires `--dialect singlestore|mysql`; there is no default, so the
operator always declares the target engine. Today the dialect affects **only the
session preamble**:

- **Both:** `SET NAMES utf8mb4;` and a `START TRANSACTION;` … `COMMIT;` wrapper
  (atomic, and much faster than autocommit-per-row).
- **`mysql` only:** additionally `SET FOREIGN_KEY_CHECKS=0;` and
  `SET UNIQUE_CHECKS=0;`.

The split is forced by the target. The export applies per-table filters with
**no foreign-key-aware ordering** (a documented sharp edge), so `INSERT`s emitted
in manifest order can reference rows that sort later or were filtered out. On
MySQL, disabling FK/unique checks makes the load order-independent. On
SingleStore those variables are unsupported — SingleStore does not enforce
foreign keys and does not recognise them, so setting them would *error* — and
they are also unnecessary there. Defaulting to one preamble would break the other
engine, so the flag is required rather than defaulted.

Value and identifier syntax are identical across both engines (both speak the
MySQL wire protocol), so `--dialect` swaps nothing else yet. It is deliberately
the seam where cross-engine type mapping (Postgres — see ADR-0001/0003) would
later hook in.

### Lossless types render as quoted string literals

Values become SQL literals by their recorded Parquet physical type:

- `INT64` → bare integer; `DOUBLE` → shortest round-trip decimal.
- `BYTE_ARRAY(STRING)` → single-quoted, backslash-escaped string literal.
- `BYTE_ARRAY` (binary) → `X'..'` hex literal (`X''` for the empty blob).
- SQL `NULL` → the unquoted keyword.

The lossless-fallback types (decimal, `bigint unsigned`, dates/times, JSON,
vector, geography) are stored in Parquet as their **exact text** already
(ADR-0003), so `restore` emits them as quoted string literals and lets the engine
coerce `'10.50'`, `'2026-08-14 12:00:00'`, or `'18446744073709551615'` into the
real column type. This sidesteps re-formatting decimals and dates — and the
precision loss that reconstructing bare numeric/temporal literals would risk —
because the exact bytes are already in hand.

## Considered options

- **DDL / `CREATE TABLE` in the output:** rejected. The manifest records column
  types but not full schema (primary keys, indexes, defaults, `AUTO_INCREMENT`,
  engine, charset, generated columns), and the target is a pre-existing copy of
  the source. Data only.
- **One `INSERT` per row:** rejected for load performance and size. `restore`
  emits multi-row batches (default 1000 rows/statement, `--batch-size`), with an
  explicit column list so the load is robust against target column ordering.
- **Bare numeric/temporal literals for lossless types:** rejected. It would mean
  re-parsing and re-formatting exact text, risking precision changes, for no
  benefit — the engine coerces the quoted form losslessly.
- **Writing to a default output file:** rejected. Output goes to stdout so it
  streams straight into `mysql`; shell redirection covers the file case.

## Consequences

- `restore` refuses a run whose manifest reports `complete=false` unless
  `--allow-incomplete` is passed, so a partial export cannot silently produce a
  "successful"-looking dump. A manifest `version` newer than the binary, or a
  missing Parquet file, is a hard error; a Parquet-vs-manifest row-count
  mismatch is a warning.
- Tables are emitted in manifest order and left **unqualified**; the operator
  picks the target database with the client (`mysql -D dbname`).
- Streaming load piped into a live database, FK-aware ordering, byte-aware batch
  capping, and cross-engine type mapping remain deferred, but the format and this
  command are shaped so they can be added without re-exporting.
