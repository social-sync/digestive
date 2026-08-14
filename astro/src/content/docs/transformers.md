---
title: Transformers
description: Every built-in transform, what it's for, and its limits.
---

A **transform** changes a column's values on the way out. You attach one to a
column in the [`columns`](/configuration/#columns) section of a table:

```yaml
columns:
  email:
    transform: hash_email
```

Transforms fall into two families:

- **Redaction** — destroy or obscure a value: [`null`](#null),
  [`constant`](#constant), [`mask`](#mask).
- **Deterministic hashing** — replace a value with a stable pseudonym:
  [`hash`](#hash), [`hash_email`](#hash_email).

## Summary

| Transform | Family | Applies to | NULL handling | Needs `hashing.key` |
| --- | --- | --- | --- | --- |
| [`null`](#null) | redaction | any **nullable** column | — | no |
| [`constant`](#constant) | redaction | any column | left as NULL | no |
| [`mask`](#mask) | redaction | text columns only | left as NULL | no |
| [`hash`](#hash) | hashing | text columns only | left as NULL | yes |
| [`hash_email`](#hash_email) | hashing | text columns only | left as NULL | yes |

:::note[Text-only transforms]
`mask`, `hash`, and `hash_email` may only target **text columns** — `char`,
`varchar`, `text` (all sizes), `enum`, and `set`. If you point one at a numeric,
date, or binary column, `validate` and `export` fail with a clear error. This
is deliberate: numeric primary/foreign keys are best left untouched so
relationships are preserved exactly.
:::

## `null`

Sets the value to SQL `NULL`.

```yaml
deleted_reason:
  transform: "null"
```

:::note
`null` must be quoted in YAML (`"null"`), otherwise YAML parses it as an actual
null value rather than the string naming the transform.
:::

**Options:** none.

**Limits:**

- The column **must be nullable**. Targeting a `NOT NULL` column fails
  validation — otherwise the reconstructed data would violate the constraint.

## `constant`

Replaces every non-NULL value with a fixed literal.

```yaml
password:
  transform: constant
  value: "password"
```

**Options:**

| Option | Type | Required | Description |
| --- | --- | --- | --- |
| `value` | string | yes | The literal to substitute. |

**Behaviour & limits:**

- **NULL is left as NULL** — `constant` never fabricates a value where there
  wasn't one. Use `null` if you want to force NULL.
- `value` is written as text. For a **non-text** column the literal must be
  valid for that column's type (e.g. a number for an integer column), or the
  export fails when writing the value. For text columns, any string works.
- The literal is stored as-is. If you need a value the target system will treat
  specially — for example a bcrypt password hash Laravel will accept — compute
  it yourself and put the finished string in `value`.

## `mask`

Keeps a few characters at the start and/or end of a string and replaces the
middle with a fill character. Good for values you want to keep recognisable in
shape without revealing them (names, card-like strings).

```yaml
name:
  transform: mask
  keep_first: 1
  keep_last: 0
  mask_char: "*"
```

`"Super Admin"` → `"S**********"`.

**Options:**

| Option | Type | Default | Description |
| --- | --- | --- | --- |
| `keep_first` | integer | `0` | Number of leading characters to keep. |
| `keep_last` | integer | `0` | Number of trailing characters to keep. |
| `mask_char` | string | `"*"` | Single character used for the mask. |

**Behaviour & limits:**

- Text columns only.
- **NULL passes through** unchanged.
- Operates on Unicode characters (runes), not bytes, so multi-byte text masks
  correctly.
- If `keep_first + keep_last` is greater than or equal to the value's length,
  the value is returned **unchanged** — masking never reveals more characters
  than the original, and never lengthens it. (Short values are therefore not
  guaranteed to be obscured; combine with a `where` filter or a different
  transform if that matters.)
- `mask_char` must be exactly one character.

## Deterministic hashing

`hash` and `hash_email` both compute a keyed
[HMAC-SHA256](https://en.wikipedia.org/wiki/HMAC) of the value using
[`hashing.key`](/configuration/#hashing). Two properties make them useful for
anonymisation:

- **Deterministic** — the same input always produces the same output, so a
  value that appears in several places is pseudonymised consistently.
- **Global by default** — hashing is keyed only on the value (and the optional
  `group`), *not* on the table or column name. So a foreign key hashed in one
  table matches the same value hashed in another, and **joins survive** in the
  exported copy.

```yaml
# users.public_id and orders.user_public_id will still join, because the same
# input hashes to the same output everywhere.
```

:::caution[The key is load-bearing]
Because the output depends on `hashing.key`, the key must stay **stable across
runs** (and across any later reconstruction). Rotating it produces a completely
different, non-joinable dataset. Keep it secret and constant.
:::

### Hash groups

By default all hashing shares one global namespace, so two unrelated columns
that happen to hold the same literal value hash to the same output. To isolate
a column into its own namespace, give it a `group`:

```yaml
internal_ref:
  transform: hash
  group: internal   # hashed separately from the global namespace
```

Columns sharing a `group` hash consistently with each other, but differently
from other groups and from the default global namespace. Use this to
deliberately break an accidental collision.

## `hash`

Replaces a value with a hex pseudonym.

```yaml
facebook_user_id:
  transform: hash
```

`"8830682673669963"` → `"d05271e91851e2462aab3f14710ead16d1e596f85316cc6d0497411fc2ea8eac"`.

**Options:**

| Option | Type | Default | Description |
| --- | --- | --- | --- |
| `group` | string | `""` (global) | Hashing namespace — see [Hash groups](#hash-groups). |
| `length` | integer | `0` (full) | Truncate the hex output to this many characters. `0` keeps the full 64. |

**Behaviour & limits:**

- Text columns only; requires `hashing.key`.
- **NULL passes through** unchanged.
- The full output is **64 hex characters**. If the target column is narrower
  (e.g. `varchar(20)`), set `length` so the pseudonym fits — but note that a
  shorter hash increases the chance of collisions between distinct values.

## `hash_email`

Like `hash`, but the output is **email-shaped** so it satisfies validation and
formatting that expects an email address.

```yaml
email:
  transform: hash_email
```

`"admin@socialmind.io"` → `"a0b6cea60325c4fe@a11628f170.example"`.

**Options:**

| Option | Type | Default | Description |
| --- | --- | --- | --- |
| `group` | string | `""` (global) | Hashing namespace — see [Hash groups](#hash-groups). |

**Behaviour & limits:**

- Text columns only; requires `hashing.key`.
- **NULL passes through** unchanged.
- The pseudonym is derived from the **whole input value**, so identical
  addresses map to identical outputs (joins on email survive).
- The output always uses the reserved `.example` domain and is roughly 35
  characters long — it is a well-formed, non-routable placeholder, not a real
  address. It does not preserve the original local part or domain.
