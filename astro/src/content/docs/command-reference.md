---
title: Command reference
description: Every digestive command and its flags at a glance.
---

A one-page index of every `digestive` command and the flags each accepts. For
task-oriented walkthroughs, follow the links to the dedicated pages.

## Commands

| Command | Description |
| ------- | ----------- |
| [`init`](#init) | Create starter `.env` and `config.yaml` in the current directory. |
| [`validate`](#validate) | Check the config against the live schema without exporting. |
| [`export`](#export) | Export configured tables to Parquet. |
| [`restore`](#restore) | Turn an export run into a SQL script of `INSERT`s. |
| [`sync`](#sync) | Export and apply straight into a destination database. |
| [`report`](#report) | Print an anonymisation report of the configured tables. |

## Global flags

These persistent flags apply to every command.

| Flag | Value | Default | Description |
| ---- | ----- | ------- | ----------- |
| `--config`, `-c` | string | `config.yaml` | Path to the YAML config file. |
| `--log-level` | string | `info` | Log level: `debug`, `info`, `warn`, `error`. |
| `--json` | — | `false` | Emit a single JSON result on stdout and disable the TUI (quiet unless `--log-level` is raised). See [JSON output](/comprehensive-docs/#json-output). |
| `--help`, `-h` | — | — | Show help for the command. |
| `--version`, `-v` | — | — | Print the version (root command only). |

## `init`

```sh
digestive init
```

Create a starter `.env` and `config.yaml` in the current directory. Takes no
arguments and has no flags of its own. See [Installation](/comprehensive-docs/#installation).

## `validate`

```sh
digestive validate
```

Check the config against the live schema without exporting anything. Takes no
arguments and has no flags of its own beyond the [global flags](#global-flags).

## `export`

```sh
digestive export
```

Export configured tables to Parquet.

| Flag | Value | Default | Description |
| ---- | ----- | ------- | ----------- |
| `--run-name` | string | timestamp | Run directory name. |
| `--delete-on-failure` | — | `false` | Remove the run directory if the export fails. |
| `--no-tui` | — | `false` | Disable the live progress UI and log plainly instead. |

Also accepts the [compliance flags](#compliance-flags).

## `restore`

```sh
digestive restore <run-dir> --dialect singlestore|mysql
```

Turn an export run into a SQL script of `INSERT`s. Takes exactly one argument:
the run directory to restore. See [Restore](/comprehensive-docs/#restore).

| Flag | Value | Default | Description |
| ---- | ----- | ------- | ----------- |
| `--dialect` | string | — | **Required.** Target SQL engine: `singlestore` or `mysql`. |
| `--batch-size` | int | `1000` | Rows per multi-row `INSERT` statement. |
| `--allow-incomplete` | — | `false` | Restore even if the manifest reports an incomplete export. |
| `--ignore-restore-conf` | — | `false` | Ignore a `restore.yaml` in the working directory. |

## `sync`

```sh
digestive sync [run-dir]
```

Export and apply straight into a destination database. Takes an optional
argument: an existing run directory to apply instead of exporting a fresh one.
See [Sync](/comprehensive-docs/#sync).

| Flag | Value | Default | Description |
| ---- | ----- | ------- | ----------- |
| `--yes` | — | `false` | Skip the confirmation prompt. Required with `--json`. |
| `--cleanup` | — | `false` | Delete the run directory after a successful apply (ignored when a run directory is given). |
| `--dialect` | string | — | Override the restore dialect from `sync.type` (`singlestore` or `mysql`). |
| `--batch-size` | int | `1000` | Rows per multi-row `INSERT` statement. Overrides `sync.batch_size`. |
| `--max-packet-bytes` | int | — | Max bytes per statement batch sent to the destination. A large table's `INSERT`s are split into chunks no larger than this, so it never trips the destination's `max_allowed_packet` (`packet for query is too large`). `0` uses `sync.max_packet_bytes` or the built-in 4 MiB default. Overrides `sync.max_packet_bytes`. |
| `--allow-incomplete` | — | `false` | Apply even if the manifest reports an incomplete export. |
| `--ignore-restore-conf` | — | `false` | Ignore a `restore.yaml` in the working directory. |
| `--no-tui` | — | `false` | Disable the live progress UI and log plainly instead. |

Also accepts the [compliance flags](#compliance-flags).

## `report`

```sh
digestive report --format html|markdown
```

Connect to the source and print an anonymisation report of every whitelisted
table to stdout. For each column it shows whether it is anonymised, and — when
it is — which transform is applied:

- **No** — the column is exported untouched (highlighted in the HTML output so
  potential leaks stand out).
- **Yes** — a transform is applied; the Method cell summarises it, e.g.
  `hash (length 16)`, `mask (keep first 2, last 2)`, `json_anonymise (3 paths)`.
- **Excluded** — the column is dropped from the export entirely.

The config is validated against the live schema first (the same check as
[`validate`](#validate)), so a stale or invalid config fails rather than
producing a misleading report. The document is written straight to stdout;
`--json` has no effect on this command.

| Flag | Value | Default | Description |
| ---- | ----- | ------- | ----------- |
| `--format` | string | `html` | Output format: `html` (a self-contained, styled document) or `markdown` (GitHub-flavored pipe tables). |

## Compliance flags

`export` and `sync` share these flags. They only take effect when a
`compliance:` block is present in the config. See [Compliance](/comprehensive-docs/#compliance).

| Flag | Value | Default | Description |
| ---- | ----- | ------- | ----------- |
| `--requester-name` | string | `""` | Name of the person requesting the export (required when compliance is configured). |
| `--requester-email` | string | `""` | Email of the person requesting the export (required when compliance is configured). |
| `--cleanup-on-audit-fail` | — | `false` | Delete the exported run directory if the audit record cannot be written. |
