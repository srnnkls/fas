# Quae Diagnostics Tests

End-to-end integration tests for the diagnostic surface of the `quae` CLI,
using [scrut](https://github.com/facebookincubator/scrut). Each block drives
a specific diagnostic code, `--explain` filter mode, or `quae explain`
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
- `tests/diagnostics_rules_broken_scope/` — load-time E0501.
- `tests/diagnostics_rules_broken_cross/` — load-time E0502.

Each block redirects stderr into stdout (`2>&1`) so scrut — which only
captures stdout by default — sees the diagnostic stream. The
`--global-config /tmp/quae-nonexistent-global` trick isolates the suite from
host-global rules.

The minimal-form rules documented in scope.md F7-F12 apply throughout:

- No "constraint not satisfied" restatement — the Title alone names the
  failure class.
- Conditional `want:` — emitted only when the caret span is not already the
  literal constraint (the "cheap/strong gate" from F7; see
  `feedback_diag_no_restate.md`).
- Per-Reason text formatting — `KindMismatch` → `expected X, got Y: Z`;
  `BoundViolation` → `V violates op B (off by N)`;
  `DisjunctionFailed` no-close-arm → `got V — no arm was close` + `= note:
  tried arms:`.
- Same-span label collapse — the source line prints once, subsequent
  messages stack aligned under the same caret column.
- `KeyMissing.Suggestion` non-empty → `= hint: did you mean "X"?` footer.

### Deferred

- **Provenance footer in text output.** `provenanceNotes` walks cross-file
  conjuncts and emits `Provenance` Labels with an invalid `Pos` (the Span
  is the payload). `orderedLabels` drops labels whose `Pos` doesn't
  resolve via `SourceCache.LineAt`, so the Text renderer currently skips
  these entries even though `--format=json` surfaces them intact. Until
  the renderer grows a dedicated Provenance fold that bypasses `LineAt`,
  a Provenance golden cannot match byte-for-byte. `tests/diagnostics_rules_*`
  still exercises the data path via the non-text format tests in the
  future `tests/diagnostics_formats.md` (T18). Target footer shape per
  scope.md Ex 5: `= note: constraint introduced at <file:line:col>`.

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
> quae explain absent-path --config tests/diagnostics_rules --global-config /tmp/quae-nonexistent-global 2>&1
error[E0201]: key not found
  --> /__quae_rules__/absent_path.cue:11:3
   |
11 |         signals: user_confirmed: true
   |         ^^^^^^^ key "signals" not found in input at path <root>
   |
   = help: input.<root> has keys: cwd, hook_event_name, session_id, tool_input, tool_name
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
> quae explain key-missing-hint --config tests/diagnostics_rules_key_missing_hint --global-config /tmp/quae-nonexistent-global 2>&1
error[E0201]: key not found
  --> /__quae_rules__/key_missing_hint.cue:10:15
   |
10 |         tool_input: command: "ls"
   |                     ^^^^^^^ key "command" not found in input at path tool_input
   |
   = help: input.tool_input has keys: commnd, parsed
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
> quae explain leaf-regex --config tests/diagnostics_rules --global-config /tmp/quae-nonexistent-global 2>&1
error[E0301]: leaf constraint failed
  --> /__quae_rules__/leaf_regex.cue:12:24
   |
12 |         tool_input: command: =~"^rm "
   |                              ^^^^^^^^
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
> quae explain bound-violation --config tests/diagnostics_rules_bound_violation --global-config /tmp/quae-nonexistent-global 2>&1
error[E0301]: leaf constraint failed
  --> /__quae_rules__/bound_violation.cue:11:35
   |
11 |         tool_input: retry_count: _int & <=10
   |                                         ^^^^ 12 violates <= 10 (off by 2)
   |
11 |         tool_input: retry_count: _int & <=10
   |                                  ^^^^^^^^^^^ want: int & <=10
[1]
```

## E0303 — kind mismatch

The `kind_mismatch` rule requires `tool_input.command: _int` (hidden-sibling
alias for `int`). A string input is kind-disjoint from `int`, so localize
short-circuits to `kindMismatchDiagnostic` and builds an E0303 with a
`KindMismatch` Reason. The caret row reads `expected <Want>, got <Got>:
<Actual>` per scope.md Ex 5.

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
> quae explain kind-mismatch --config tests/diagnostics_rules_kind_mismatch --global-config /tmp/quae-nonexistent-global 2>&1
error[E0303]: type mismatch
  --> /__quae_rules__/kind_mismatch.cue:13:24
   |
13 |         tool_input: command: _int
   |                              ^^^^ expected int, got string: "ls"
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
> quae explain disjunction --config tests/diagnostics_rules --global-config /tmp/quae-nonexistent-global 2>&1
error[E0401]: no disjunction arm matched
  --> /__quae_rules__/disjunction.cue:10:20
   |
10 |         tool_name:       "Read" | "Write" | "Edit"
   |                          ^^^^^^^^^^^^^^^^^^^^^^^^^ got "Bash" — no arm was close
   = note: tried arms: "Read", "Edit", "Write"
[1]
```

## E0401 — disjunction, ranked arms (closest arm was X)

Same rule shape, but the input `tool_name: "Rea"` is Levenshtein 1 from
`"Read"`. The top arm's score lands at or above `ScoreKindMatch`, so the
renderer names the closest arm on the primary row and emits one secondary
caret frame per arm (sorted by score descending).

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
> quae explain disjunction-close --config tests/diagnostics_rules_disjunction_close --global-config /tmp/quae-nonexistent-global 2>&1
error[E0401]: no disjunction arm matched
  --> /__quae_rules__/disjunction_close.cue:10:20
   |
10 |         tool_name:       "Read" | "Write" | "Edit"
   |                          ^^^^^^^^^^^^^^^^^^^^^^^^^ got "Rea" — closest arm was "Read"
   |
10 | 
   |                    ^^^^^^ "Read"
   |
10 | 
   |                                       ^^^^^^ "Edit"
   |
10 | 
   |                             ^^^^^^^ "Write"
[1]
```

## E0501 — unbound identifier (load-time)

`tests/diagnostics_rules_broken_scope/unbound.cue` references `myUnknownVar`,
which resolves to neither a local binding nor a stdlib import. The loader
rejects the directory with an E0501 diagnostic and the CLI exits 1.

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
> quae eval --harness claude --config tests/diagnostics_rules_broken_scope --global-config /tmp/quae-nonexistent-global 2>&1
error[E0501]: rule "uid_rule": unbound identifier "myUnknownVar" in `when`
  --> tests/diagnostics_rules_broken_scope/unbound.cue:6:20
  |
6 |     when: {tool_name: myUnknownVar}
  |                       ^^^^^^^^^^^^ unbound identifier "myUnknownVar" in rule "uid_rule"
  |
  = help: Declare a hidden sibling (leading underscore, e.g. `_myUnknownVar: ...`) on the same rule, or import the value from a stdlib package (e.g. `import "list"`). Bare identifiers in `when` must resolve to one of those two scopes.

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
> quae eval --harness claude --config tests/diagnostics_rules_broken_cross --global-config /tmp/quae-nonexistent-global 2>&1
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
> quae eval --harness claude --config tests/diagnostics_rules --global-config /tmp/quae-nonexistent-global --explain=fired 2>&1
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
> quae eval --harness claude --config tests/diagnostics_rules --global-config /tmp/quae-nonexistent-global --explain=both 2>&1
{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"wrong tool"}}fired: disjunction (tests/diagnostics_rules/disjunction.cue:disjunction)
rule_id: absent-path
error[E0301]: leaf constraint failed
  --> /__quae_rules__/absent_path.cue:10:20
   |
10 |         tool_name:       "Bash"
   |                          ^^^^^^
rule_id: absent-path
error[E0201]: key not found
  --> /__quae_rules__/absent_path.cue:11:3
   |
11 |         signals: user_confirmed: true
   |         ^^^^^^^ key "signals" not found in input at path <root>
   |
   = help: input.<root> has keys: cwd, hook_event_name, session_id, tool_input, tool_name
rule_id: leaf-regex
error[E0301]: leaf constraint failed
  --> /__quae_rules__/leaf_regex.cue:11:20
   |
11 |         tool_name:       "Bash"
   |                          ^^^^^^
rule_id: leaf-regex
error[E0201]: key not found
  --> /__quae_rules__/leaf_regex.cue:12:15
   |
12 |         tool_input: command: =~"^rm "
   |                     ^^^^^^^ key "command" not found in input at path tool_input
   |
   = help: input.tool_input has keys: file_path
```

## `quae explain` exit codes — match, no-match, unknown rule

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
> quae explain disjunction --config tests/diagnostics_rules --global-config /tmp/quae-nonexistent-global 2>&1
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
> quae explain nonexistent-rule --config tests/diagnostics_rules --global-config /tmp/quae-nonexistent-global 2>&1
rule_id "nonexistent-rule" not found in project or global rules
[2]
```

## `quae explain --code E0201` prints help; unknown code exits 2

The `--code <code>` fast path prints the registered help for a known error
code to stdout and exits 0. An unknown code exits 2 with a stderr diagnostic.

```scrut
$ quae explain --code E0201
E0201

A path segment referenced in the rule does not exist in the input.

Under closed-world pattern-match semantics, every path referenced in
`when` must exist in the input for the rule to match. Absent paths
cause the rule to silently not fire; the diagnostic shows which
segment broke the chain.
```

```scrut
$ quae explain --code EXXXX 2>&1
unknown error code "EXXXX"
[2]
```
