---
status: accepted
---

# Compliance audit logging for `export` and `sync`

## Context and decision

Regulated users need to answer, after the fact, "who pulled this export, when,
under what config, and how much data left each table?" Nothing in the tool
recorded that: an export produced Parquet plus a `manifest.json` and no record of
the human or the request behind it.

`digestive` now writes a per-run **audit record** — a single JSON document — for
every `export` and `sync`, to either an S3-compatible bucket or a local
directory. The feature is **opt-in and gated by config**: it is off unless a
`compliance:` block is present, and when present it is mandatory.

```yaml
compliance:
  audit:
    # exactly one of `directory` or `s3`
    directory: ./audit-logs
    s3:
      endpoint: ${AUDIT_S3_ENDPOINT}   # host[:port], no scheme
      bucket: ${AUDIT_S3_BUCKET}
      prefix: exports/
      region: ${AUDIT_S3_REGION:-us-east-1}
      access_key_id: ${AUDIT_S3_ACCESS_KEY}
      secret_access_key: ${AUDIT_S3_SECRET_KEY}
      use_ssl: true
      path_style: true
```

Several decisions are load-bearing.

### Presence of `compliance:` is the gate; "on but broken" is a hard error

The whole point of a compliance control is that it can't be silently skipped.
So the gate is **the presence of the `compliance:` key**, not an extra flag a
user could forget. When the key is present, `export` and `sync` **require**
`--requester-name` and `--requester-email`, validated before any work starts
(name non-empty; email via `net/mail.ParseAddress`).

Equally, a present-but-malformed block must never degrade into a no-op that
quietly disables auditing. `config.Load` validates the block eagerly: exactly one
of `directory`/`s3`, and all required S3 fields present. Any problem fails the
command loudly. Presence means "on"; "on but broken" is an error, never a silent
skip.

Validating in `config.Load` (rather than per-command, as `sync` does for its own
block — ADR-0007) is deliberate here: the gate must apply uniformly to every
command that could pull data, so it lives at the shared load path.

### The record: full context, secrets redacted

One JSON document per run captures everything an auditor needs:

- **who** — requester name and email (from the required flags);
- **when** — the export start (`manifest.created_at`) and the audit write time;
- **what config governed it** — the *resolved, effective* config, so `${VAR}`
  references are shown as the values that actually applied;
- **the run** — run name/directory, the full `manifest.json` embedded inline,
  and a flat per-table `row_counts` map;
- **provenance** — hostname and the `digestive` build version.

The config is the compliance-relevant part (which tables, which columns, which
transforms governed what left the database) — but it also holds secrets. Writing
a DB password or the PII-hashing key into a durable, possibly-shared audit log
would itself be a compliance violation. So the effective config is **redacted**
before embedding: fields are tagged `redact:"true"` in the config structs
(`source.dsn`, `sync.dsn`, `hashing.key`, and the S3 credentials), and a
reflection walk blanks them to `***REDACTED***`. The tag lives next to each field
so the sensitive-field list can't drift out of sync with the struct as new fields
are added. Whole values are redacted (the entire DSN, not just its password) —
simpler and with no risk of a partial-mask leak.

### Success-only, hard-fail, and `--cleanup-on-audit-fail`

The record needs the completed manifest and row counts, which only exist once an
export finishes. So the audit is written **only on success**, right after the
export completes.

If the *audit write itself* fails (bucket down, permission denied, disk full),
the command **fails hard** (non-zero exit). A silently-missing audit trail is the
worst outcome for a compliance control, so a momentary outage turning a good
export into a failed command is the correct bias. The exported files still exist
on disk; the operator is simply told, unambiguously, that the record did not
land.

To let a user *enforce* "no export without an audit," `--cleanup-on-audit-fail`
deletes the run directory when the audit write fails — so a completed export can
never be left behind without its trail. It is off by default (files are kept for
inspection) and composes with the existing `--delete-on-failure` (which handles
*export* failure; this new flag handles *audit* failure).

For `sync`, the audit is written **after the export succeeds but before `Apply`**
to the target. That ordering guarantees "data loaded into the destination" can
never coexist with "no audit trail": a failed audit aborts the whole sync before
any row reaches the target.

### An `action` field, one entry per invocation

