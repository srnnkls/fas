# Fas Guide Tests

End-to-end integration tests for the worked examples in [GUIDE.md](../GUIDE.md),
using [scrut](https://github.com/facebookincubator/scrut). Each block runs a
command exactly as the guide shows it and asserts the exact output, so the guide
cannot drift from the binary.

Run with:
```bash
scrut test -w . tests/guide.md
```

Fixtures live in two dirs so the layering examples have a real global layer:

- `tests/guide_rules/` — the project layer: `no-rm-home`, `tee-system`,
  `webfetch-reminder`, `confirm-force-push`.
- `tests/guide_rules_global/` — the global layer: `audit-bash` (inject on every
  Bash) and `global-no-force-add` (deny `git add --force`).

Blocks that exercise a single layer point `--global-config` at a non-existent
path so host-level rules never leak in.

## Your first rule

`fas vet` loads and validates the project layer without reading stdin.

```scrut
$ fas vet --config tests/guide_rules --global-config /tmp/fas-nonexistent-global
ok: 4 rules loaded (global: 0, project: 4)
  project: tee-system
  project: webfetch-reminder
  project: confirm-force-push
  project: no-rm-home
```

The `no_rm_home` rule denies a recursive delete of the home directory.

```scrut
$ cat << 'EOF' |
> {
>   "hook_event_name": "PreToolUse",
>   "tool_name": "Bash",
>   "tool_input": {"command": "rm -rf ~"},
>   "session_id": "test",
>   "cwd": "/tmp"
> }
> EOF
> fas eval --harness claude --config tests/guide_rules --global-config /tmp/fas-nonexistent-global 2>/dev/null
{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"Recursive deletion of the home directory is blocked"}} (no-eol)
```

A relative path never matches the `~|$HOME` target constraint, so the call falls
through to allow.

```scrut
$ cat << 'EOF' |
> {
>   "hook_event_name": "PreToolUse",
>   "tool_name": "Bash",
>   "tool_input": {"command": "rm -rf ./build"},
>   "session_id": "test",
>   "cwd": "/tmp"
> }
> EOF
> fas eval --harness claude --config tests/guide_rules --global-config /tmp/fas-nonexistent-global 2>/dev/null
{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"allow"}} (no-eol)
```

## Composing from parsed facts

`bash.#command & {#name: "tee"}` reads `parsed.commands`, so it matches through a
`sudo` prefix; `path.#hasSystemInCommand` matches the system path in the command
line. CRITICAL severity rides through to the reason.

```scrut
$ cat << 'EOF' |
> {
>   "hook_event_name": "PreToolUse",
>   "tool_name": "Bash",
>   "tool_input": {"command": "sudo tee /etc/hosts"},
>   "session_id": "test",
>   "cwd": "/tmp"
> }
> EOF
> fas eval --harness claude --config tests/guide_rules --global-config /tmp/fas-nonexistent-global 2>/dev/null
{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"Writing to system paths via tee is blocked"}} (no-eol)
```

## Effects: inject and ask

An `inject` on an otherwise-unremarkable call produces an allow that carries the
injected `additionalContext`.

```scrut
$ cat << 'EOF' |
> {
>   "hook_event_name": "PreToolUse",
>   "tool_name": "WebFetch",
>   "tool_input": {"url": "https://x"},
>   "session_id": "test",
>   "cwd": "/tmp"
> }
> EOF
> fas eval --harness claude --config tests/guide_rules --global-config /tmp/fas-nonexistent-global 2>/dev/null
{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"allow","additionalContext":"Prefer the local docs cache before fetching the network."}} (no-eol)
```

An `ask` gate routes the call to a human; reason and question join with a
newline in the decision reason.

```scrut
$ cat << 'EOF' |
> {
>   "hook_event_name": "PreToolUse",
>   "tool_name": "Bash",
>   "tool_input": {"command": "git push --force origin main"},
>   "session_id": "test",
>   "cwd": "/tmp"
> }
> EOF
> fas eval --harness claude --config tests/guide_rules --global-config /tmp/fas-nonexistent-global 2>/dev/null
{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"ask","permissionDecisionReason":"Force-push rewrites remote history.\nForce-push this branch?"}} (no-eol)
```

## Layering: global and project

A non-blocking global `inject` lets the project layer run, so the global
`additionalContext` and the project `deny` both appear in the output.

```scrut
$ cat << 'EOF' |
> {
>   "hook_event_name": "PreToolUse",
>   "tool_name": "Bash",
>   "tool_input": {"command": "rm -rf ~"},
>   "session_id": "test",
>   "cwd": "/tmp"
> }
> EOF
> fas eval --harness claude --config tests/guide_rules --global-config tests/guide_rules_global 2>/dev/null
{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"Recursive deletion of the home directory is blocked","additionalContext":"Bash call audited by the global policy layer."}} (no-eol)
```

A blocking global `deny` short-circuits the pipeline — the project layer never
runs — while the phase-one global `inject` still rides along.

```scrut
$ cat << 'EOF' |
> {
>   "hook_event_name": "PreToolUse",
>   "tool_name": "Bash",
>   "tool_input": {"command": "git add --force secret.env"},
>   "session_id": "test",
>   "cwd": "/tmp"
> }
> EOF
> fas eval --harness claude --config tests/guide_rules --global-config tests/guide_rules_global 2>/dev/null
{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"git add --force is blocked by the global policy layer","additionalContext":"Bash call audited by the global policy layer."}} (no-eol)
```

A plain command matches only the global `audit-bash` inject, so it allows with
just the audit context.

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
> fas eval --harness claude --config tests/guide_rules --global-config tests/guide_rules_global 2>/dev/null
{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"allow","additionalContext":"Bash call audited by the global policy layer."}} (no-eol)
```

## Debugging

`fas explain --code` prints the registered help for an error code and exits 0.

```scrut
$ fas explain --code E0201
E0201

A path segment referenced in the rule does not exist in the input.

Under closed-world pattern-match semantics, every path referenced in
`when` must exist in the input for the rule to match. Absent paths
cause the rule to silently not fire; the diagnostic shows which
segment broke the chain.
```

`fas explain <rule_id>` exits 0 when the rule fires.

```scrut
$ cat << 'EOF' |
> {
>   "hook_event_name": "PreToolUse",
>   "tool_name": "Bash",
>   "tool_input": {"command": "rm -rf ~"},
>   "session_id": "test",
>   "cwd": "/tmp"
> }
> EOF
> fas explain no-rm-home --config tests/guide_rules --global-config /tmp/fas-nonexistent-global 2>/dev/null
```

It exits 1 when the rule does not fire.

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
> fas explain no-rm-home --config tests/guide_rules --global-config /tmp/fas-nonexistent-global 2>/dev/null
[1]
```
