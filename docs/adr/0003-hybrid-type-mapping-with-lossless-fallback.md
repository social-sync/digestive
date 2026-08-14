---
status: accepted
---

# Hybrid type mapping: native Parquet where safe, lossless fallback elsewhere

## Context and decision

SingleStore's type system does not map cleanly onto Parquet's logical types. We
decided on a **hybrid** mapping strategy:

- Map clean scalar types to their native Parquet logical type (`INT`→INT64,
  `VARCHAR`→STRING, `DATETIME`→TIMESTAMP, `DECIMAL`→Parquet DECIMAL, `BLOB`→
  BYTE_ARRAY, etc.) so the common case is properly typed and inspectable.
- For types with no safe native Parquet equivalent — `JSON`, `BSON`, `VECTOR`,
  geospatial, `BIGINT UNSIGNED` (exceeds signed INT64 range), MySQL zero-dates
  (`'0000-00-00'`), `ENUM`/`SET` — fall back to a **lossless string/bytes
  representation**, with the Manifest recording the true Source type and encoding.

The principle: **never silently corrupt a value to force a native type.**
Correctness beats prettiness. Reconstruction trusts the Manifest's recorded
Source type, not Parquet's inferred type.

The per-type mapping behaviour MUST be documented (a type-mapping table shipped
with the tool), because the choice is invisible in the data files themselves.

## Considered options

- **Best-fit native typing for everything:** rejected — risks silent data
  corruption on the edges (unsigned overflow, zero-dates, precision loss).
- **Fully lossless string/bytes for every column:** rejected as the default — it
  throws away the typed-inspectability that motivated choosing Parquet at all
  (ADR-0001). Kept only as the fallback for awkward types.

## Consequences

- A documented type-mapping table is a v1 deliverable.
- The lossless-fallback columns are where the **deferred** cross-engine mapping
  (SingleStore → Postgres) will do most of its work: because the Manifest records
  the exact Source type and encoding, a future mapping config can translate those
  types into a destination engine's equivalents without re-exporting. `reconstruct`
  and streaming load are not built in v1, but this mapping strategy is chosen with
  them in mind.
