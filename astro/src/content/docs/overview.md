---
title: Overview
description: What Digestive is, what it does, and what it doesn't do yet.
---

Digestive is a command line tool for exporting data from your database, anonymising it, then reloading it back into your local or staging database.

It includes tools for transforming data on the way out, compressing it, mapping schema differences between your production and local/staging DBs, and for storing compliance and audit logs.

It's built to handle large datasets, written in Go for speed and memory efficiency, and it's the tool we at [Social Sync](https://socialsync.io) have been looking for, for a long time.

> Note: It's not perfect, and support for databases is limited at the moment, but hopefully as we go, we can add more support.

## Usage of AI

This tool was - as much software is these days - built with the assistance of AI. But that doesn't automatically mean it's "slop" (though, that's for you to judge as you use it). I've tried to build a thoughtful tool, and i've endeavoured to hand-write the documentation and usage guides to reduce how "AI-written" the documentation feels, as well as making sure it aligns with the quality of a hand-written tool that i'd actually _want_ to use myself.

## Built with Laravel in mind.

Digestive was built as a tool anyone can use, but here at [Social Sync](https://socialsync.io) we use Laravel - so a lot of the benchmarking, architectural decisions and more are directly influenced by the features and databases that Laravel supports.

## What it does

- Export data from the supported databases using `digestive export`
- Convert data from the parquet format into SQL INSERT statements using the `digestive restore` command, which you can pipe into the `mysql` command.
- Sync data from a source database straight into a destination database using `digestive sync`. As of now though, it doesn't handle creating tables.
- You can use another config file - `restore.yaml` - to make changes to the data as it's restored, if your destination database schema is different from your source one.
- You can specify audit logging configuration - JSON  based audit logs  are created each time an `export` is ran that identifies the user, what they exported, and when.

## What it doesn't do

- It's not meant as a data migration tool between two database types just yet.
- It's purely a command-line tool for now. 
- It only exports data to the local disk right now, S3 + compatible storage for exports is planned.
- No Postgres support as of yet.
- Some type mappings and encodings might be flaky, you can see the `tests` and test matrix for an idea of what is being tested against for a 1-1 reproduction from source to destination.
