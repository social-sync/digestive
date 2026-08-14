---
status: accepted
---

# Export to Parquet data files plus a per-run JSON manifest

## Context and decision

The tool exports a Source database (SingleStore first, MySQL-wire) to local
files that must later be reconstructed into exact `INSERT` statements. We decided
the on-disk format is **one Parquet file per table plus a single per-run
`manifest.json`**, rather than SQL dump files or CSV.

Parquet is columnar, typed, compressed, and carries its own schema — it preserves
nulls and types far better than CSV (which is type-blind) and is not the SQL
format we're explicitly trying to avoid. But Parquet's type system does not map
1:1 onto SingleStore's, so Parquet alone cannot guarantee exact reconstruction.
The manifest closes that gap: per table it records the ordered column list, the
precise Source column types, nullability, and the Source-type → Parquet-type
mapping used. The manifest is the source of truth for reconstruction; the Parquet
files hold only values.

## Considered options

- **SQL dump (`INSERT` statements):** explicitly rejected — the brief wants a
  format that is *not* INSERTs but can become them later.
- **CSV + schema sidecar:** rejected. CSV is type-blind (loses null-vs-empty,
  numeric vs string, binary), forcing heavy re-parsing against the sidecar for
  every value. Parquet carries types natively.
- **Parquet only, no manifest:** rejected. Parquet's logical types can't
  represent all SingleStore types exactly (see ADR-0003), so reconstruction
  would be lossy without recorded Source types.

## Consequences

- Parquet files are self-describing enough to inspect, but reconstruction MUST
  trust the manifest's Source types, not Parquet's inferred types.
- The manifest must be rich enough to drive the **deferred** future work:
  `reconstruct` (Parquet + manifest → INSERTs), a streaming load (Parquet →
  INSERTs → piped into a destination DB), and cross-engine type mapping
  (SingleStore → Postgres). None of these are built in v1, but the format is
  designed so they can be added without re-exporting. This is the main reason
  the manifest captures full, precise Source type information now.
