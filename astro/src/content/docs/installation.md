---
title: Installation
description: Install Grimnir via Homebrew, the install script, or from source, and scaffold a config.
---

Grimnir ships as a single self-contained binary. Install a prebuilt release
with Homebrew or the install script, or build from source. Then run `init` to
scaffold your configuration.

## Install a prebuilt binary

**Homebrew (macOS):**

```sh
brew install social-sync/tap/grimnir
```

**Install script (macOS / Linux):**

```sh
curl -sSfL https://raw.githubusercontent.com/social-sync/grimnir/main/install.sh | sh
```

The script detects your OS and architecture, downloads the matching release
asset, verifies its SHA-256 checksum, and installs `grimnir` to
`/usr/local/bin` (falling back to `~/.local/bin`). Set `GRIMNIR_BIN_DIR` to
choose the target directory, or `GRIMNIR_VERSION=v1.2.3` to pin a version.

**Manual:** grab an archive for your platform from the
[releases page](https://github.com/social-sync/grimnir/releases), extract it,
and put the `grimnir` binary on your `PATH`.

Once installed, skip ahead to [Scaffold a config](#scaffold-a-config).

## Build from source

Prefer building yourself? Read on.

### Prerequisites

- **Go 1.26 or newer** (the module targets Go 1.26). Check with `go version`.
- Network access to your source database (SingleStore, or any MySQL-wire-compatible database).

No C toolchain is required — the build is pure Go (`CGO_ENABLED=0`), so it
produces one self-contained binary with no runtime dependencies.

### Clone and build

```sh
git clone https://github.com/social-sync/grimnir.git
cd grimnir

# Build the single static binary into ./grimnir
make build
```

`make build` runs `CGO_ENABLED=0 go build -o grimnir .`. If you prefer, run
that directly, or install it onto your `PATH`:

```sh
go install github.com/social-sync/grimnir@latest
```

Verify it works:

```sh
./grimnir --help
```

## Scaffold a config

From the directory you want to work in, run `init`. It writes two files:

```sh
./grimnir init
```

- **`config.yaml`** — a starter configuration you edit to describe your export.
- **`.env`** — holds your secrets, created with a freshly generated random
  hashing key (`EXPORT_HASH_KEY`) and a placeholder database DSN. It is written
  with `0600` permissions because it contains secrets.

:::caution[init never overwrites]
If either `config.yaml` or `.env` already exists, `init` fails and writes
nothing — so re-running it can't clobber your config or regenerate (and thereby
invalidate) your hashing key. Delete the files yourself if you truly want to
start over.
:::

## Point it at your database

Edit `.env` and set your connection string. The DSN uses the
[go-sql-driver/mysql](https://github.com/go-sql-driver/mysql#dsn-data-source-name)
format; SingleStore is MySQL wire compatible, so the same format applies:

```ini
# .env
SINGLESTORE_DSN=root:password@tcp(127.0.0.1:3306)/mydb
EXPORT_HASH_KEY=<generated for you by init — keep it stable>
```

:::note[Keep the key stable]
`EXPORT_HASH_KEY` is the secret that keys deterministic hashing. If it changes
between runs, every hashed value changes too, and previously-exported data will
no longer join. Keep it constant (and out of source control).
:::

## Validate, then export

Describe the tables you want in `config.yaml` (see
[Configuration](/configuration/)), then:

```sh
# Check the config against the live schema without exporting anything.
./grimnir validate

# Run the export.
./grimnir export
```

A successful export prints the run directory it produced, e.g.
`./exports/20260814T150405Z/`, containing one Parquet file per table plus a
`manifest.json`.

To turn that run back into loadable SQL, see [Restore](/restore/):

```sh
./grimnir restore ./exports/20260814T150405Z --dialect singlestore > dump.sql
```
