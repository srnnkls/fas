# Quae Diagnostics Tests

End-to-end integration tests for the diagnostic surface of the `quae` CLI,
using [scrut](https://github.com/facebookincubator/scrut). Each block drives
a specific diagnostic code, `--explain` filter mode, or `quae explain`
subcommand exit path and asserts the exact stdout+stderr the binary emits.

Run with:
```bash
scrut test -w . tests/diagnostics.md
```

Fixture rules live under `tests/diagnostics_rules/` (well-formed rules that
fail to match a chosen input, producing miss diagnostics) and two sibling
directories for load-time errors:

- `tests/diagnostics_rules_broken_scope/` — unbound identifier (E0501)
- `tests/diagnostics_rules_broken_cross/` — cross-rule reference (E0502)

Each block redirects stderr into stdout (`2>&1`) so scrut — which only
captures stdout by default — sees the diagnostic stream. The
`--global-config /tmp/quae-nonexistent-global` trick isolates the suite from
host-global rules.

## E0201 — absent path segment

The `absent_path` rule demands `signals.user_confirmed: true`; a Bash payload
that omits `signals` entirely produces an E0201 carat at `signals` and a
help line listing the keys the input actually exposes at that level.

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
11 | \t\tsignals: user_confirmed: true (escaped)
   |   ^^^^^^^ key "signals" not found in input at path <root>
   |
   = help: input.<root> has keys: cwd, hook_event_name, session_id, tool_input, tool_name
[1]
```

## E0301 — leaf constraint failure (regex)

The `leaf_regex` rule pins `tool_input.command: =~"^rm "`. An `ls` command
surfaces the leaf miss with `want:` / `got:` notes under a single caret span.

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
12 | \t\ttool_input: command: =~"^rm " (escaped)
   |                        ^^^^^^^^ constraint not satisfied
   |
12 | \t\ttool_input: command: =~"^rm " (escaped)
   |                        ^^^^^^^^ want: =~"^rm "
   |
12 | \t\ttool_input: command: =~"^rm " (escaped)
   |                        ^^^^^^^^ got: "ls"
[1]
```

## E0401 — disjunction all-arms-fail

The `disjunction` rule accepts `tool_name: "Read" | "Write" | "Edit"`. Feeding
`tool_name: "Bash"` rejects every arm; the diagnostic highlights the full span
and adds one Note per arm.

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
10 | \t\ttool_name:       "Read" | "Write" | "Edit" (escaped)
   |                    ^^^^^^^^^^^^^^^^^^^^^^^^^ no arm subsumes "Bash"
   |
10 | \t\ttool_name:       "Read" | "Write" | "Edit" (escaped)
   |                    ^^^^^^ arm "Read" did not match
   |
10 | \t\ttool_name:       "Read" | "Write" | "Edit" (escaped)
   |                             ^^^^^^^ arm "Write" did not match
   |
10 | \t\ttool_name:       "Read" | "Write" | "Edit" (escaped)
   |                                       ^^^^^^ arm "Edit" did not match
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
6 | \twhen: {tool_name: myUnknownVar} (escaped)
  |                    ^^^^^^^^^^^^ unbound identifier "myUnknownVar" in rule "uid_rule"
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
14 | \twhen: {tool_name: base_rule.when.tool_name} (escaped)
   |                    ^^^^^^^^^ cross-rule reference from "consumer_rule" to "base_rule".when
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
diagnostics for the other two rules.

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
10 | \t\ttool_name:       "Bash" (escaped)
   |                    ^^^^^^ constraint not satisfied
   |
10 | \t\ttool_name:       "Bash" (escaped)
   |                    ^^^^^^ want: "Bash"
   |
10 | \t\ttool_name:       "Bash" (escaped)
   |                    ^^^^^^ got: "Read"
rule_id: absent-path
error[E0201]: key not found
  --> /__quae_rules__/absent_path.cue:11:3
   |
11 | \t\tsignals: user_confirmed: true (escaped)
   |   ^^^^^^^ key "signals" not found in input at path <root>
   |
   = help: input.<root> has keys: cwd, hook_event_name, session_id, tool_input, tool_name
rule_id: leaf-regex
error[E0301]: leaf constraint failed
  --> /__quae_rules__/leaf_regex.cue:11:20
   |
11 | \t\ttool_name:       "Bash" (escaped)
   |                    ^^^^^^ constraint not satisfied
   |
11 | \t\ttool_name:       "Bash" (escaped)
   |                    ^^^^^^ want: "Bash"
   |
11 | \t\ttool_name:       "Bash" (escaped)
   |                    ^^^^^^ got: "Read"
rule_id: leaf-regex
error[E0201]: key not found
  --> /__quae_rules__/leaf_regex.cue:12:15
   |
12 | \t\ttool_input: command: =~"^rm " (escaped)
   |               ^^^^^^^ key "command" not found in input at path tool_input
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
