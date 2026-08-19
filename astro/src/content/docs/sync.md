---
title: Sync
description: Export and apply straight into a destination database in one command.
---

`sync` runs the whole pipeline end to end: it **exports** the configured tables,
generates the same `INSERT`s [`restore`](/restore/) would, and **applies them
directly into a destination database** over the Go SQL driver — no `mysql`
client, no intermediate `.sql` file, no piping.

```sh
# Export fresh, then apply into the destination from your config:
./digestive sync

# Skip the export and apply an existing run directory (retry a failed apply):
./digestive sync ./exports/2026-08-14T15-04-05Z
```

The destination lives in your [config](/configuration/#sync) under a `sync`
block — a DSN and a type:

```yaml
sync:
  dsn: ${SYNC_DSN}   # go-sql-driver/mysql DSN for the destination
  type: mysql        # mysql or singlestore
```

## What `sync` does

1. **Resolves and opens the destination** from `sync.dsn` / `sync.type`, and
   verifies it's reachable — so a missing config block or an unreachable target
   fails **before** any export work happens.
2. **Confirms** (when attached to a terminal) that you mean to write to that
   database — see [The confirmation guard](#the-confirmation-guard).
3. **Exports** the configured tables to a run directory — unless you passed one,
   in which case it uses that and skips the export.
4. **Applies** the run's `INSERT`s into the destination inside a **single
   transaction**.

Because step 4 reuses `restore`, the data applied is **byte-for-byte** what a
piped `restore` would load, and a `restore.yaml` in your working directory is
honoured identically. Anything `restore` does, `sync` does — the only difference
is the sink: a live database connection instead of stdout.

## Applied in a single transaction

The whole apply runs in one transaction: session setup, every table's `INSERT`s,
then `COMMIT`. **Any error rolls the whole thing back**, so the destination ends
up either fully synced or completely untouched — never half-populated.

:::note[The destination schema must already exist]
Like `restore`, `sync` emits **`INSERT`s only — it never creates tables**. Point
it at a database whose schema already exists (run your migrations first). A
missing table surfaces as a database error and rolls the sync back. If your local
schema has drifted from the export, reconcile it with a
[`restore.yaml`](/restore/#reconciling-schema-drift), exactly as you would for
`restore`.
:::

## The confirmation guard

`sync` is the one command that writes to a **live database**, so a mistyped DSN
could hit the wrong server. When stderr is a terminal it prints the resolved
destination — host and database name, never the password — and asks before
applying:

```text
About to sync into mysql database "app_staging" on db.internal:3306.
This INSERTs into existing tables in a single transaction. Continue? [y/N]
```

The prompt is **skipped automatically** when the run is non-interactive (CI, or
stderr redirected) or when you pass `--yes`, so automation is never blocked.

## Export fresh, or apply an existing run

- **`digestive sync`** (no argument) exports fresh, then applies. This needs
  `source.dsn` in your config, just like `export`.
- **`digestive sync <run-dir>`** skips the export and applies an existing run
  directory. It needs no source connection — handy for **retrying a failed
  apply** (fix the destination, re-run) without re-querying the source.

The run directory is **kept by default** — a reusable, inspectable artifact you
can re-apply or hand to `restore`. Pass `--cleanup` to delete it after a
successful apply. Cleanup only ever removes a directory `sync` created this run:
a run directory you passed in is never deleted, and a **failed apply always keeps
the directory** for inspection and retry.

## `type` selects the driver and dialect

`sync.type` resolves to both the SQL driver and the [restore
dialect](/restore/#the---dialect-flag-is-required):

| `type` | Driver | Dialect | Preamble |
| --- | --- | --- | --- |
| `mysql` | go-sql-driver/mysql | `mysql` | disables foreign-key & unique checks |
| `singlestore` | go-sql-driver/mysql | `singlestore` | charset only |

Both supported engines speak the MySQL wire protocol today, so they share one
driver. `type` is the seam a future engine (e.g. Postgres) would slot into with
its own driver and dialect. Pass `--dialect` to override the dialect `type`
implies.

## Flags

| Flag | Default | Description |
| --- | --- | --- |
| `--yes` | `false` | Skip the confirmation prompt. |
| `--cleanup` | `false` | Delete the run directory after a successful apply. Ignored when you pass an existing run directory. |
| `--dialect` | from `type` | Override the restore dialect: `singlestore` or `mysql`. |
| `--batch-size` | `1000` | Rows per multi-row `INSERT`. |
| `--allow-incomplete` | `false` | Apply even if the manifest reports an incomplete export. |
| `--ignore-restore-conf` | `false` | Ignore a `restore.yaml` in the working directory. |
| `--no-tui` | `false` | Disable the live progress UI and log plainly instead. |
| `--requester-name` | — | Name of the requester. Required when [compliance](/compliance/) is configured. |
| `--requester-email` | — | Email of the requester. Required when compliance is configured; must be a valid address. |
| `--cleanup-on-audit-fail` | `false` | Delete the run directory if the [audit record](/compliance/) can't be written. |

Plus the [global flags](/configuration/#global-flags) `--config` and
`--log-level`.

When a [`compliance:`](/compliance/) block is configured, `sync` requires a
requester and writes an audit record **after the export but before applying** to
the destination — so data never lands without a trail. See
[Compliance](/compliance/).

## What's out of scope

- **Creating the destination schema (DDL).** `sync` inserts data; it does not
  create tables. Cross-engine type translation makes DDL its own, larger
  feature.
- **Piping into an external client.** `sync` uses the Go driver directly. An
  external-client escape hatch (for engines without a Go driver) is a possible
  future addition; `type` is where it would hook in.
- **Postgres.** Designed for — `type` is the seam — but not built yet.
