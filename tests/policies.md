# Quae Policy Tests

End-to-end integration tests for the `quae eval` CLI using [scrut](https://github.com/facebookincubator/scrut).
Each block pipes a Claude Code hook event into `quae eval --harness claude` and asserts the exact response JSON.

Run with:
```bash
scrut test -w . tests/policies.md
```

The `--global-config /tmp/quae-nonexistent-global` flag points at a path that does not exist so host-level
rules never leak into the suite; only rules under `tests/policies/` participate.

## System Path Protection

Critical read-only system directories (`/etc`, `/sys`, `/proc`, `/boot`, `/dev`) are protected
against every Bash operation regardless of verb. Reads, deletes, writes, or any reference to
these prefixes in a command's targets denies the tool call.

### Blocks rm -rf /etc/passwd

```scrut
$ cat << 'EOF' |
> {
>   "hook_event_name": "PreToolUse",
>   "tool_name": "Bash",
>   "tool_input": {"command": "rm -rf /etc/passwd"},
>   "session_id": "test",
>   "cwd": "/tmp"
> }
> EOF
> quae eval --harness claude --config tests/policies --global-config /tmp/quae-nonexistent-global 2>/dev/null
{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"System path blocked"}} (no-eol)
```

### Blocks cat /etc/shadow

```scrut
$ cat << 'EOF' |
> {
>   "hook_event_name": "PreToolUse",
>   "tool_name": "Bash",
>   "tool_input": {"command": "cat /etc/shadow"},
>   "session_id": "test",
>   "cwd": "/tmp"
> }
> EOF
> quae eval --harness claude --config tests/policies --global-config /tmp/quae-nonexistent-global 2>/dev/null
{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"System path blocked"}} (no-eol)
```

### Blocks rm -rf /sys/power

```scrut
$ cat << 'EOF' |
> {
>   "hook_event_name": "PreToolUse",
>   "tool_name": "Bash",
>   "tool_input": {"command": "rm -rf /sys/power"},
>   "session_id": "test",
>   "cwd": "/tmp"
> }
> EOF
> quae eval --harness claude --config tests/policies --global-config /tmp/quae-nonexistent-global 2>/dev/null
{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"System path blocked"}} (no-eol)
```

### Allows rm -rf ./build (relative path outside system prefixes)

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
> quae eval --harness claude --config tests/policies --global-config /tmp/quae-nonexistent-global 2>/dev/null
{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"allow"}} (no-eol)
```

## Git --no-verify

`git commit --no-verify`, `git push --no-verify`, and the short form `-n` all bypass
commit/push hooks. Policy denies these so repository hygiene checks always run.

### Blocks git commit --no-verify

```scrut
$ cat << 'EOF' |
> {
>   "hook_event_name": "PreToolUse",
>   "tool_name": "Bash",
>   "tool_input": {"command": "git commit --no-verify -m test"},
>   "session_id": "test",
>   "cwd": "/tmp"
> }
> EOF
> quae eval --harness claude --config tests/policies --global-config /tmp/quae-nonexistent-global 2>/dev/null
{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"Git --no-verify is not permitted; commit/push hooks must run"}} (no-eol)
```

### Blocks git push --no-verify

```scrut
$ cat << 'EOF' |
> {
>   "hook_event_name": "PreToolUse",
>   "tool_name": "Bash",
>   "tool_input": {"command": "git push --no-verify"},
>   "session_id": "test",
>   "cwd": "/tmp"
> }
> EOF
> quae eval --harness claude --config tests/policies --global-config /tmp/quae-nonexistent-global 2>/dev/null
{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"Git --no-verify is not permitted; commit/push hooks must run"}} (no-eol)
```

### Blocks git commit -n (short form of --no-verify)

```scrut
$ cat << 'EOF' |
> {
>   "hook_event_name": "PreToolUse",
>   "tool_name": "Bash",
>   "tool_input": {"command": "git commit -n -m test"},
>   "session_id": "test",
>   "cwd": "/tmp"
> }
> EOF
> quae eval --harness claude --config tests/policies --global-config /tmp/quae-nonexistent-global 2>/dev/null
{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"Git --no-verify is not permitted; commit/push hooks must run"}} (no-eol)
```

### Allows normal git commit

```scrut
$ cat << 'EOF' |
> {
>   "hook_event_name": "PreToolUse",
>   "tool_name": "Bash",
>   "tool_input": {"command": "git commit -m \"normal\""},
>   "session_id": "test",
>   "cwd": "/tmp"
> }
> EOF
> quae eval --harness claude --config tests/policies --global-config /tmp/quae-nonexistent-global 2>/dev/null
{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"allow"}} (no-eol)
```

## Destructive Home-Directory Commands

`rm -rf $HOME` and `rm -rf ~` are near-certain mistakes. Policy denies them.
Scoped recursive deletes inside a project tree (`./node_modules`) remain allowed.

### Blocks rm -rf $HOME

```scrut
$ cat << 'EOF' |
> {
>   "hook_event_name": "PreToolUse",
>   "tool_name": "Bash",
>   "tool_input": {"command": "rm -rf $HOME"},
>   "session_id": "test",
>   "cwd": "/tmp"
> }
> EOF
> quae eval --harness claude --config tests/policies --global-config /tmp/quae-nonexistent-global 2>/dev/null
{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"Recursive deletion of the home directory is blocked"}} (no-eol)
```

### Blocks rm -rf ~

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
> quae eval --harness claude --config tests/policies --global-config /tmp/quae-nonexistent-global 2>/dev/null
{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"Recursive deletion of the home directory is blocked"}} (no-eol)
```

### Allows rm -rf ./node_modules

