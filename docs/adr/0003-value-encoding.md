# 0003: Values over JSON

Date: 2026-11-14. Status: accepted.

## Context

PLAN 6.4: decide once how SQLite values travel as JSON, with no silent precision loss. JSON numbers lose integers past 2^53 in most clients, can't tell `1` (integer) from `1.0` (real), and have no bytes type.

## Decision

Every value in a response is a typed object (`internal/value`):

```json
{"type":"null"}
{"type":"integer","value":"9007199254740993"}
{"type":"float","value":1.5}
{"type":"text","value":"hello"}
{"type":"blob","base64":"AAEC"}
```

- Integers are decimal strings, so all of int64 survives any JSON parser.
- Floats are JSON numbers in the shortest form that parses back to the same float64. Positive and negative infinity are the strings `"Infinity"` and `"-Infinity"`. SQLite stores NaN as NULL, so NaN never appears.
- Blobs are standard base64 with padding.
- Rows are arrays in column order and columns are listed separately, so repeated column names (`SELECT 1 AS a, 2 AS a`) keep both values.

Parameters accept the same typed objects, or plain JSON for convenience: `null`, `true`/`false` (integer 1/0), strings (text), and numbers. A number without a fraction or exponent is an integer and must fit in 64 bits; a bigger one is an error, never a float. Any other number is a float. Blob parameters must use the typed form.

The SDK turns typed values into native ones (integers as `number` when safe, otherwise `bigint`, or as configured).

The shapes match the libSQL remote protocol's value objects, so milestone 11 reuses the same code.

## Consequences

Responses are wordier than bare JSON. In exchange, a value read through the API and written back is always the same value. `internal/value/value_test.go` round-trips the edge cases.
