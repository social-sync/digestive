---
status: accepted
---

# `json_anonymise`: structural, in-place anonymisation of JSON columns

## Context and decision

Some `json` columns hold structured documents (typically a serialized DTO) that
mix PII with non-sensitive structure. Whole-column redaction (`null` /
`constant`) is safe but destroys the document, and consumers that deserialize
the export back into a typed object (e.g. a PHP DTO) need the **keys, nesting,
and null-vs-populated distinctions preserved** or deserialization fails. We need
a transform that keeps the *shape* of the JSON while anonymising the *values*
inside it.

We decided on a new transform, **`json_anonymise`**, in the deferred
"anonymisation" family (see [CONTEXT.md](../../CONTEXT.md)). Its design:

- **In-place, default-deny.** The document's shape is preserved exactly; every
  leaf the operator does not explicitly `keep` or name is anonymised by a
  built-in rule. Unnamed leaves are **never passed through and never removed** —
  removing them would change the shape the feature exists to preserve, and
  passing them through would leak PII nobody configured. Default-deny is the
  safety net for fields that are invisible in the schema and drift over time.
  This is the **opposite** default from scalar columns (which are default-allow,
  pass-through unless named) — deliberately, because a JSON blob's internal
  fields cannot be seen or reasoned about from the table schema.

- **Type-preserving leaf rules.** Anonymising a leaf keeps its JSON type so the
  document still deserializes:
  - **keys** — preserved verbatim, always (keys are structure, never PII).
  - **null** — stays `null` (never hashed).
  - **empty string `""`** — passed through unchanged. There is no PII in zero
    bytes, and hashing it would *fabricate* a populated value where prod had
    none, breaking empty-vs-populated semantics. Mirrors the existing principle
    that `constant` never fabricates a value from NULL.
  - **non-empty string** — replaced with a keyed `hash` in the global identity
    space (below).
  - **number** — replaced with `0` (anonymised; default-deny).
  - **boolean** — **kept unchanged**. A boolean carries ~1 bit and is realistically
    never PII, so flipping it is all cost (changed semantics for consent flags,
    etc.) and no safety benefit.

- **Numeric PII is the operator's responsibility.** Because numbers default to
  `0` but booleans are kept, the only default-allow hole is *not present*: numbers
  are anonymised. To **preserve** a specific number (a count, an enum-as-int) the
  operator names its path with `keep`. To give a leaf a nicer transform than the
  default (an email that must stay email-shaped, a phone to mask) they name it in
  `paths`.

- **Path language: dotted paths with implicit array traversal**, not JSONPath.
  A path like `details.email` matches that leaf; `contacts.email` matches the
  `email` leaf of *every* element of the `contacts` array (arrays are transparent
  to the path). No indices, no filters, no recursive descent. Two rule kinds:
  - **`keep`** — a list of paths (leaf or whole subtree) passed through untouched;
    the opt-out from default-deny.
  - **`paths`** — a map of path → an ordinary column transform spec, reusing the
    **existing transform catalogue verbatim** (`null`, `constant`, `mask`,
    `hash`, `hash_email`). A JSON leaf is just a string cell, which is exactly
    what the `Transformer` interface already consumes.
  - **Precedence:** a specific `paths` entry overrides an ancestor `keep`
    (so `keep: [details]` + `paths: {details.email: hash_email}` still hashes the
    email — a broad keep can never accidentally expose a named PII child).

- **Global identity space by default.** Leaf hashes share the same HMAC key and
  namespace as scalar-column hashes (see [ADR-0002](./0002-global-deterministic-hmac-hashing.md)),
  so an email hashed inside JSON matches the same email hashed in a scalar column
  and joins survive across the boundary. The `group` knob is available per path
  for isolation.

- **Native `json` columns only.** The transform is rejected on any other column
  type (a new `IsJSON` gate mirroring the existing text-only gate). SingleStore
  guarantees `json` columns are valid JSON, so the unparseable path is a genuine
  rarity rather than routine.

- **Fail-safe on unparseable input.** If a cell somehow does not parse, the whole
  cell is **redacted** (collapsed to the safe fallback), never passed through raw
  and never aborting the run — and a per-run fallback **counter is logged** so a
  mis-targeted column (everything falling back) is visible rather than silent.

## Considered options

- **Remove unnamed leaves (strict allowlist):** rejected — it changes the
  document's shape to the operator's allowlist, defeating the "export the
  structure" goal and breaking DTO deserialization (missing keys, lost
  null-vs-absent distinction).
- **Pass unnamed leaves through:** rejected — silently leaks any PII field added
  upstream that nobody configured. Default-deny is the whole point.
- **Full JSONPath (filters, indices, recursive descent):** rejected for v1 —
  heavier dependency, slower matching, far more surface area; index/filter
  precision is rarely wanted for anonymisation. Dotted + implicit-array covers
  the real cases.
- **A configurable `default` section** to override the unnamed-leaf fallback per
  type: deferred — the fixed built-in fallback (string→hash, number→0,
  bool→keep, null/empty→passthrough) covers the need; a knob to isolate or harden
  the fallback can be added later without disruption.
- **`byte-patch` a few paths (`sjson`/`gjson`):** rejected — that model fits
  "set 3 fields and copy the rest", but default-deny rewrites *every* leaf, so
  the whole document changes and the patch model gives no advantage.
- **Anonymise booleans (`→ false`) / keep numbers:** rejected — booleans are
  effectively never PII (flipping consent flags is pure cost), whereas numbers
  genuinely can be PII (dates of birth, coordinates), so the safe split is
  keep-booleans / anonymise-numbers.

## Consequences

- **Performance.** Every leaf of every JSON cell is touched every row, so the
  document is fully re-serialized — there is no partial-patch shortcut. The cost
  is bounded and controlled by three non-negotiables:
  1. **Parse only when needed** — a `json` column with no `json_anonymise`
     transform is never decoded; it streams through as raw bytes as today.
  2. **Compile the rule set once** — the `keep`/`paths` list is compiled in
     `buildPlan` into a **segment trie** (once per column, reused every row). The
     walker descends the trie by key as it descends the document, so it **never
     builds full `"$.contacts[3].email"` strings per leaf** (the accidental-
     quadratic-allocation trap).
  3. **Reuse scratch buffers** across rows (the pipeline is single-goroutine per
     table).
  The internals (currently a tree-walk: decode → trie-guided walk → encode) sit
  behind the unchanged `Transformer` interface, so a streaming token rewriter can
  replace them later *if profiling on real blob sizes demands it*, without
  touching config, the trie, or the leaf transforms.
- **Safety floor.** With *zero* rules (`transform: json_anonymise` alone) the
  output is already safe: nulls stay null, empty strings stay empty, non-empty
  strings are hashed, numbers become `0`, booleans are kept. It is safe but not
  necessarily *usable* — e.g. an `email` leaf becomes a plain hash with no `@`,
  which will fail DTO validation; email fields must be named with `hash_email`.
- **Reconstruction.** The anonymised document is still valid JSON stored
  losslessly as `BYTE_ARRAY(STRING)` per [ADR-0003](./0003-hybrid-type-mapping-with-lossless-fallback.md);
  the anonymised bytes *are* the value, so reconstruction needs no special
  handling. The Manifest records the transform name on the column.
- **`validate`** can confirm the column is a `json` type and that every `paths`
  leaf transform builds (e.g. `hash` needs `hashing.key`), but it **cannot**
  validate that a path exists — JSON structure is not in the schema.
