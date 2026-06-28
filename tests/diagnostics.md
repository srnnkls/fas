# Fas Diagnostics Tests

End-to-end integration tests for the diagnostic surface of the `fas` CLI,
using [scrut](https://github.com/facebookincubator/scrut). Each block drives
a specific diagnostic code, `--explain` filter mode, or `fas explain`
subcommand exit path and asserts the exact stdout+stderr the binary emits.

Run with:
```bash
scrut test -w . tests/diagnostics.md
```

Fixture rules live under `tests/diagnostics_rules*/`:

- `tests/diagnostics_rules/` — the core three (`absent-path`, `leaf-regex`,
  `disjunction`) used by the `--explain=both|fired` blocks.
- `tests/diagnostics_rules_kind_mismatch/` — E0303 (`KindMismatch`).
- `tests/diagnostics_rules_bound_violation/` — E0301 (`BoundViolation`, with
  distance).
- `tests/diagnostics_rules_key_missing_hint/` — E0201 (`KeyMissing` +
  `Suggestion` footer).
- `tests/diagnostics_rules_disjunction_close/` — E0401 with a close arm
  (ranked `closest arm was X` primary).
- `tests/diagnostics_rules_disjunction_ref/` — E0401 reached via a
  hidden-sibling definition rather than a literal-on-field disjunction.
- `tests/diagnostics_rules_provenance/` — E0301 with a cross-file conjunct
  imported from the stdlib (`path.#systemInCommand`); exercises the
  Provenance footer.
- `tests/diagnostics_rules_broken_scope/` — load-time E0501.
- `tests/diagnostics_rules_broken_cross/` — load-time E0502.
- `tests/diagnostics_rules_broken_let/` — load-time E0506 (`let` in `when`).
- `tests/diagnostics_rules_broken_if/` — load-time E0507 (`if` comprehension
  in `when`).
- `tests/diagnostics_rules_bind/` — E0601 (`BindingMismatch`, `@bind`
  variable equality).

Each block redirects stderr into stdout (`2>&1`) so scrut — which only
captures stdout by default — sees the diagnostic stream. The
`--global-config /tmp/fas-nonexistent-global` trick isolates the suite from
host-global rules.

The minimal-form rules documented in scope.md F7-F12 apply throughout:

- No "constraint not satisfied" restatement — the Title alone names the
  failure class.
- Conditional `want:` — emitted only when the caret span is not already the
  literal constraint (the "cheap/strong gate" from F7; see
  `feedback_diag_no_restate.md`).
- Per-Reason text formatting — `KindMismatch` → `want: X, got: Z` (Z is the
  concrete actual value, which already shows its kind);
  `BoundViolation` → `V violates op B (off by N)`;
  `DisjunctionFailed` no-close-arm → `got V — no arm was close` + `= note:
  tried arms:`.
- Same-span label collapse — the source line prints once, subsequent
  messages stack aligned under the same caret column.
- `KeyMissing.Suggestion` non-empty → `= hint: did you mean "X"?` footer.

## E0201 — absent path segment (no hint path)

The `absent_path` rule demands `signals.user_confirmed: true`; a Bash payload
that omits `signals` entirely produces an E0201 caret at `signals` and a
help line listing the keys the input actually exposes at that level. No
input key is within Levenshtein distance 2 of "signals", so no `= hint:`
footer appears.

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
> fas explain absent-path --config tests/diagnostics_rules --global-config /tmp/fas-nonexistent-global 2>&1
error[E0201]: key not found
  --> tests/diagnostics_rules/absent_path.cue:11:3
   |
11 |         signals: user_confirmed: true
   |         ^^^^^^^ key "signals" not found at <root>
   |
   = help: <root> has keys: cwd, hook_event_name, session_id, tool_input, tool_name
[1]
```

## E0201 — absent key with did-you-mean hint

The `key_missing_hint` rule demands `tool_input.command`; the payload
supplies `tool_input.commnd` (Levenshtein 1). `KeyMissing.Suggestion` is
non-empty, so the renderer appends a `= hint: did you mean "commnd"?`
footer under the help line. The `parsed` key appears in the available-keys
list because the preprocessor injects a `parsed.*` synthetic subtree
alongside any `tool_input.command` it sees.

```scrut
$ cat << 'EOF' |
> {
>   "hook_event_name": "PreToolUse",
>   "tool_name": "Bash",
>   "tool_input": {"commnd": "ls"},
>   "session_id": "test",
>   "cwd": "/tmp"
> }
> EOF
> fas explain key-missing-hint --config tests/diagnostics_rules_key_missing_hint --global-config /tmp/fas-nonexistent-global 2>&1
error[E0201]: key not found
  --> tests/diagnostics_rules_key_missing_hint/key_missing_hint.cue:10:15
   |
10 |         tool_input: command: "ls"
   |                     ^^^^^^^ key "command" not found at tool_input
   |
   = help: tool_input has keys: commnd, parsed
   = hint: did you mean "commnd"?
[1]
```

## E0301 — leaf constraint failure (regex, single-conjunct)

The `leaf_regex` rule pins `tool_input.command: =~"^rm "`. With a single
conjunct the localize walker emits an empty-Reasons Label (no
`ConjunctFailed` wrapper, no `want:` gate) and the renderer falls through
to the bare caret row — no `got:` restatement, no `= want:` footer.

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
> fas explain leaf-regex --config tests/diagnostics_rules --global-config /tmp/fas-nonexistent-global 2>&1
error[E0301]: leaf constraint failed
  --> tests/diagnostics_rules/leaf_regex.cue:12:24
   |
12 |         tool_input: command: =~"^rm "
   |                              ^^^^^^^^^^^^^^^^^ got: "ls"
   |
   | ls
   | ^
   = note: regex first diverged at offset 0
[1]
```

## E0301 — bound violation with distance

The `bound_violation` rule requires `tool_input.retry_count: _int & <=10`.
An input `retry_count: 12` fails the `<=10` conjunct with a `BoundViolation`
whose primary row renders `V violates op B (off by N)`. A second
(same-span) Label stacks the `want:` expansion of the full conjunction
underneath — emitted because the caret spans the whole `_int & <=10` form,
not just the literal bound.

```scrut
$ cat << 'EOF' |
> {
>   "hook_event_name": "PreToolUse",
>   "tool_name": "Bash",
>   "tool_input": {"retry_count": 12},
>   "session_id": "test",
>   "cwd": "/tmp"
> }
> EOF
> fas explain bound-violation --config tests/diagnostics_rules_bound_violation --global-config /tmp/fas-nonexistent-global 2>&1
error[E0301]: leaf constraint failed
  --> tests/diagnostics_rules_bound_violation/bound_violation.cue:11:35
   |
11 |         tool_input: retry_count: _int & <=10
   |                                         ^^^^ 12 violates <= 10 (off by 2)
   |
11 |         tool_input: retry_count: _int & <=10
   |                                  ^^^^^^^^^^^ want: int & <=10
[1]
```

## E0301 — provenance footer (cross-file conjunct)

The `provenance` rule constrains `tool_input.command` via a stdlib-defined
regex (`path.#systemInCommand`). When the input fails the constraint, the
localize walker harvests cross-file conjuncts on `ruleNext` and emits one
`Provenance` Note per stdlib-origin conjunct. The text renderer surfaces
each Provenance Note as a `= note: constraint introduced at <file:line:col>`
footer, bypassing the `SourceCache.LineAt` filter that the caret-frame
pipeline applies — the Span carries the coordinates directly, no
`token.Pos` exists for these synthetic Notes.

```scrut
$ cat << 'EOF' |
> {
>   "hook_event_name": "PreToolUse",
>   "tool_name": "Bash",
>   "tool_input": {"command": "ls /home"},
>   "session_id": "test",
>   "cwd": "/tmp"
> }
> EOF
> fas explain provenance --config tests/diagnostics_rules_provenance --global-config /tmp/fas-nonexistent-global 2>&1
error[E0301]: leaf constraint failed
  --> tests/diagnostics_rules_provenance/provenance.cue:19:24
   |
19 |         tool_input: command: path.#systemInCommand
   |                              ^^^^^^^^^^^^^^^^^^^^^ got: "ls /home"
   |                              want: =~"(^|[^A-Za-z0-9_])/(etc|sys|proc|boot|dev)(/|$|[^A-Za-z0-9_])"
   = note: constraint introduced at /__fas_rules__/cue.mod/pkg/github.com/srnnkls/fas/cue/path/path.cue:42:1
[1]
```

## E0303 — kind mismatch

The `kind_mismatch` rule requires `tool_input.command: _int` (hidden-sibling
alias for `int`). A string input is kind-disjoint from `int`, so localize
short-circuits to `kindMismatchDiagnostic` and builds an E0303 with a
`KindMismatch` Reason. The caret row reads `want: <Want>, got: <Actual>` —
the actual literal carries its own kind, so the rendering drops the redundant
`<Got>` kind word and aligns with the `want:`/`got:` label pairs used by the
rest of the leaf-failure surface.

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
> fas explain kind-mismatch --config tests/diagnostics_rules_kind_mismatch --global-config /tmp/fas-nonexistent-global 2>&1
error[E0303]: type mismatch
  --> tests/diagnostics_rules_kind_mismatch/kind_mismatch.cue:13:24
   |
13 |         tool_input: command: _int
   |                              ^^^^ want: int, got: "ls"
[1]
```

## E0401 — disjunction, no arm close enough

The `disjunction` rule accepts `tool_name: "Read" | "Write" | "Edit"`.
Feeding `tool_name: "Bash"` ranks every arm below `ScoreKindMatch` (kind
matches but the Levenshtein distance drags every score under 100), so the
renderer takes the "no-close-arm" path: a flat primary message plus a
`= note: tried arms:` footer in rank order, with no per-arm caret frames.

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
> fas explain disjunction --config tests/diagnostics_rules --global-config /tmp/fas-nonexistent-global 2>&1
error[E0401]: no disjunction arm matched
  --> tests/diagnostics_rules/disjunction.cue:10:20
   |
10 |         tool_name:       "Read" | "Write" | "Edit"
   |                          ^^^^^^^^^^^^^^^^^^^^^^^^^ got: "Bash" — no arm was close
   = note: tried arms: "Read", "Edit", "Write"
[1]
```

## E0401 — disjunction, ranked arms (closest arm was X)

Same rule shape, but the input `tool_name: "Rea"` is Levenshtein 1 from
`"Read"`. The top arm's score lands at or above `ScoreKindMatch`, so the
renderer names the closest arm on the primary row and lists the runner-up
arms (rank order, descending score) in a `= note: other ranked arms:`
footer. Per-arm caret frames are not emitted: every arm is already visible
in the same source line the primary underlines, so a second caret pass
would point at locations the reader can already see. The data path still
carries full Span info per arm — JSON / SARIF surface it intact for tools
that want to inspect arm positions independently.

```scrut
$ cat << 'EOF' |
> {
>   "hook_event_name": "PreToolUse",
>   "tool_name": "Rea",
>   "tool_input": {"file_path": "/tmp/f"},
>   "session_id": "test",
>   "cwd": "/tmp"
> }
> EOF
> fas explain disjunction-close --config tests/diagnostics_rules_disjunction_close --global-config /tmp/fas-nonexistent-global 2>&1
error[E0401]: no disjunction arm matched
  --> tests/diagnostics_rules_disjunction_close/disjunction_close.cue:11:20
   |
11 |         tool_name:       "Read" | "Write" | "Edit"
   |                          ^^^^^^^^^^^^^^^^^^^^^^^^^ got: "Rea" — closest arm was "Read"
   = note: other ranked arms: "Edit", "Write"
[1]
```

## E0401 — disjunction via reference, ranked arms

Same shape as the literal close-arm case, but the disjunction lives in a
hidden-sibling definition (`_#ToolKind`) and the field references it.
`localize` follows the reference via `cue.Dereference`, finds the underlying
`OrOp` value, and ranks its arms — so the diagnostic fires E0401 with
`closest arm was X` on the primary row and a `= note: other ranked arms:`
footer, just like the literal case. Because the caret underlines the
reference name (not the arm body), an aligned `want:` row prints the
expanded disjunction so the reader can see what the field admits without
chasing the definition — matching the want:/got: rule that fires for any
reference constraint (E0301, E0303). The primary span underlines the
reference (`_#ToolKind`) at its use site; arm `Span`s in the data layer
point at the definition site (line 10 here), which JSON / SARIF surface
unchanged.

```scrut
$ cat << 'EOF' |
> {
>   "hook_event_name": "PreToolUse",
>   "tool_name": "Rea",
>   "tool_input": {"file_path": "/tmp/f"},
>   "session_id": "test",
>   "cwd": "/tmp"
> }
> EOF
> fas explain disjunction-ref --config tests/diagnostics_rules_disjunction_ref --global-config /tmp/fas-nonexistent-global 2>&1
error[E0401]: no disjunction arm matched
  --> tests/diagnostics_rules_disjunction_ref/disjunction_ref.cue:15:20
   |
15 |         tool_name:       _#ToolKind
   |                          ^^^^^^^^^^ got: "Rea" — closest arm was "Read"
   |                          want: "Read" | "Write" | "Edit"
   = note: other ranked arms: "Edit", "Write"
[1]
```

## E0501 — unbound identifier (load-time)

`tests/diagnostics_rules_broken_scope/unbound.cue` references `myUnknownVar`,
which resolves to none of a local binding, a stdlib import, or a curated
universe builtin. The loader rejects the directory with an E0501 diagnostic and
the CLI exits 1.

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
> fas eval --harness claude --config tests/diagnostics_rules_broken_scope --global-config /tmp/fas-nonexistent-global 2>&1
error[E0501]: rule "uid_rule": unbound identifier "myUnknownVar" in `when`
  --> tests/diagnostics_rules_broken_scope/unbound.cue:6:20
  |
6 |     when: {tool_name: myUnknownVar}
  |                       ^^^^^^^^^^^^ unbound identifier "myUnknownVar" in rule "uid_rule"
  |
  = help: Declare a hidden sibling (leading underscore, e.g. `_myUnknownVar: ...`) on the same rule, import the value from a stdlib package (e.g. `import "list"`), or use a curated universe builtin (`and`, `or`, `matchN`, `matchIf`, `len`). Bare identifiers in `when` must resolve to one of those scopes.

[1]
```

## E0502 — cross-rule reference (load-time)

`tests/diagnostics_rules_broken_cross/crossref.cue` contains two rules where
the second reaches into the first's `when` subtree. The loader rejects the
directory with an E0502 diagnostic and the CLI exits 1.

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
> fas eval --harness claude --config tests/diagnostics_rules_broken_cross --global-config /tmp/fas-nonexistent-global 2>&1
error[E0502]: rule "consumer_rule": cross-rule reference into "base_rule".when from `when`
  --> tests/diagnostics_rules_broken_cross/crossref.cue:14:20
   |
14 |     when: {tool_name: base_rule.when.tool_name}
   |                       ^^^^^^^^^ cross-rule reference from "consumer_rule" to "base_rule".when
   |
   = help: A rule references an identifier declared inside a different rule.

Rules are isolated units; cross-rule references would couple their
evaluation order and break composability. If the shared value is
genuinely common, extract it as a hidden sibling (leading
underscore) at the file top level where both rules can see it.

[1]
```

## E0506 — `let` clause in `when` (load-time)

`tests/diagnostics_rules_broken_let/let_in_when.cue` uses a `let` clause to
name an input-derived path inside `when`. The `let` binds the pattern's type
(e.g. `string`), not the input's concrete value, so any downstream constraint
silently misfires. The loader rejects the directory with an E0506 diagnostic
and the CLI exits 1.

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
> fas eval --harness claude --config tests/diagnostics_rules_broken_let --global-config /tmp/fas-nonexistent-global 2>&1
error[E0506]: rule "let_rule": `let` clause in `when` binds the pattern type, not the input value
  --> tests/diagnostics_rules_broken_let/let_in_when.cue:7:3
  |
7 |         let cmd = tool_input.command
  |         ^^^ `let` in `when` of rule "let_rule"
  |
  = help: A `let` clause inside `when` binds the pattern's type, not the input's value.

`let cmd = tool_input.command` names the `command` path inside the pattern,
which is a type constraint (e.g. `string`), not the concrete value from
the input. A downstream constraint like `cmd & =~"^git"` then matches
every input whose command is a string, not just git commands. Remove
the `let` and place the constraint directly on the field.

[1]
```

## E0507 — `if` comprehension in `when` (load-time)

`tests/diagnostics_rules_broken_if/if_in_when.cue` uses an `if` comprehension
to conditionally add constraints inside `when`. The guard evaluates against the
pattern's own types, not the input's values, so the gated fields either always
appear or never appear. The loader rejects the directory with an E0507
diagnostic and the CLI exits 1.

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
> fas eval --harness claude --config tests/diagnostics_rules_broken_if --global-config /tmp/fas-nonexistent-global 2>&1
error[E0507]: rule "if_rule": `if` guard in `when` evaluates against the pattern, not the input
  --> tests/diagnostics_rules_broken_if/if_in_when.cue:12:4
   |
12 |             if list.Contains(flags, "--force") {
   |             ^^ `if` guard in `when` of rule "if_rule"
   |
   = help: A comprehension (`if` or `for`) inside `when` evaluates against the pattern, not the input.

`if list.Contains(flags, "--force") { ... }` evaluates the guard against
the pattern's own `flags` field, which is a type (e.g. `[...string]`),
not a concrete list from the input. The guarded fields either always
appear or never appear, regardless of what the input carries. Remove
the comprehension and express the constraint directly as a field
pattern (e.g. `flags: list.MatchN(>0, =~"--force")`).

[1]
```

## `--explain=fired` emits only fired traces

With three rules and a Read payload, only `disjunction` fires (its disjunction
accepts `"Read"`). `--explain=fired` suppresses the miss diagnostics and prints
a single `fired:` trace line on stderr.

```scrut
$ cat << 'EOF' |
> {
>   "hook_event_name": "PreToolUse",
>   "tool_name": "Read",
>   "tool_input": {"file_path": "/tmp/f"},
>   "session_id": "test",
>   "cwd": "/tmp"
> }
> EOF
> fas eval --harness claude --config tests/diagnostics_rules --global-config /tmp/fas-nonexistent-global --explain=fired 2>&1
{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"wrong tool"}}fired: disjunction (tests/diagnostics_rules/disjunction.cue:disjunction)
```

## `--explain=both` emits fired traces and miss diagnostics

`--explain=both` combines the fired-trace lane and the missed-diagnostic lane;
the same Read payload shows one fired trace for `disjunction` plus the miss
diagnostics for the other two rules. Diagnostics appear in rule_id order.

```scrut
$ cat << 'EOF' |
> {
>   "hook_event_name": "PreToolUse",
>   "tool_name": "Read",
>   "tool_input": {"file_path": "/tmp/f"},
>   "session_id": "test",
>   "cwd": "/tmp"
> }
> EOF
> fas eval --harness claude --config tests/diagnostics_rules --global-config /tmp/fas-nonexistent-global --explain=both 2>&1
{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"wrong tool"}}fired: disjunction (tests/diagnostics_rules/disjunction.cue:disjunction)
rule_id: absent-path
error[E0301]: leaf constraint failed
  --> tests/diagnostics_rules/absent_path.cue:10:20
   |
10 |         tool_name:       "Bash"
   |                          ^^^^^^ got: "Read"
rule_id: absent-path
error[E0201]: key not found
  --> tests/diagnostics_rules/absent_path.cue:11:3
   |
11 |         signals: user_confirmed: true
   |         ^^^^^^^ key "signals" not found at <root>
   |
   = help: <root> has keys: cwd, hook_event_name, session_id, tool_input, tool_name
rule_id: leaf-regex
error[E0301]: leaf constraint failed
  --> tests/diagnostics_rules/leaf_regex.cue:11:20
   |
11 |         tool_name:       "Bash"
   |                          ^^^^^^ got: "Read"
rule_id: leaf-regex
error[E0201]: key not found
  --> tests/diagnostics_rules/leaf_regex.cue:12:15
   |
12 |         tool_input: command: =~"^rm "
   |                     ^^^^^^^ key "command" not found at tool_input
   |
   = help: tool_input has keys: file_path
```

## `--explain=missed` is the default; bare `--explain` and `FAS_EXPLAIN=1` mirror it

`--explain` with no value defaults to the `missed` filter — only non-firing
rules emit diagnostics, the firing `disjunction` is silent. The
`FAS_EXPLAIN=1` env var enables the same default without a flag (the
hook-debugging path). Both forms produce byte-identical output to an
explicit `--explain=missed`.

```scrut
$ cat << 'EOF' |
> {
>   "hook_event_name": "PreToolUse",
>   "tool_name": "Read",
>   "tool_input": {"file_path": "/tmp/f"},
>   "session_id": "test",
>   "cwd": "/tmp"
> }
> EOF
> fas eval --harness claude --config tests/diagnostics_rules --global-config /tmp/fas-nonexistent-global --explain 2>&1
{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"wrong tool"}}rule_id: absent-path
error[E0301]: leaf constraint failed
  --> tests/diagnostics_rules/absent_path.cue:10:20
   |
10 |         tool_name:       "Bash"
   |                          ^^^^^^ got: "Read"
rule_id: absent-path
error[E0201]: key not found
  --> tests/diagnostics_rules/absent_path.cue:11:3
   |
11 |         signals: user_confirmed: true
   |         ^^^^^^^ key "signals" not found at <root>
   |
   = help: <root> has keys: cwd, hook_event_name, session_id, tool_input, tool_name
rule_id: leaf-regex
error[E0301]: leaf constraint failed
  --> tests/diagnostics_rules/leaf_regex.cue:11:20
   |
11 |         tool_name:       "Bash"
   |                          ^^^^^^ got: "Read"
rule_id: leaf-regex
error[E0201]: key not found
  --> tests/diagnostics_rules/leaf_regex.cue:12:15
   |
12 |         tool_input: command: =~"^rm "
   |                     ^^^^^^^ key "command" not found at tool_input
   |
   = help: tool_input has keys: file_path
```

```scrut
$ cat << 'EOF' |
> {
>   "hook_event_name": "PreToolUse",
>   "tool_name": "Read",
>   "tool_input": {"file_path": "/tmp/f"},
>   "session_id": "test",
>   "cwd": "/tmp"
> }
> EOF
> FAS_EXPLAIN=1 fas eval --harness claude --config tests/diagnostics_rules --global-config /tmp/fas-nonexistent-global 2>&1
{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"wrong tool"}}rule_id: absent-path
error[E0301]: leaf constraint failed
  --> tests/diagnostics_rules/absent_path.cue:10:20
   |
10 |         tool_name:       "Bash"
   |                          ^^^^^^ got: "Read"
rule_id: absent-path
error[E0201]: key not found
  --> tests/diagnostics_rules/absent_path.cue:11:3
   |
11 |         signals: user_confirmed: true
   |         ^^^^^^^ key "signals" not found at <root>
   |
   = help: <root> has keys: cwd, hook_event_name, session_id, tool_input, tool_name
rule_id: leaf-regex
error[E0301]: leaf constraint failed
  --> tests/diagnostics_rules/leaf_regex.cue:11:20
   |
11 |         tool_name:       "Bash"
   |                          ^^^^^^ got: "Read"
rule_id: leaf-regex
error[E0201]: key not found
  --> tests/diagnostics_rules/leaf_regex.cue:12:15
   |
12 |         tool_input: command: =~"^rm "
   |                     ^^^^^^^ key "command" not found at tool_input
   |
   = help: tool_input has keys: file_path
```

## E0601 — binding variable mismatch

The `bind_eq` rule annotates `parsed.targets` with `@bind(Path, 0)` and
`@bind(Path, 1)`, declaring that the source (first target) must equal the
destination (second target) — a self-referencing copy/move. When the input
carries `mv src.go dst.go` (targets[0]="src.go", targets[1]="dst.go")
subsumption passes but the binding check fails, producing an E0601
diagnostic with `= note:` footers showing each path's resolved value.

```scrut
$ cat << 'EOF' |
> {
>   "hook_event_name": "PreToolUse",
>   "tool_name": "Bash",
>   "tool_input": {"command": "mv src.go dst.go"},
>   "session_id": "test",
>   "cwd": "/tmp"
> }
> EOF
> fas explain bind-eq --config tests/diagnostics_rules_bind --global-config /tmp/fas-nonexistent-global 2>&1
error[E0601]: binding variable mismatch
  --> tests/diagnostics_rules_bind/bind_eq.cue:10:44
   |
10 |         tool_input: parsed: targets: [...string] @bind(Path, 0) @bind(Path, 1)
   |                                                  ^^^^^^^^^^^ @bind(Path): values differ
   |
   = help: Fields annotated with the same @bind variable resolved to different values.

Two or more fields in `when` carry `@bind(X)` with the same variable
name. At match time, the concrete input values at those paths must be
equal (they must unify to the same point in the lattice). This diagnostic
fires when the input structurally matched the pattern but the bound values
diverged — e.g. `command` was `"cat"` while `targets[0]` was `"dog"`.
   = note: tool_input.parsed.targets[0] = "src.go"
   = note: tool_input.parsed.targets[1] = "dst.go"
[1]
```

When source and destination are the same (`mv src.go src.go`) the binding is
satisfied and the rule fires silently (exit 0, no diagnostic output).

```scrut
$ cat << 'EOF' |
> {
>   "hook_event_name": "PreToolUse",
>   "tool_name": "Bash",
>   "tool_input": {"command": "mv src.go src.go"},
>   "session_id": "test",
>   "cwd": "/tmp"
> }
> EOF
> fas explain bind-eq --config tests/diagnostics_rules_bind --global-config /tmp/fas-nonexistent-global 2>&1
```

## `fas explain` exit codes — match, no-match, unknown rule

Exit 0 when the rule fires, exit 1 when it does not (and a diagnostic is
emitted on stderr), exit 2 when the rule_id is unknown.

```scrut
$ cat << 'EOF' |
> {
>   "hook_event_name": "PreToolUse",
>   "tool_name": "Read",
>   "tool_input": {"file_path": "/tmp/f"},
>   "session_id": "test",
>   "cwd": "/tmp"
> }
> EOF
> fas explain disjunction --config tests/diagnostics_rules --global-config /tmp/fas-nonexistent-global 2>&1
```

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
> fas explain absent-path --config tests/diagnostics_rules --global-config /tmp/fas-nonexistent-global 2>&1
error[E0201]: key not found
  --> tests/diagnostics_rules/absent_path.cue:11:3
   |
11 |         signals: user_confirmed: true
   |         ^^^^^^^ key "signals" not found at <root>
   |
   = help: <root> has keys: cwd, hook_event_name, session_id, tool_input, tool_name
[1]
```

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
> fas explain nonexistent-rule --config tests/diagnostics_rules --global-config /tmp/fas-nonexistent-global 2>&1
rule_id "nonexistent-rule" not found in project or global rules
[2]
```

## `fas explain --code E0201` prints help; unknown code exits 2

The `--code <code>` fast path prints the registered help for a known error
code to stdout and exits 0. An unknown code exits 2 with a stderr diagnostic.

```scrut
$ fas explain --code E0201
E0201

A path segment referenced in the rule does not exist in the input.

Under closed-world pattern-match semantics, every path referenced in
`when` must exist in the input for the rule to match. Absent paths
cause the rule to silently not fire; the diagnostic shows which
segment broke the chain.
```

```scrut
$ fas explain --code EXXXX 2>&1
unknown error code "EXXXX"
[2]
```