```scrut
$ cat << 'EOF' |
> {
>   "hook_event_name": "PreToolUse",
>   "tool_name": "Bash",
>   "tool_input": {"command": "rm -rf ./node_modules"},
>   "session_id": "test",
>   "cwd": "/tmp"
> }
> EOF
> quae eval --harness claude --config tests/policies --global-config /tmp/quae-nonexistent-global 2>/dev/null
{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"allow"}} (no-eol)
```

## Secret Files

Staging `.env`, credential JSON, or SSH private keys into git is almost always a mistake.
Policy denies these `git add` invocations; staging ordinary source files remains allowed.

### Blocks git add .env

```scrut
$ cat << 'EOF' |
> {
>   "hook_event_name": "PreToolUse",
>   "tool_name": "Bash",
>   "tool_input": {"command": "git add .env"},
>   "session_id": "test",
>   "cwd": "/tmp"
> }
> EOF
> quae eval --harness claude --config tests/policies --global-config /tmp/quae-nonexistent-global 2>/dev/null
{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"Refusing to stage a likely secret file"}} (no-eol)
```

### Blocks git add credentials.json

```scrut
$ cat << 'EOF' |
> {
>   "hook_event_name": "PreToolUse",
>   "tool_name": "Bash",
>   "tool_input": {"command": "git add credentials.json"},
>   "session_id": "test",
>   "cwd": "/tmp"
> }
> EOF
> quae eval --harness claude --config tests/policies --global-config /tmp/quae-nonexistent-global 2>/dev/null
{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"Refusing to stage a likely secret file"}} (no-eol)
```

### Blocks git add id_rsa

```scrut
$ cat << 'EOF' |
> {
>   "hook_event_name": "PreToolUse",
>   "tool_name": "Bash",
>   "tool_input": {"command": "git add id_rsa"},
>   "session_id": "test",
>   "cwd": "/tmp"
> }
> EOF
> quae eval --harness claude --config tests/policies --global-config /tmp/quae-nonexistent-global 2>/dev/null
{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"Refusing to stage a likely secret file"}} (no-eol)
```

### Allows git add src/main.py

```scrut
$ cat << 'EOF' |
> {
>   "hook_event_name": "PreToolUse",
>   "tool_name": "Bash",
>   "tool_input": {"command": "git add src/main.py"},
>   "session_id": "test",
>   "cwd": "/tmp"
> }
> EOF
> quae eval --harness claude --config tests/policies --global-config /tmp/quae-nonexistent-global 2>/dev/null
{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"allow"}} (no-eol)
```

## Compound Commands (AST regression guard)

These exercise T4's AST walk: policies must inspect every simple-command inside
`&&`, `||`, `;`, pipelines, and `for` loops — not just the first command token.

### Blocks echo start && rm -rf /etc/passwd

```scrut
$ cat << 'EOF' |
> {
>   "hook_event_name": "PreToolUse",
>   "tool_name": "Bash",
>   "tool_input": {"command": "echo start && rm -rf /etc/passwd"},
>   "session_id": "test",
>   "cwd": "/tmp"
> }
> EOF
> quae eval --harness claude --config tests/policies --global-config /tmp/quae-nonexistent-global 2>/dev/null
{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"System path blocked"}} (no-eol)
```

### Blocks for-loop rm under /etc

```scrut
$ cat << 'EOF' |
> {
>   "hook_event_name": "PreToolUse",
>   "tool_name": "Bash",
>   "tool_input": {"command": "for f in /etc/*.conf; do rm $f; done"},
>   "session_id": "test",
>   "cwd": "/tmp"
> }
> EOF
> quae eval --harness claude --config tests/policies --global-config /tmp/quae-nonexistent-global 2>/dev/null
{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"System path blocked"}} (no-eol)
```

### Allows npm install && npm test

```scrut
$ cat << 'EOF' |
> {
>   "hook_event_name": "PreToolUse",
>   "tool_name": "Bash",
>   "tool_input": {"command": "npm install && npm test"},
>   "session_id": "test",
>   "cwd": "/tmp"
> }
> EOF
> quae eval --harness claude --config tests/policies --global-config /tmp/quae-nonexistent-global 2>/dev/null
{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"allow"}} (no-eol)
```

## Safe Commands

Plain, non-destructive invocations that every policy set must allow.

### Allows echo hello

```scrut
$ cat << 'EOF' |
> {
>   "hook_event_name": "PreToolUse",
>   "tool_name": "Bash",
>   "tool_input": {"command": "echo hello"},
>   "session_id": "test",
>   "cwd": "/tmp"
> }
> EOF
> quae eval --harness claude --config tests/policies --global-config /tmp/quae-nonexistent-global 2>/dev/null
{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"allow"}} (no-eol)
```

### Allows ls -la

```scrut
$ cat << 'EOF' |
> {
>   "hook_event_name": "PreToolUse",
>   "tool_name": "Bash",
>   "tool_input": {"command": "ls -la"},
>   "session_id": "test",
>   "cwd": "/tmp"
> }
> EOF
> quae eval --harness claude --config tests/policies --global-config /tmp/quae-nonexistent-global 2>/dev/null
{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"allow"}} (no-eol)
```

### Allows cat README.md

```scrut
$ cat << 'EOF' |
> {
>   "hook_event_name": "PreToolUse",
>   "tool_name": "Bash",
>   "tool_input": {"command": "cat README.md"},
>   "session_id": "test",
>   "cwd": "/tmp"
> }
> EOF
> quae eval --harness claude --config tests/policies --global-config /tmp/quae-nonexistent-global 2>/dev/null
{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"allow"}} (no-eol)
```
