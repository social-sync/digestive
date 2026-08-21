---
title: Restoring data to a database
description: Restore an export into a database, sync end-to-end, and reconcile schema drift.
---

After you've ran an export, you can then restore that data to another database using the `digestive restore` command.

This takes the data from the parquet files and transforms it into SQL INSERT statements, and streams the data straight to `stdout` in your terminal.

You'll need to specify a `--dialect` when running this command - currently we support `mysql` and `singlestore`, this ensures that the INSERT statements are properly formatted.

Because it streams to `stdout`, you can output the result to a file:

```bash
digestive restore {path-to-export-folder} --dialect=mysql > import.sql
```

Or you can pipe the result straight into the `mysql` command to import it directly:

```bash
mysql -uroot -p my_database_name < digestive restore {path-to-export-folder} --dialect=mysql
```

## Sync from source to destination.

You can run an `export` and automatically pipe the result of the export from your source database by setting your destination database up in your config.yaml:

```yaml
source:
	dsn: <DB>
sync:
	dsn: ${SYNC_DSN} # go-sql-driver/mysql DSN for the destination
	type: mysql # mysql or singlestore
```

Then you can run `digestive sync` - this will run the entire process end-to-end, and you can pass the `--cleanup` flag to remove the exported parquet files at the end.

The `sync` runs in stages: first it will `export`, storing the data, then it will run `restore`.


## Dealing with schema differences.

`digestive` is designed (fundamentally) as a tool for pulling data from a production environment and using it to test or debug in a staging or local development environment.

During development, your staging or local environments' database schema might differ from the production environment in some way - new columns, renamed columns, renamed or dropped tables even.

When `digestive restore` runs, it doesn't know any of this, by design, so you need to create a `restore.yaml` file to tell it what's different, and reconcile the changes.

```yaml
tables:
	# The 'crm' table has been renamed to 'crm' information
	crm:
		rename_table: crm_information
	
	# The 'users' table's 'phone' column has been renamed to 'primary_phone'
	users:
		rename_columns:
			phone: primary_phone
	
	# The 'logins' table now includes a non-nullable IP address column
	logins:
		add_columns:
			ip_address: '0.0.0.0'
	
	# The 'compliance_records' table has been removed
	compliance_records:
		drop_table: true
```

> Note: Transforming data on the way in isn't currently possible, but i'm working on allowing people to provide custom transforms.

Note that any of these broken combinations in the `restore.yaml` will produce an error before anything runs:

- a **rename-source / drop / rename-table / drop-table** targeting a column or table **absent from the manifest** (a typo or a stale rule);
- an **`add_columns`** name that **already exists** and is not being renamed or dropped away (that is a rename, not an add);
- a rename **target**, or an added column, that **collides** with another emitted column (two columns, one name);
- a column named in both `rename_columns` and `drop_columns`;
- **`drop_table`** combined with any other rule for the same table;
- two source tables **emitting into the same target name**;
- a table left with **no columns** after drops.