`sync` produces its export by calling the same `runExport()` the `export` command
uses. If audit-writing lived inside that shared function, a single `sync` would
emit two records. So audit-writing is **not** baked into the export path; it is a
discrete step each command drives after the artifact exists, stamping its own
`action`:

- `export` → one record, `action: "export"`;
- `sync` → one record, `action: "sync"` — whether it exported fresh or reused an
  existing run directory (a sync-from-existing still moves data, so it is still a
  compliance event worth logging).

All actions share one schema; `action` distinguishes them. A future `download`
action can reuse the format unchanged.

### `minio-go` for the S3 sink

The requirement is explicitly *S3-compatible* (MinIO, R2, Ceph, Wasabi, AWS),
not AWS-specific. `github.com/minio/minio-go/v7` is a single module with a clean
`PutObject`, first-class custom endpoints, path-style addressing, and static
credentials — a much smaller footprint than `aws-sdk-go-v2`'s dozen-module split
for what is one small `PUT` per run. Credentials come from explicit,
env-expanded config keys (consistent with how `source.dsn`/`hashing.key` already
resolve secrets) rather than the ambient AWS credential chain, so the audit
destination is fully described by the config.

The endpoint is a bare `host[:port]` (the scheme is controlled by `use_ssl`); a
leading `http(s)://` is tolerated and stripped. We avoid checksum/trailing-header
options and set a region, which keeps the single `PUT` compatible with R2 (which
has rejected minio-go's newer `x-amz-checksum-*` and streaming trailers). No
bucket-exists / create-bucket check is performed: the bucket is a precondition,
provisioned out of band.

### The audit object name is collision-resistant

When many people or machines write into one shared destination, timestamp-based
run names collide. The audit object is therefore named
`<run-name>-<hostname>-<8hex>.json`: hostname attributes origin, and a
`crypto/rand` suffix guarantees uniqueness. The suffix is applied to the **audit
object only** — the export run directory keeps its existing naming, so this
feature changes no current export behaviour. The same hostname and run name are
also stored as fields *inside* the record, so they're queryable, not just in the
key.

## Considered options

- **A separate `compliance.yaml` file vs. a key in `config.yaml`:** chose a key
  in the existing config. It loads through the same `config.Load` (env-expansion,
  `.env` overlay) with no second discovery path, and colocates the gate with the
  export config it governs.
- **An explicit `--compliance` / `--audit` flag to enable it:** rejected — a gate
  you must remember to turn on is not a gate. Presence of the config block is the
  switch.
- **Raw config file text vs. resolved-and-redacted config:** chose resolved +
  redacted. Raw text would capture pre-expansion `${VAR}` placeholders (not what
  actually ran) and any hardcoded secret verbatim. The resolved config shows what
  governed the run; redaction keeps secrets out of the durable record.
- **A central hardcoded list of sensitive field paths vs. struct tags:** chose
  tags. A central list drifts the moment a new secret field is added elsewhere; a
  `redact:"true"` tag next to the field travels with it.
- **Masking only the DSN password vs. redacting the whole DSN:** chose whole-DSN
  redaction — simpler, and no risk of a partial mask leaking host/user detail a
  given deployment considers sensitive.
- **Soft-fail (warn) on audit-write failure:** rejected as the default — it
  allows exports with no trail. Hard-fail is the compliance-correct bias;
  `--cleanup-on-audit-fail` goes further and removes the orphaned export.
- **Writing the audit inside `runExport`:** rejected — `sync` reuses
  `runExport`, so this would double-log. Command-level writing keyed by `action`
  yields exactly one record per invocation.
- **`aws-sdk-go-v2` for S3:** rejected for footprint. The need is S3-compatible,
  not AWS-specific; `minio-go` covers every provider in one light module.

## Consequences

- Regulated users get a durable, queryable record of every data pull — who,
  when, what config, which manifest, how many rows per table — without the record
  itself leaking secrets.
- The feature is inert unless `compliance:` is configured; existing users are
  unaffected. When configured, it is mandatory and cannot be silently bypassed.
- A new dependency (`minio-go`) enters the tree, scoped to the audit sink.
- `export`/`sync` gain `--requester-name`, `--requester-email`, and
  `--cleanup-on-audit-fail`. The export run-directory naming is unchanged.
- The record schema is versioned (`audit_version`) and `action`-keyed, so new
  actions (e.g. `download`) and fields can be added without a breaking change.
