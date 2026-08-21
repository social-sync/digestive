---
title: Compliance Logs
description: Write per-run audit logs for every export and sync.
---

Digestive also has the ability to write compliance logs - records of an export being ran, and by whom, in order to track who has captured data from the source database.

These logs can be stored locally, or forwarded to a remote S3 (or s3-compatible) storage bucket for archiving.

To get started generating compliance logs, simply add a `compliance` segment to `config.yaml`

```yaml
# ...
compliance:
	audit:
		# Local directory:
		directory: ./audit-logs
		
		# …or an S3-compatible bucket (set one, not both):

		s3:
			endpoint: ${AUDIT_S3_ENDPOINT} # host[:port], no scheme
			bucket: ${AUDIT_S3_BUCKET}
			prefix: exports/ # optional key prefix
			region: ${AUDIT_S3_REGION:-us-east-1}
			access_key_id: ${AUDIT_S3_ACCESS_KEY}
			secret_access_key: ${AUDIT_S3_SECRET_KEY}
			use_ssl: true
			path_style: true
```

Once you've added this, both the `export` and `sync` commands now REQUIRE you to provide requester information:

```yaml
./digestive export \
--requester-name "Jane Auditor" \
--requester-email "jane@example.com"
```

Once you run either command, you'll start to see logs appear, here's an example of what one looks like:

```json
{
	"audit_version": 1,
	"action": "export",
	"requester": { "name": "Jane Auditor", "email": "jane@example.com" },
	"hostname": "worker-03.internal",
	"timestamps": {
		"export_started_at": "2026-08-19T14:30:00Z",
		"audit_written_at": "2026-08-19T14:31:12Z"
	},
	"output": {
		"run_name": "2026-08-19T14-30-00Z",
		"run_directory": "/data/exports/2026-08-19T14-30-00Z"
	},
	"config": { "…": "the effective config, secrets redacted" },
	"manifest": { "…": "the full manifest.json, embedded inline" },
	"row_counts": { "users": 10432, "orders": 88123 },
	"tool_version": "1.4.2"
}
```

The file includes the entire `config.yaml` in the `config` key, but it also redacts secrets by default, including:

- `source.dsn` will never be included.
- `sync.dsn` will never be included.
- `hashing.key` will never be included.
- any s3 `access_keys`.

If you're working in an environment where the audit logs MUST be written in order for the `export` or `sync` to have succeeded, you can also pass the `--cleanup-on-audit-fail` flag, which, if the audit log fails to write, will cleanup the export and exit with an error.
