---
status: accepted
---

# `sync`: export then apply straight into a destination database

## Context and decision

`restore` turns an export run into a SQL script of `INSERT`s on stdout, for a
human to pipe into a client (ADR-0005). The common end-to-end need — "refresh my
local/staging database from a production export" — then takes three manual
steps: `export`, `restore > dump.sql`, `mysql < dump.sql`. `digestive sync`
collapses that into one command: it exports the configured tables and applies
the same INSERTs **directly** into a destination database.

The destination is declared in `config.yaml`:

```yaml
sync:
  dsn: ${SYNC_DSN}
  type: mysql        # mysql | singlestore
```

Several decisions are load-bearing.

### Apply over the Go SQL driver, not a piped external client

The obvious framing is "stream `restore` into a `mysql` process." We instead
open the destination with the **Go SQL driver** (`sql.Open`, the same driver the
export side already uses to read the source) and execute the restore's
statements ourselves. This:

- needs no `mysql` (or `psql`) binary installed on the box, and no shell/arg
  escaping of a subprocess;
- gives **real per-statement errors** and a Go-managed transaction, rather than
  only a process exit code;
- reuses the DSN concept already in config — `sync.dsn` is a driver DSN, not a
  client's flags.

Piping into an external client is genuinely useful for an engine we have no Go
driver for, so it is kept as a **future escape hatch** (a `--pipe-to`-style
flag), not built now.

### `type` selects driver **and** dialect; it is the cross-engine seam

`sync.type` resolves to a `{driver, restore.Dialect}` pair in one place
(`internal/target`). Today both supported types are MySQL-wire, so they share
the `mysql` driver and differ only in dialect (the `restore.Dialect` preamble).
A future Postgres would register its own driver and dialect here without
touching the command. `--dialect` can override the dialect derived from `type`.

### Single transaction, all-or-nothing

The whole apply runs inside one Go-managed transaction: session `SET`s, then
each table's INSERTs, then `COMMIT`. Any error rolls the transaction back, so
the destination ends up either fully synced or untouched — never half-populated.
This is why `sync` manages the transaction itself rather than executing
`restore`'s self-contained `START TRANSACTION … COMMIT` script: a Go-driven
`BeginTx`/`Rollback`/`Commit` guarantees the connection can't be returned to the
pool mid-transaction, and surfaces the failing statement as a Go error.

We accept the tradeoff that a very large sync holds one large transaction
(destination memory/lock pressure); if that ever bites, per-table or batched
commits behind a flag are a compatible follow-up. Tables are applied one Exec at
a time (each table's SQL is buffered, not the whole database), so process memory
is bounded by the largest single table, and per-table progress is reportable.

### Reuses `restore` verbatim — same SQL, same reconciliation

`sync` does not re-implement value rendering. It calls a shared
`restore.Prepare` / `Prepared.WriteTable` seam, so the SQL applied is
byte-for-byte what a piped `restore` would emit (minus the script's own
transaction control, which `sync` manages). A `restore.yaml` in the working
directory is discovered and applied **identically**, with the same
`--ignore-restore-conf` opt-out — anything `restore` does, `sync` does, differing
only in the sink (driver `Exec` vs stdout).

### Schema is a precondition — no DDL

Like `restore`, `sync` emits `INSERT`s only and **creates no tables**. The
destination schema must already exist (run your migrations first). A missing
table surfaces as a database error and rolls the sync back. DDL generation would
require cross-engine type translation — the same hard problem ADR-0006 deferred
for type-change — and is out of scope here.

### Confirmation guard on the one command that writes to a live DB

Every other command is read-only or writes local files; `sync` is the first to
mutate a remote database, so a mistyped DSN could hit the wrong server. When
stderr is a terminal, `sync` prints the resolved destination (host + database,
never the password) and asks before applying. The prompt is skipped
automatically when non-interactive (CI, piped) or with `--yes`, so automation is
never blocked. The destination is opened and validated (and the guard shown)
**before** any export work, so a bad target fails fast.

### Optional existing-run argument; keep the artifact

`digestive sync` with no argument exports fresh, then applies. Given a run
directory (`digestive sync ./exports/…`) it **skips the export** and applies that
run — a cheap retry path after a failed apply, without re-querying the source.
The run directory is kept by default (a reusable, inspectable artifact);
`--cleanup` deletes it after a successful apply, and only when `sync` created it
this run — a run directory passed in is never `sync`'s to delete, and a failed
apply always keeps it.

## Considered options

- **Pipe into an external `mysql`/`psql` process:** rejected for v1 — requires
  the client binary, loses per-statement errors, and adds subprocess/escaping
  surface the codebase otherwise avoids. Retained as a future `--pipe-to`
  escape hatch for driver-less engines.
- **Execute `restore`'s self-contained transaction script in one `Exec`:**
  rejected — a mid-script failure would leave the pooled connection inside an
  open transaction, and only a monolithic error would be reported. A
  Go-managed transaction is safer and gives per-table errors and progress.
- **A new top-level `sync:` block vs. nesting under `destination`:** chose a new
  block. `destination` already means "the local Parquet directory"; overloading
  it muddies that. A separate block is additive and leaves existing `export`
  configs untouched.
- **Generate DDL / create tables at the destination:** deferred — needs
  cross-engine type translation and is its own decision (cf. ADR-0006's
  type-change deferral). `sync` assumes the schema exists, exactly like
  `restore`.
- **Validate the `sync` block in `config.Load`:** rejected — `Load` is shared by
  `export`/`validate`/`restore`, which have no destination. `sync` validates its
  own block at the start of the command instead, still before any export work.

## Consequences

- A one-command refresh: `digestive sync` exports and loads a destination with no
  intermediate files to manage (or `--cleanup` to discard the run dir).
- The destination is reached over the same driver the source uses; `type` is the
  single seam where a non-MySQL engine slots in.
- The apply is atomic (single transaction) and connection-driven, so failures
  are reported per statement and leave the destination untouched.
- `restore` stays the source of truth for SQL generation and reconciliation;
  `sync` adds a sink, not a second renderer.
- DDL creation, an external-client pipe, and Postgres remain deferred, and the
  command and config are shaped so they can be added without breaking changes.
