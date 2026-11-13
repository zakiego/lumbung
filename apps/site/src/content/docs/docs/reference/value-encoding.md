---
title: Value encoding
description: How SQLite values travel as JSON in requests and responses, without losing precision or type.
sidebar:
  order: 7
---

Every value in a response is an object with a `type`:

```json
{"type": "null"}
{"type": "integer", "value": "9007199254740993"}
{"type": "float", "value": 1.5}
{"type": "text", "value": "hello"}
{"type": "blob", "base64": "AAEC"}
```

| Type | Encoding |
|---|---|
| `null` | no value |
| `integer` | decimal string, so all 64-bit integers survive any JSON parser |
| `float` | JSON number; infinities are the strings `"Infinity"` and `"-Infinity"` |
| `text` | JSON string |
| `blob` | standard base64 with padding, in `base64` |

An integer and a float never look alike: `1` stored as INTEGER is `{"type":"integer","value":"1"}`, and `1.0` stored as REAL is `{"type":"float","value":1}`.

## Rows and columns

A result lists its columns once, then each row as an array in column order. Two columns with the same name both keep their values:

```json
{
  "columns": [{"name": "a", "decltype": ""}, {"name": "a", "decltype": ""}],
  "rows": [[{"type": "integer", "value": "1"}, {"type": "integer", "value": "2"}]],
  "rows_affected": 0,
  "last_insert_rowid": 0
}
```

`decltype` is the column's declared type in the table, or empty for an expression.

## Parameters

Parameters accept the same typed objects. For convenience they also accept plain JSON:

| You send | It binds as |
|---|---|
| `null` | NULL |
| `true` / `false` | integer 1 / 0 |
| `"text"` | text |
| `42`, `-7` | integer (must fit in 64 bits) |
| `1.5`, `1.0`, `1e3` | float |

A number written without a fraction or exponent is an integer. One too large for 64 bits is an error, never a silently rounded float. Blobs must use the typed form.

Positional parameters (`?`, `?1`) take an `args` array. Named parameters (`:name`, `@name`, `$name`) take a `named` object, keyed with or without the prefix. A statement uses one style, and every placeholder needs a value.
