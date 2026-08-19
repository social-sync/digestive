---
title: Compliance
description: Write a per-run audit record for every export and sync, to S3-compatible storage or a local directory.
---

For regulated data, you often need to answer — after the fact — **who** pulled an
export, **when**, under **what config**, and **how much** data left each table.
Digestive's compliance mode writes a single **audit record** (a JSON document)
for every [`export`](/configuration/#export-flags) and [`sync`](/sync/), to
either an **S3-compatible bucket** or a **local directory**.

The feature is **opt-in and gated by config**. It is off unless a `compliance:`
block is present — and when it is present, it is **mandatory**: you can't run an
export or sync without recording who requested it.

## Turning it on

Add a `compliance:` block to your `config.yaml`. Set **exactly one** of
`directory` (local) or `s3` (S3-compatible):

```yaml
compliance:
  audit:
    # Local directory:
    directory: ./audit-logs

    # …or an S3-compatible bucket (set one, not both):
    s3:
      endpoint: ${AUDIT_S3_ENDPOINT}       # host[:port], no scheme
      bucket: ${AUDIT_S3_BUCKET}
      prefix: exports/                     # optional key prefix
      region: ${AUDIT_S3_REGION:-us-east-1}
      access_key_id: ${AUDIT_S3_ACCESS_KEY}
      secret_access_key: ${AUDIT_S3_SECRET_KEY}
      use_ssl: true
      path_style: true                     # MinIO / Ceph / custom domains
```

Once this block is present, `export` and `sync` **require** a requester:

```sh
./digestive export \
  --requester-name  "Jane Auditor" \
  --requester-email "jane@example.com"
```

Both flags are validated **before any work starts** — the name must be non-empty
and the email must parse as an address. Omit either and the command fails
immediately.

:::note[Present means on; broken means error]
The mere presence of `compliance:` switches auditing on — there is no separate
"enable" flag to forget. A present-but-malformed block (neither or both of
`directory`/`s3`, or an `s3` block missing a required field) is a **hard error at
load time**, never a silent no-op. "On but broken" fails loudly.
:::

## What's in the record

One JSON document is written per run. All actions share the same schema; the
`action` field (`export`, `sync`, …) distinguishes them.

```json
{
  "audit_version": 1,
  "action": "export",
  "requester": { "name": "Jane Auditor", "email": "jane@example.com" },
  "hostname": "worker-03.internal",
  "timestamps": {
    "export_started_at": "2026-08-19T14:30:00Z",
    "audit_written_at":  "2026-08-19T14:31:12Z"
  },
  "output": {
    "run_name": "2026-08-19T14-30-00Z",
    "run_directory": "/data/exports/2026-08-19T14-30-00Z"
  },
  "config":   { "…": "the effective config, secrets redacted" },
  "manifest": { "…": "the full manifest.json, embedded inline" },
  "row_counts": { "users": 10432, "orders": 88123 },
  "tool_version": "1.4.2"
}
```

| Field | What it captures |
| --- | --- |
| `action` | `export` or `sync` (one record per command run). |
| `requester` | The name and email from the required flags. |
| `hostname` | The machine that produced the export. |
| `timestamps` | When the export started (`manifest.created_at`) and when the record was written, both RFC3339 UTC. |
| `output` | The run name and directory the export produced. |
| `config` | The **resolved, effective** config that governed the run — with secrets redacted (see below). |
| `manifest` | The full [`manifest.json`](/configuration/#output-layout) embedded inline. |
| `row_counts` | A flat `table → rows` map. |
| `tool_version` | The `digestive` build that ran. |

### Secrets are redacted

The config is the compliance-relevant part of the record — which tables, columns,
and transforms governed what left the database — but it also holds secrets.
Digestive **redacts** them before embedding, replacing each with
`***REDACTED***`:

- `source.dsn` and `sync.dsn` (the whole DSN),
- `hashing.key`,
- the S3 `access_key_id` and `secret_access_key`.

Everything else — table/column config, transforms, the destination directory,
`sync.type` — is preserved. The audit record never contains a live credential.

## When the record is written

The record needs the completed manifest and row counts, so it is written **only
on success**, right after the export finishes.

- **`export`** writes the record after the export completes, then prints the run
  directory as usual.
- **`sync`** writes the record after the export succeeds but **before applying**
  to the destination — so data can never land in the target without an audit
  trail having been written first. A sync that reuses an existing run directory
  still records a `sync` entry.

### If the audit write fails

If the record can't be written (bucket unreachable, permission denied, disk
full), the command **fails with a non-zero exit**. A silently-missing audit trail
is the worst outcome for a compliance control, so this is deliberate — a
momentary outage turns a good export into a failed command. The exported files
still exist on disk; you're simply told the record didn't land.

To go further and **enforce** "no export without an audit," pass
`--cleanup-on-audit-fail`: on an audit-write failure, the run directory is
deleted, so a completed export is never left behind without its trail.

```sh
./digestive export \
  --requester-name "Jane" --requester-email "jane@example.com" \
  --cleanup-on-audit-fail
```

This composes with `--delete-on-failure`, which handles *export* failure;
`--cleanup-on-audit-fail` handles *audit* failure.

## The audit object name

Each record is written as `<run-name>-<hostname>-<random>.json`. The hostname
attributes origin and the random suffix guarantees uniqueness, so many people or
machines can write into **one shared bucket or directory** without colliding —
even when two runs share a timestamp-based run name. (For S3, the configured
`prefix` is prepended to the key.)

## S3-compatible storage

The S3 sink works against any S3-compatible service — MinIO, Cloudflare R2, Ceph,
Wasabi, and AWS S3. A few notes:

- **`endpoint`** is a bare `host[:port]` with **no scheme** — TLS is controlled by
  `use_ssl`, not the URL. (A leading `http(s)://` is tolerated and stripped.)
- **`path_style: true`** forces path-style bucket addressing, which MinIO, Ceph,
  and custom domains generally require. Leave it off for AWS S3.
- **`region`** defaults to `us-east-1`; use `auto` for Cloudflare R2.
- The **bucket must already exist** — Digestive uploads the record but never
  creates the bucket.
- Supply `access_key_id` / `secret_access_key` via `${VAR}` substitution so
  credentials stay out of the file.

## Flags

These flags are added to both `export` and `sync`:

| Flag | Default | Description |
| --- | --- | --- |
| `--requester-name` | — | Name of the person requesting the export. **Required** when compliance is configured. |
| `--requester-email` | — | Email of the requester. **Required** when compliance is configured; must be a valid address. |
| `--cleanup-on-audit-fail` | `false` | Delete the run directory if the audit record can't be written. |

When no `compliance:` block is present, these flags are ignored and everything
behaves exactly as before.
