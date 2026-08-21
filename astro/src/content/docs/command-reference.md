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

## Compliance flags

`export` and `sync` share these flags. They only take effect when a
`compliance:` block is present in the config. See [Compliance](/comprehensive-docs/#compliance).

| Flag | Value | Default | Description |
| ---- | ----- | ------- | ----------- |
| `--requester-name` | string | `""` | Name of the person requesting the export (required when compliance is configured). |
| `--requester-email` | string | `""` | Email of the person requesting the export (required when compliance is configured). |
| `--cleanup-on-audit-fail` | — | `false` | Delete the exported run directory if the audit record cannot be written. |
