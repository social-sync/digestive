---
status: accepted
---

# Deterministic hashing is global-by-default HMAC, keyed from the environment

## Context and decision

Anonymised data must remain *referentially usable*: a text key hashed in one
table has to hash identically wherever it's referenced, or foreign-key joins
break in the exported copy. We decided deterministic hashing is:

- **HMAC-SHA256 keyed by a secret sourced only from the environment** (referenced
  in config as `${VAR}`, never written in plaintext). A plain unsalted hash of
  common PII (emails, phones) is brute-forceable, so a secret key is required to
  actually anonymise.
- **Global by default** — the same input string yields the same pseudonym in
  every table and column, with no extra configuration, so relationships survive
  automatically. An optional **hash group** label scopes hashing to a separate
  namespace when a deliberate collision needs breaking.
- **Text/string columns only** — the tool rejects a hash transform aimed at a
  non-text column. An **email** variant preserves email shape (`local@domain`)
  for data-shape/validation purposes.

## Considered options

- **Per-column / explicit-group-only hashing:** rejected as the default — it
  forces the operator to hand-wire every FK relationship into a shared group,
  which is error-prone and easy to forget, silently breaking joins. Made the
  opt-in override instead.
- **Unkeyed hashing:** rejected — reversible by brute force for common PII.
- **Format-preserving hashing for numeric keys (int→int):** out of scope —
  hashing is restricted to text columns, so numeric PKs/FKs are simply not
  hashed.

## Consequences

- The HMAC secret is load-bearing: the **same key must be reused** across runs
  (and eventually across export → reconstruct) for pseudonyms to stay stable.
  Rotating the key produces a completely different, non-joinable dataset. Key
  management is the operator's responsibility.
- Global hashing means two *unrelated* text columns sharing a literal value hash
  to the same output, leaking that they were equal. Accepted as a minor trade
  for zero-config join integrity; use a hash group to isolate when it matters.
- Stable pseudonyms are what will make the **deferred** `reconstruct` / streaming
  load produce a dataset whose relationships still hold in the destination DB.
