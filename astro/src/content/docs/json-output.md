---
title: JSON output
description: Machine-readable output for every command via the --json flag.
---

Every command accepts a persistent `--json` flag for machine consumers — a web
app shelling out to the binary, CI, or a script that needs to read back what
happened rather than parse human text.

Under `--json`:

- **stdout carries exactly one JSON object** — pretty-printed, with a trailing
  newline, and nothing else. The live TUI is disabled.
- It applies to **success and failure alike**. A failing run still prints a JSON
  object (with `"status": "error"`) and the process still exits non-zero, so
  `&&` chains and CI keep working. A consumer never has to scrape stderr.
- It implies **quiet**: diagnostic logging on stderr is suppressed unless you
  raise it explicitly with `--log-level`. stdout stays pure JSON regardless.

## The envelope

Every command emits the same top-level shape, so a consumer has one parse path.
The per-command payload lives in `result`:

```json
{
  "schema_version": 1,
  "command": "export",
  "status": "ok",
  "error": null,
  "warnings": [],
  "result": {
    "run_dir": "exports/2026-08-14T15-04-05Z",
    "run_id": "2026-08-14T15-04-05Z",
    "tables": [
      { "name": "users", "rows": 48210 },
      { "name": "orders", "rows": 195003 }
    ],
    "total_rows": 243213
  }
}
```

| Field            | Meaning                                                            |
| ---------------- | ----------------------------------------------------------------- |
| `schema_version` | Envelope version; bumps only on a breaking shape change.          |
| `command`        | Which subcommand ran.                                             |
| `status`         | `"ok"` or `"error"`; mirrors the exit code.                       |
| `error`          | `null` on success, a string on failure.                          |
| `warnings`       | Always an array (empty when there are none).                      |
| `result`         | Command-specific payload; `null` on error.                        |

## Per-command `result`

### `export`

`run_dir`, `run_id`, per-table `{name, rows}`, and `total_rows` — drawn from the
manifest the run just wrote, so the payload never diverges from the artifact on
disk.

### `sync`

The same table/row summary as `export`, plus:

```json
"result": {
  "run_dir": "exports/2026-08-14T15-04-05Z",
  "run_id": "2026-08-14T15-04-05Z",
  "tables": [{ "name": "users", "rows": 48210 }],
  "total_rows": 48210,
  "applied": true,
  "destination": { "type": "mysql", "host": "db.internal", "database": "app" }
}
```

`run_dir` is `null` when `--cleanup` removed it after a successful apply.

Because `sync` writes to a live database, under `--json` it **requires `--yes`**:
a machine consumer cannot answer the interactive confirmation prompt, so the
flag must be passed explicitly to acknowledge the write. Without it, `sync`
returns a JSON error (`"--yes is required with --json"`) and does nothing.

### `restore`

`restore --json` emits a **summary**, not SQL:

```json
"result": {
  "run_dir": "exports/2026-08-14T15-04-05Z",
  "dialect": "mysql",
  "tables": [{ "name": "users", "rows": 48210, "statements": 49 }],
  "total_statements": 49
}
```

No SQL is written to stdout under `--json`. When you want the script itself, use
plain [`restore`](/restore/) and capture stdout; to apply the data directly, use
[`sync`](/sync/).

### `validate`

`tables` (the names that validated, in config order) and `table_count`.

### `init`

`created` — the files written (`.env`, `config.yaml`). The freshly generated
hashing key is **never** included in the output; it lives only in `.env`.

## The invocation boundary

The JSON contract covers **execution-time outcomes**. A malformed *invocation*
that `cobra` rejects before the command runs — an unknown flag or command, a
missing required argument like `restore`'s `--dialect` — still errors
conventionally on stderr. A consumer should treat "stdout did not parse as JSON"
as a failed invocation and read stderr for the reason.
