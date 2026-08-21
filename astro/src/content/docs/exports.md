---
title: Exports
description: Export and anonymise database tables to Parquet with digestive export.
---

`digestive export` connects to the database setup in your `source.dsn` value in the config file and beings pulling rows into compressed [Parquet](https://www.databricks.com/blog/what-is-parquet) files. Parquet handles your data as binary, which means that types and encodings are preserved as much as possible, with the only drawback being that you can't open the files to read and inspect the data directly.

>Note: I'd strongly suggest creating a read-only user for exporting data.

Exports will create a folder full of parquet files in the `destination` directory:

```yaml
source:
  dsn: ${SINGLESTORE_DSN}

destination:
  directory: ./exports
```

The exported folder will also contain a `manifest` file - this contains the details of each table exported, it's columns, and the datatypes stored. This is used to translate the parquet data back into SQL later on.

## Tables.

In order to get `digestive export` to actually export data, you have to whitelist the database tables you want to export:

```
source:
	dsn: <DB>
tables:
	- users
	- transactions
```

This will export all the rows from the `users` and `transactions` tables.

### Adding optional constraints

Each `tables` key supports also supporters a raw `where` clause you can use to filter data, you can combine this with data from your `.env` file to effectively export data from specific people, or teams:

```yaml
source:
	dsn: <DB>
tables:
	- users:
		  where: 'team_id = ${TEAM_ID}'
	- transactions:
		  where: 'team_id = ${TEAM_ID}'
```

Managing the syntax of this is up to you - it will append these to raw queries without sanitisation, so be careful.

> Note: Some database export tools include a `limit` clause to export the last `{x}` rows, while that's not in `digestive` just yet, it should be soon.

### Anonymising data.

This is the core of `digestive` - the ability to anonymise data on the way out of your database.

By default, when adding a table to the `tables` list in `config.yaml`, `digestive` will export the data as-is, including any personal data.

To transform data, you can specify the columns and add a `transform` to it:

```yaml
source:
	dsn: <DB>
tables:
	- users:
		  where: 'team_id = ${TEAM_ID}'
		  columns:
			  email:
				  transform: hash_email
	- transactions:
		  where: 'team_id = ${TEAM_ID}'
```

Some transforms also have options, like the 'mask' transform:

```yaml
source:
	dsn: <DB>
tables:
	- users:
		  where: 'team_id = ${TEAM_ID}'
		  columns:
			  email:
				  transform: hash_email
	- transactions:
		  where: 'team_id = ${TEAM_ID}'
		  columns:
			  card_number:
				  transform: mask
				  keep_last: 4
```

You can also set a column to null:

```yaml
source:
	dsn: <DB>
tables:
	- users:
		  where: 'team_id = ${TEAM_ID}'
		  columns:
			  email:
				  transform: hash_email
	- transactions:
		  where: 'team_id = ${TEAM_ID}'
		  columns:
			  stripe_id:
				  # quoted "null" because otherwise
				  # yaml would see the actual value as null
				  transform: "null" 
			  card_number:
				  transform: mask
				  keep_last: 4
```

You can find a [full list of transforms](/comprehensive-docs/#transformers), along with their options, on the comprehensive docs page.
