# Diagnostic JSON schema

`quae --format=json` emits newline-delimited JSON (ND-JSON) — one
`Diagnostic` object per line, each terminated by `\n`. The schema is
pinned by the field order below; consumers may depend on both key names
and their emission order.

## Top-level Diagnostic

| Key        | Type                | Required | Notes                                                        |
|------------|---------------------|----------|--------------------------------------------------------------|
| `code`     | string              | yes      | Quae error code, e.g. `"E0301"`.                             |
| `severity` | string              | yes      | One of `"error"`, `"warning"`, `"note"`.                     |
| `title`    | string              | yes      | Human-readable title; matches the text-renderer title line.  |
| `location` | object              | yes      | `{file, line, col}` of the primary label.                    |
| `primary`  | Label               | yes      | Primary label carrying the failure message.                  |
| `notes`    | array of Label      | no       | Omitted when empty.                                          |
| `help`     | string              | no       | Omitted when empty.                                          |

## Label

| Key       | Type           | Required | Notes                                                              |
|-----------|----------------|----------|--------------------------------------------------------------------|
| `pos`     | object         | yes      | `{line, col, len}` — flat position triple.                         |
| `msg`     | string         | yes      | Inline message; may be empty when reasons carry the content.       |
| `reasons` | array of Reason| no       | Omitted when empty (consumers must handle an absent key).          |

## Reason (sum type)

Every Reason object carries a stable snake_case `"type"` tag. Seven variants:

| Tag                  | Variant fields                                                                     |
|----------------------|------------------------------------------------------------------------------------|
| `kind_mismatch`      | `want`, `got`, `actual` (strings; `want`/`got` are lowercase kind names)           |
| `bound_violation`    | `op`, `bound`, `actual`, `distance`                                                |
| `regex_mismatch`     | `pattern`, `input`, `diverge_at` (int, `-1` when unavailable)                      |
| `conjunct_failed`    | `expr`, `span` (`{file,line,col,length}`), `sub` (nested Reason, `null` if absent) |
| `disjunction_failed` | `arms` (array of `{arm, span, inner, score}`)                                      |
| `key_missing`        | `key`, `available_keys` (array, `[]` when empty), `suggestion`                     |
| `provenance`         | `span`, `snippet`                                                                  |

## Determinism and round-trip

- Output is byte-identical across runs for identical inputs (NF2).
- `RenderJSON(d)` followed by `json.Unmarshal` into a `Diagnostic` followed
  by `RenderJSON` again is byte-identical.
- Empty `Notes` is omitted; a present `notes` key is always a non-empty array.
- Empty `Reasons` is omitted; a present `reasons` key is always a
  non-empty array.
- Empty `available_keys` (KeyMissing) serialises as `[]` (never `null`)
  because consumers iterate without nil checks.

## Example

```json
{"code":"E0303","severity":"error","title":"type mismatch","location":{"file":"retry.cue","line":8,"col":21},"primary":{"pos":{"line":8,"col":21,"len":3},"msg":"want: int","reasons":[{"type":"kind_mismatch","want":"int","got":"string","actual":"\"five\""}]},"notes":[{"pos":{"line":8,"col":21,"len":3},"msg":"introduced here","reasons":[{"type":"provenance","span":{"file":"stdlib/nums.cue","line":7,"col":17,"length":3},"snippet":"\u003e=0"}]}],"help":"no value of kind `string` can satisfy a constraint of kind `int`"}
```
