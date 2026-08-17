---
status: accepted
---

# Restore-time schema reconciliation via `restore.yaml` rules

## Context and decision

An export run is an immutable production snapshot; `restore` turns it into a
single SQL script of `INSERT`s (ADR-0005). That command assumes "the target is a
pre-existing copy of the source database whose schema already exists." In
practice the target often *drifts*: you pull a production export to work on
locally, but your local migrations have already renamed a column, added a
non-null column with no default, or dropped one. The `INSERT`s then fail at the
database.

`restore` grows an optional **restore-rules** file, `restore.yaml`, that
reconciles the export's column set with the drifted target so the load succeeds.
It is **declarative schema-shape reconciliation** — it reshapes the emitted
column list and values, and does not transform values' meaning (anonymisation,
hashing, and masking stay on the export side, keyed and typed by the source).

Several decisions are load-bearing.

### Declarative only — no connection to the target

`restore` still connects to nothing. The operator *states* the drift in
`restore.yaml`; the tool never reads the target's `information_schema` and so
**cannot detect or verify** drift. A rule that contradicts the target (rather
than the manifest) surfaces exactly as it would today: a database error at load
time. This preserves the property that `restore` runs on a dev box with no
database reachable, and matches how the export side already treats config as
trusted operator declaration rather than something checked against a live schema
(the export does no drift detection either — see CONTEXT.md). Auto-diffing a
live target is a genuinely useful future capability, but it is a much larger
surface (driver, credentials, per-engine `information_schema` quirks) and would
be its own ADR; the declarative rules are the primitive it would build on.

### Relaxes ADR-0005's "reads only the run directory"

ADR-0005 made a point of `restore` reading *only* the run directory. This ADR
**consciously relaxes** that: a `restore.yaml` in the working directory is an
additional input. The relaxation is deliberate and bounded:

- The file lives with the **local application and its migrations** (the working
  directory), not inside the immutable export artifact — the drift knowledge
  belongs where the drift was caused, and version-controls alongside the
  migrations.
- It is **auto-discovered** from the working directory. Because that means the
  same `restore <run-dir> --dialect …` command produces different SQL depending
  on where it runs, `restore` **announces on stderr** whenever it applies a
  `restore.yaml`, and `--ignore-restore-conf` visibly opts out. The
  reconciliation is never invisible.

### Five operations, no type-change

The rules cover five row-shaping operations, all of which reduce to rewriting
the explicit column list and per-row values `restore` already emits:

- **rename column** — emit the target name instead of the manifest name.
- **drop column** — omit the column (name and every row's value).
- **add column** — append a column with a constant value on every row, for a
  target column the export predates (the load-bearing case: **non-null, no
  default**).
- **rename table** — emit `INSERT INTO <new>`.
- **drop table** — skip the table's `INSERT`s entirely.

**Type-change** (re-rendering a value into a different column type) and
**add-table** (a wholly new table has no source data) are out of scope.
Type-change in particular would need genuine value re-coercion; and because
lossless types are already emitted as quoted string literals the engine coerces
(ADR-0005), many widenings already round-trip without any rule. It deserves its
own decision later.

### Added-column values: quoted literal, explicit null, or raw expression

An added column is the one operation that emits a value the manifest knows
nothing about, so it needs a rendering rule:

- A **scalar** renders as a **quoted string literal** the engine coerces into
  the real column type — the same lossless philosophy ADR-0005 uses for
  decimals, dates, and `bigint unsigned`.
- **`value: null`** renders as the unquoted `NULL` keyword (for a nullable added
  column stated explicitly; a nullable column usually needs no rule at all,
  since omitting it lets the database apply its default).
- **`raw: true`** splices the value verbatim as a SQL expression (`NOW()`,
  `UUID()`), for the cases a constant cannot express. Raw values are **trusted
  config**, the same trust model as the export `where` fragment (CONTEXT.md).

Added columns are emitted in **sorted order** so the same `restore.yaml` always
produces byte-identical SQL (a Go map's iteration order is otherwise random).

### Fail fast on any rule that contradicts the manifest

The only thing `restore` can check rules against is the manifest (the export's
own column set). Every rule must apply to something and must not create a
collision; a rule that matches nothing is a typo or a stale rule, and a silent
no-op there would fail cryptically at the database later — defeating the point.
So all of the following are **hard errors**, validated before any SQL is
emitted:

- a rename-source / drop / rename-table / drop-table targeting a column or table
  **absent from the manifest**;
- an `add_columns` name that **already exists** in the manifest and is not being
  renamed or dropped away (it is not an *add*);
- a rename **target**, or an added column, that **collides** with another
  emitted column (two columns, one name);
- a column named in both `rename_columns` (as source) and `drop_columns`;
- `drop_table` combined with any other rule for the same table (the rest is
  meaningless);
- two source tables **emitting into the same target name**;
- a table left with **no columns** after drops.

## Considered options

- **Connect to the target and auto-reconcile:** rejected for v1 (see above) —
  breaks the connection-free property and is a much larger surface. The
  declarative rules are the primitive it would later build on.
- **Explicit `--rules <path>` flag instead of auto-discovery:** rejected — the
  rules track with the local repo, so discovering `./restore.yaml` and
  announcing it on stderr is less friction than re-typing a path every run.
  `--ignore-restore-conf` covers the opt-out.
- **Rules inside `config.yaml`:** rejected — `config.yaml` describes the export
  (source side, secrets, transforms) and lives on a different machine; restore
  rules describe *local* drift and belong with the local migrations.
- **Value transforms at restore time (hash/mask on load):** rejected — the key
  and the source types live on the export side; duplicating the transform
  registry on the read side would muddy the "restore reads a self-describing
  artifact" story. Reconciliation is schema-shape only.
- **`${VAR}` expansion in `restore.yaml`:** not implemented — the rules are
  structural and hold no secrets.

## Consequences

- `restore` remains connection-free and, absent a `restore.yaml`, byte-for-byte
  identical to before.
- Reconciliation is announced on stderr and disabled with
  `--ignore-restore-conf`, so the auto-discovered file never silently changes
  output.
- The rules rewrite the `(column-list, per-row-values)` the emitter already
  builds; the `--dialect` preamble is untouched, and no new value-rendering
  machinery is introduced. Type-change, add-table, and target-schema
  auto-diffing remain deferred, and the file and command are shaped so they can
  be added without re-exporting.
