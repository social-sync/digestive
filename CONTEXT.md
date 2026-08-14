# Context

Ubiquitous language for the database exporter. A glossary, not a spec.

## Purpose

Produce a sanitised, referentially-usable copy of production data for use in
non-production environments (dev / staging / test). PII is anonymised, redacted,
or deterministically hashed on the way out. Output round-trips back into exact
`INSERT` statements later.

## Terms

### Source
The remote database an export reads from. First concrete target is **SingleStore**
(MySQL wire compatible). "Source" is the abstraction; SingleStore is one
implementation.

### Export run
A single invocation of the tool that reads selected tables from a Source,
applies transformations, and writes the results to a Destination. Produces data
files plus one Manifest.

### Destination
Where an export run writes its output. v1: a local directory. Designed so remote
storage (e.g. object storage) can be added later without changing the export
model.

### Manifest
A single metadata file written per export run. Records, per table: ordered
column list, original Source column types, nullability, and the mapping from
Source type to the storage format's type. It is the source of truth for
restoring exact INSERTs; the data files hold only values.

### Transformation
A rule applied to a column's values on the way out. Families:
- **Redaction** — remove or mask a value (no attempt at realism). v1 built-ins:
  `null`, `constant`, `mask`.
- **Deterministic hashing** — map a value to a stable pseudonym so the same
  input always yields the same output (enabling joins to survive). Keyed by an
  HMAC secret sourced from the environment. **Global by default**: the same
  input string yields the same pseudonym in every table/column, so foreign-key
  relationships survive with no extra config. Only applies to text/string
  columns (the tool rejects a hash transform aimed at a non-text column). An
  **email** variant preserves email shape (`local@domain`).

- **Anonymisation** — keep a structured value's shape but anonymise the data
  inside it. v1 built-in: `json_anonymise`, which walks a JSON document and
  anonymises its leaves in place (default-deny) while preserving keys, nesting,
  and null-vs-set, so the document still deserializes. See
  [ADR-0004](./docs/adr/0004-json-anonymise-structural-in-place-anonymisation.md).

> **Deferred:** a **realistic faker** capability (plausible fake values via a
> faker library — names, emails, phones, addresses) is out of scope for v1.
> `json_anonymise` produces *safe* leaf values (hashes, zeros), not realistic
> ones; faker-backed leaf transforms are the deferred part. The transform
> registry is designed so they can be added later without disruption.

### Hash group
An optional label that scopes deterministic hashing to a namespace. Columns
sharing a group hash into the same space; the default (no group) is one global
space. Used to deliberately isolate columns that would otherwise collide.

### Identity space
The set of values that must hash consistently together to preserve a
relationship (e.g. a text/UUID key referenced across tables). The global default
is a single identity space; hash groups create additional ones.

### Table selection
Export is **opt-in per table**: nothing is exported unless the table is listed
in config. A table listed with no options exports in full, untransformed — the
quick path. Columns are pass-through by default; the operator only names the
columns that need a Transformation. Unlisted columns are kept, untransformed.
v1 does **not** detect schema drift (a newly added, unlisted column exports as-is).

### Query condition
Per-table controls that reduce the rows an export pulls: a raw SQL `WHERE`
fragment, an optional `LIMIT`, and an optional `ORDER BY` (to make a `LIMIT`
deterministic). Interpolated literally into the `SELECT` — treated as trusted
config, not untrusted input.

> **Sharp edge (v1):** filters are applied per table with **no automatic
> foreign-key following**. If a child table is exported unfiltered while its
> parent is limited, the child will reference rows that were never exported.
> Keeping filtered tables referentially consistent is the operator's
> responsibility. FK-aware subsetting is explicitly deferred.

### Restore
Reading data files + Manifest and emitting `INSERT` statements. Lives in the
same single binary, exposed as the `restore` command. It reads only an export
run directory — no config, no Source connection — and streams a single SQL
script of batched multi-row `INSERT`s to stdout, ready for the `mysql` client or
a SQL editor. Types are preserved for a same-engine round-trip: the Manifest's
recorded Source types drive how each value becomes a SQL literal, and the
lossless-fallback types (see below) are emitted as quoted string literals the
engine coerces back. A required `--dialect` (`singlestore` or `mysql`) selects
the session preamble. See
[ADR-0005](./docs/adr/0005-restore-to-sql.md).

Two future capabilities the format still anticipates (and which shaped it):
- **Streaming load** — stream each Parquet file, convert to INSERTs on the fly,
  and pipe directly into a destination DB (no intermediate `.sql` file needed).
- **Cross-engine type mapping** — a separate mapping config that translates
  Source types into another engine's types (Postgres is the primary target),
  so a SingleStore export can be restored into a different DB engine. This is
  why the Manifest records full, precise Source type information, and why
  `--dialect` is the seam it would hook into. **Deferred.**

## v1 scope

In scope: `export` (config → Parquet + Manifest), `validate` (parse config,
expand env, connect, check tables/columns exist, no export), and `restore`
(export run → single SQL script of INSERTs, same-engine round-trip). Everything
else above — realistic fakers, remote Destinations, FK-aware subsetting,
streaming load piped into a live DB, cross-engine mapping — is deferred but
designed for.
