# Quae Diagnostic Format Tests

End-to-end integration tests for the `--format` and `--color` surface of the
`quae` CLI, using [scrut](https://github.com/facebookincubator/scrut). Each
block feeds the same input fixture into `quae explain` and asserts either
the exact serialized output (text, JSON, SARIF) or the presence/absence of
ANSI escape sequences. Goldens mirror `tests/diagnostics.md` style; the
fixtures under `tests/diagnostics_rules*/` are reused verbatim.

Run with:
```bash
scrut test -w . tests/diagnostics_formats.md
```

Cross-cutting conventions:

- `2>&1` redirects the diagnostic stream (stderr) into stdout so scrut can
  capture it — matches the base `tests/diagnostics.md` suite.
- `--global-config /tmp/quae-nonexistent-global` isolates the run from host
  rules (same trick as the base suite).
- ANSI escapes in goldens are written as `\x1b[...]` with the scrut
  `(escaped)` marker on the relevant line; scrut decodes the literal
  backslash-`x1b` sequence to byte `0x1B` before matching.
- JSON bodies are matched with a `* (glob)` wildcard band where the field
  order is already pinned by the Go wire structs (see
  `internal/diag/render_json.go` and `render_sarif.go`) but inner content
  is too verbose to repeat verbatim. The goldens anchor the keys that form
  the schema contract (`"code"`, `"severity"`, `"$schema"`, `"version"`,
  `"runs"`, `"ruleId"`), which is what downstream consumers parse on.
- The E0201 `absent-path` rule under `tests/diagnostics_rules/` is reused
  as the single fixture across every block so format differences are the
  only variable.

## `--format=json` emits one JSON object per diagnostic (ND-JSON)

`RenderJSON` writes a single JSON object followed by a trailing newline per
diagnostic (ND-JSON). One rule + one absent-key failure = exactly one line.
The glob asserts the stable top-level schema keys in the documented order:
`code`, `severity`, `title`, `location`, `primary`, and the `help` tail for
this fixture.

```scrut
$ cat << 'EOF' |
> {
>   "hook_event_name": "PreToolUse",
>   "tool_name": "Bash",
>   "tool_input": {"command": "ls"},
>   "session_id": "test",
>   "cwd": "/tmp"
> }
> EOF
> quae explain absent-path --config tests/diagnostics_rules --global-config /tmp/quae-nonexistent-global --format=json 2>&1
{"code":"E0201","severity":"error","title":"key not found","location":{"file":"/__quae_rules__/absent_path.cue","line":11,"col":3},"primary":*"reasons":[{"type":"key_missing",*}]},"help":"*has keys: *"} (glob)
[1]
```

## `--format=sarif` emits a SARIF 2.1.0 document

`RenderSARIF` emits a single SARIF 2.1.0 JSON document. The golden anchors
`$schema`, `version: "2.1.0"`, `tool.driver.name: "quae"`, and a non-empty
`results[]` carrying the `ruleId` for the failing rule.

```scrut
$ cat << 'EOF' |
> {
>   "hook_event_name": "PreToolUse",
>   "tool_name": "Bash",
>   "tool_input": {"command": "ls"},
>   "session_id": "test",
>   "cwd": "/tmp"
> }
> EOF
> quae explain absent-path --config tests/diagnostics_rules --global-config /tmp/quae-nonexistent-global --format=sarif 2>&1
{"$schema":"https://json.schemastore.org/sarif-2.1.0.json","version":"2.1.0","runs":[{"tool":{"driver":{"name":"quae","version":*}},"results":[{"ruleId":"E0201","level":"error",*}]}]} (glob)
[1]
```

## `--color=always` wraps the severity word and caret in ANSI SGR escapes

`ANSIPalette` paints the severity word red (`\x1b[31m…\x1b[0m`), the caret
row red, and the `-->` location line dim (`\x1b[2m…\x1b[0m`). Even in a
non-TTY shell, `--color=always` forces the ANSI palette.

```scrut
$ cat << 'EOF' |
> {
>   "hook_event_name": "PreToolUse",
>   "tool_name": "Bash",
>   "tool_input": {"command": "ls"},
>   "session_id": "test",
>   "cwd": "/tmp"
> }
> EOF
> quae explain absent-path --config tests/diagnostics_rules --global-config /tmp/quae-nonexistent-global --color=always 2>&1
\x1b[31merror\x1b[0m[E0201]: key not found (escaped)
  --> \x1b[2m/__quae_rules__/absent_path.cue:11:3\x1b[0m (escaped)
   |
11 |         signals: user_confirmed: true
   |         \x1b[31m^^^^^^^\x1b[0m key "signals" not found in input at path <root> (escaped)
   |
   = help: <root> has keys: cwd, hook_event_name, session_id, tool_input, tool_name
[1]
```

## `--color=never` produces plain text (no ANSI)

`NoColorPalette` is the identity palette — every wrapper returns its input
unchanged. The golden is byte-identical to the `tests/diagnostics.md`
E0201 absent-path block.

```scrut
$ cat << 'EOF' |
> {
>   "hook_event_name": "PreToolUse",
>   "tool_name": "Bash",
>   "tool_input": {"command": "ls"},
>   "session_id": "test",
>   "cwd": "/tmp"
> }
> EOF
> quae explain absent-path --config tests/diagnostics_rules --global-config /tmp/quae-nonexistent-global --color=never 2>&1
error[E0201]: key not found
  --> /__quae_rules__/absent_path.cue:11:3
   |
11 |         signals: user_confirmed: true
   |         ^^^^^^^ key "signals" not found in input at path <root>
   |
   = help: <root> has keys: cwd, hook_event_name, session_id, tool_input, tool_name
[1]
```

## `NO_COLOR=1` (community convention) suppresses ANSI with no explicit flag

`ResolveColorMode` honours the `NO_COLOR` env var whenever `--color` is
absent and `QUAE_COLOR` is unset. The output matches `--color=never`.

```scrut
$ cat << 'EOF' |
> {
>   "hook_event_name": "PreToolUse",
>   "tool_name": "Bash",
>   "tool_input": {"command": "ls"},
>   "session_id": "test",
>   "cwd": "/tmp"
> }
> EOF
> NO_COLOR=1 quae explain absent-path --config tests/diagnostics_rules --global-config /tmp/quae-nonexistent-global 2>&1
error[E0201]: key not found
  --> /__quae_rules__/absent_path.cue:11:3
   |
11 |         signals: user_confirmed: true
   |         ^^^^^^^ key "signals" not found in input at path <root>
   |
   = help: <root> has keys: cwd, hook_event_name, session_id, tool_input, tool_name
[1]
```

## `QUAE_FORMAT=json` (env var, no flag) matches `--format=json`

`resolveFormat` consults `QUAE_FORMAT` only when `--format` is absent. Setting
the env to `json` produces the same ND-JSON shape as `--format=json`.

```scrut
$ cat << 'EOF' |
> {
>   "hook_event_name": "PreToolUse",
>   "tool_name": "Bash",
>   "tool_input": {"command": "ls"},
>   "session_id": "test",
>   "cwd": "/tmp"
> }
> EOF
> QUAE_FORMAT=json quae explain absent-path --config tests/diagnostics_rules --global-config /tmp/quae-nonexistent-global 2>&1
{"code":"E0201","severity":"error","title":"key not found","location":{"file":"/__quae_rules__/absent_path.cue","line":11,"col":3},"primary":*"reasons":[{"type":"key_missing",*}]},"help":"*has keys: *"} (glob)
[1]
```

## `QUAE_COLOR=always` beats `NO_COLOR=1` (quae-specific wins)

`resolveColor` precedence: the quae-specific `QUAE_COLOR` overrides the
community `NO_COLOR` when both are set. ANSI escapes are present.

```scrut
$ cat << 'EOF' |
> {
>   "hook_event_name": "PreToolUse",
>   "tool_name": "Bash",
>   "tool_input": {"command": "ls"},
>   "session_id": "test",
>   "cwd": "/tmp"
> }
> EOF
> QUAE_COLOR=always NO_COLOR=1 quae explain absent-path --config tests/diagnostics_rules --global-config /tmp/quae-nonexistent-global 2>&1
\x1b[31merror\x1b[0m[E0201]: key not found (escaped)
  --> \x1b[2m/__quae_rules__/absent_path.cue:11:3\x1b[0m (escaped)
   |
11 |         signals: user_confirmed: true
   |         \x1b[31m^^^^^^^\x1b[0m key "signals" not found in input at path <root> (escaped)
   |
   = help: <root> has keys: cwd, hook_event_name, session_id, tool_input, tool_name
[1]
```

## `--format=sarif --color=always` — color is text-only, SARIF stays plain JSON

Color is a text-format concern; the JSON and SARIF renderers never consult
the palette. A `--color=always` flag alongside `--format=sarif` must not
inject ANSI escapes into the SARIF document.

```scrut
$ cat << 'EOF' |
> {
>   "hook_event_name": "PreToolUse",
>   "tool_name": "Bash",
>   "tool_input": {"command": "ls"},
>   "session_id": "test",
>   "cwd": "/tmp"
> }
> EOF
> quae explain absent-path --config tests/diagnostics_rules --global-config /tmp/quae-nonexistent-global --format=sarif --color=always 2>&1
{"$schema":"https://json.schemastore.org/sarif-2.1.0.json","version":"2.1.0","runs":[{"tool":{"driver":{"name":"quae","version":*}},"results":[{"ruleId":"E0201","level":"error",*}]}]} (glob)
[1]
```

## Determinism guard — repeated `--format=json` runs are byte-identical

`RenderJSON` is deterministic (NF2 in scope.md): identical inputs produce
byte-identical output across runs. The block runs the same invocation
twice, diffs the captures in-shell, and asserts equality. Scrut has no
native "run twice and compare" primitive, so the guard lives inline —
the exact output the `quae explain` produced isn't re-asserted here
(already covered upstream); only stability across invocations is.

```scrut
$ IN='{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"ls"},"session_id":"test","cwd":"/tmp"}'; A=$(echo "$IN" | quae explain absent-path --config tests/diagnostics_rules --global-config /tmp/quae-nonexistent-global --format=json 2>&1); B=$(echo "$IN" | quae explain absent-path --config tests/diagnostics_rules --global-config /tmp/quae-nonexistent-global --format=json 2>&1); [ "$A" = "$B" ] && echo "json: byte-identical"
json: byte-identical
```

## Determinism guard — repeated `--format=sarif` runs are byte-identical

Same contract as the JSON determinism guard, but for the SARIF renderer.
`RenderSARIF` uses hand-rolled struct ordering (no `map[string]any` sort)
so two runs always emit the same byte sequence.

```scrut
$ IN='{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"ls"},"session_id":"test","cwd":"/tmp"}'; A=$(echo "$IN" | quae explain absent-path --config tests/diagnostics_rules --global-config /tmp/quae-nonexistent-global --format=sarif 2>&1); B=$(echo "$IN" | quae explain absent-path --config tests/diagnostics_rules --global-config /tmp/quae-nonexistent-global --format=sarif 2>&1); [ "$A" = "$B" ] && echo "sarif: byte-identical"
sarif: byte-identical
```
