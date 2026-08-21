---
title: Getting started
description: Set up a config-driven, repeatable export with digestive init.
---

Digestive is driven by a config file - `config.yaml`, this means that you can set up a repeatable export in a git repository to share with your team, and inject secrets from a `.env` file so that nothing sensitive is ever committed to VCS.

Install digestive locally or build it from source, then run `digestive init` - this will create a `config.yaml` and a `.env` file.

All commands share a `--json` option which will allow you to parse the output using agents or other scripts you might want to use to drive calls to it.
