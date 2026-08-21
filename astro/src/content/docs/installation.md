---
title: Installation
description: Install Digestive via Homebrew, the install script, or from source.
---

Digestive ships as a single self-contained binary. Install a prebuilt release
with Homebrew or the install script, or build from source. Then run `init` to
scaffold your configuration.

## Install a prebuilt binary

**Homebrew (macOS):**

```sh
brew install social-sync/tap/digestive
```

**Install script (macOS / Linux):**

```sh
curl -sSfL https://raw.githubusercontent.com/social-sync/digestive/main/install.sh | sh
```

The script detects your OS and architecture, downloads the matching release
asset, verifies its SHA-256 checksum, and installs `digestive` to
`/usr/local/bin` (falling back to `~/.local/bin`). Set `DIGESTIVE_BIN_DIR` to
choose the target directory, or `DIGESTIVE_VERSION=v1.2.3` to pin a version.

**Manual:** grab an archive for your platform from the
[releases page](https://github.com/social-sync/digestive/releases), extract it,
and put the `digestive` binary on your `PATH`.

Once installed, skip ahead to [Getting started](/getting-started/).

## Build from source

Prefer building yourself? Read on.

### Prerequisites

- **Go 1.26 or newer** (the module targets Go 1.26). Check with `go version`.
- Network access to your source database (SingleStore, or any MySQL-wire-compatible database).

No C toolchain is required — the build is pure Go (`CGO_ENABLED=0`), so it
produces one self-contained binary with no runtime dependencies.

### Clone and build

```sh
git clone https://github.com/social-sync/digestive.git
cd digestive

# Build the single static binary into ./digestive
make build
```

`make build` runs `CGO_ENABLED=0 go build -o digestive .`. If you prefer, run
that directly, or install it onto your `PATH`:

```sh
go install github.com/social-sync/digestive@latest
```

Verify it works:

```sh
./digestive --help
```
