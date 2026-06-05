# Fas Policy Tests

End-to-end integration tests for the `fas eval` CLI using [scrut](https://github.com/facebookincubator/scrut).
Each block pipes a Claude Code hook event into `fas eval --harness claude` and asserts the exact response JSON.

Run with:
```bash
scrut test -w . tests/policies.md
```

The `--global-config /tmp/fas-nonexistent-global` flag points at a path that does not exist so host-level
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
> fas eval --harness claude --config tests/policies --global-config /tmp/fas-nonexistent-global 2>/dev/null
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
> fas eval --harness claude --config tests/policies --global-config /tmp/fas-nonexistent-global 2>/dev/null
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
> fas eval --harness claude --config tests/policies --global-config /tmp/fas-nonexistent-global 2>/dev/null
{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"System path blocked"}} (no-eol)
```

### Allows rm -rf /devops (prefix is not a complete path component)

```scrut
$ cat << 'EOF' |
> {
>   "hook_event_name": "PreToolUse",
>   "tool_name": "Bash",
>   "tool_input": {"command": "rm -rf /devops"},
>   "session_id": "test",
>   "cwd": "/tmp"
> }
> EOF
> fas eval --harness claude --config tests/policies --global-config /tmp/fas-nonexistent-global 2>/dev/null
{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"allow"}} (no-eol)
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
> fas eval --harness claude --config tests/policies --global-config /tmp/fas-nonexistent-global 2>/dev/null
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
> fas eval --harness claude --config tests/policies --global-config /tmp/fas-nonexistent-global 2>/dev/null
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
> fas eval --harness claude --config tests/policies --global-config /tmp/fas-nonexistent-global 2>/dev/null
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
> fas eval --harness claude --config tests/policies --global-config /tmp/fas-nonexistent-global 2>/dev/null
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
> fas eval --harness claude --config tests/policies --global-config /tmp/fas-nonexistent-global 2>/dev/null
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
> fas eval --harness claude --config tests/policies --global-config /tmp/fas-nonexistent-global 2>/dev/null
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
> fas eval --harness claude --config tests/policies --global-config /tmp/fas-nonexistent-global 2>/dev/null
{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"Recursive deletion of the home directory is blocked"}} (no-eol)
```

### Blocks rm --recursive=true ~

```scrut
$ cat << 'EOF' |
> {
>   "hook_event_name": "PreToolUse",
>   "tool_name": "Bash",
>   "tool_input": {"command": "rm --recursive=true ~"},
>   "session_id": "test",
>   "cwd": "/tmp"
> }
> EOF
> fas eval --harness claude --config tests/policies --global-config /tmp/fas-nonexistent-global 2>/dev/null
{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"Recursive deletion of the home directory is blocked"}} (no-eol)
```

### Blocks rm -R ~

```scrut
$ cat << 'EOF' |
> {
>   "hook_event_name": "PreToolUse",
>   "tool_name": "Bash",
>   "tool_input": {"command": "rm -R ~"},
>   "session_id": "test",
>   "cwd": "/tmp"
> }
> EOF
> fas eval --harness claude --config tests/policies --global-config /tmp/fas-nonexistent-global 2>/dev/null
{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"Recursive deletion of the home directory is blocked"}} (no-eol)
```

### Blocks rm -rd ~

```scrut
$ cat << 'EOF' |
> {
>   "hook_event_name": "PreToolUse",
>   "tool_name": "Bash",
>   "tool_input": {"command": "rm -rd ~"},
>   "session_id": "test",
>   "cwd": "/tmp"
> }
> EOF
> fas eval --harness claude --config tests/policies --global-config /tmp/fas-nonexistent-global 2>/dev/null
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
> fas eval --harness claude --config tests/policies --global-config /tmp/fas-nonexistent-global 2>/dev/null
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
> fas eval --harness claude --config tests/policies --global-config /tmp/fas-nonexistent-global 2>/dev/null
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
> fas eval --harness claude --config tests/policies --global-config /tmp/fas-nonexistent-global 2>/dev/null
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
> fas eval --harness claude --config tests/policies --global-config /tmp/fas-nonexistent-global 2>/dev/null
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
> fas eval --harness claude --config tests/policies --global-config /tmp/fas-nonexistent-global 2>/dev/null
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
> fas eval --harness claude --config tests/policies --global-config /tmp/fas-nonexistent-global 2>/dev/null
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
> fas eval --harness claude --config tests/policies --global-config /tmp/fas-nonexistent-global 2>/dev/null
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
> fas eval --harness claude --config tests/policies --global-config /tmp/fas-nonexistent-global 2>/dev/null
{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"allow"}} (no-eol)
```

## Init Process Protection (struct-level disjunction)

A single rule denies either `kill` naming PID 1 directly OR `killall` naming
`systemd`/`init` — the two process-supervisor targets whose termination halts
the whole system. The two shapes are expressed with a top-level `when: {...}
| {...}` instead of two separate rules so the policy intent reads as one
idea. Ordinary signal delivery to other PIDs remains allowed.

### Blocks kill -9 1 (PID 1 / first disjunct)

```scrut
$ cat << 'EOF' |
> {
>   "hook_event_name": "PreToolUse",
>   "tool_name": "Bash",
>   "tool_input": {"command": "kill -9 1"},
>   "session_id": "test",
>   "cwd": "/tmp"
> }
> EOF
> fas eval --harness claude --config tests/policies --global-config /tmp/fas-nonexistent-global 2>/dev/null
{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"Refusing to signal the init process"}} (no-eol)
```

### Blocks killall -9 systemd (second disjunct)

```scrut
$ cat << 'EOF' |
> {
>   "hook_event_name": "PreToolUse",
>   "tool_name": "Bash",
>   "tool_input": {"command": "killall -9 systemd"},
>   "session_id": "test",
>   "cwd": "/tmp"
> }
> EOF
> fas eval --harness claude --config tests/policies --global-config /tmp/fas-nonexistent-global 2>/dev/null
{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"Refusing to signal the init process"}} (no-eol)
```

### Allows kill 1234 (ordinary PID, matches neither disjunct)

```scrut
$ cat << 'EOF' |
> {
>   "hook_event_name": "PreToolUse",
>   "tool_name": "Bash",
>   "tool_input": {"command": "kill 1234"},
>   "session_id": "test",
>   "cwd": "/tmp"
> }
> EOF
> fas eval --harness claude --config tests/policies --global-config /tmp/fas-nonexistent-global 2>/dev/null
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
> fas eval --harness claude --config tests/policies --global-config /tmp/fas-nonexistent-global 2>/dev/null
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
> fas eval --harness claude --config tests/policies --global-config /tmp/fas-nonexistent-global 2>/dev/null
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
> fas eval --harness claude --config tests/policies --global-config /tmp/fas-nonexistent-global 2>/dev/null
{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"allow"}} (no-eol)
```

## Runtime Directory Permission Guard

Socket files and PID files under `/run` are owned by system daemons. Widening
their permissions can hand an unprivileged process root-equivalent control
(e.g. `/run/docker.sock`). Policy denies all `chmod` invocations that name a
path under `/run` or any standard system prefix.

### Blocks chmod 777 /run/docker.sock

```scrut
$ cat << 'EOF' |
> {
>   "hook_event_name": "PreToolUse",
>   "tool_name": "Bash",
>   "tool_input": {"command": "chmod 777 /run/docker.sock"},
>   "session_id": "test",
>   "cwd": "/tmp"
> }
> EOF
> fas eval --harness claude --config tests/policies --global-config /tmp/fas-nonexistent-global 2>/dev/null
{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"Changing permissions on runtime directories is blocked"}} (no-eol)
```

### Blocks chmod o+w /run/systemd/private

```scrut
$ cat << 'EOF' |
> {
>   "hook_event_name": "PreToolUse",
>   "tool_name": "Bash",
>   "tool_input": {"command": "chmod o+w /run/systemd/private"},
>   "session_id": "test",
>   "cwd": "/tmp"
> }
> EOF
> fas eval --harness claude --config tests/policies --global-config /tmp/fas-nonexistent-global 2>/dev/null
{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"Changing permissions on runtime directories is blocked"}} (no-eol)
```

### Allows chmod +x ./scripts/deploy.sh

```scrut
$ cat << 'EOF' |
> {
>   "hook_event_name": "PreToolUse",
>   "tool_name": "Bash",
>   "tool_input": {"command": "chmod +x ./scripts/deploy.sh"},
>   "session_id": "test",
>   "cwd": "/tmp"
> }
> EOF
> fas eval --harness claude --config tests/policies --global-config /tmp/fas-nonexistent-global 2>/dev/null
{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"allow"}} (no-eol)
```

## Audit Log Protection

Moving or renaming files under `/var/log` destroys the audit trail that
incident response depends on. Policy denies `mv` invocations that reference
a `/var/log` path.

### Blocks mv /var/log/auth.log /tmp/hidden.log

```scrut
$ cat << 'EOF' |
> {
>   "hook_event_name": "PreToolUse",
>   "tool_name": "Bash",
>   "tool_input": {"command": "mv /var/log/auth.log /tmp/hidden.log"},
>   "session_id": "test",
>   "cwd": "/tmp"
> }
> EOF
> fas eval --harness claude --config tests/policies --global-config /tmp/fas-nonexistent-global 2>/dev/null
{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"Moving system log files conceals audit evidence"}} (no-eol)
```

### Blocks mv /var/log/syslog /var/log/syslog.bak (rotation bypass)

```scrut
$ cat << 'EOF' |
> {
>   "hook_event_name": "PreToolUse",
>   "tool_name": "Bash",
>   "tool_input": {"command": "mv /var/log/syslog /var/log/syslog.bak"},
>   "session_id": "test",
>   "cwd": "/tmp"
> }
> EOF
> fas eval --harness claude --config tests/policies --global-config /tmp/fas-nonexistent-global 2>/dev/null
{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"Moving system log files conceals audit evidence"}} (no-eol)
```

### Allows mv ./logs/debug.log ./archive/debug.log

```scrut
$ cat << 'EOF' |
> {
>   "hook_event_name": "PreToolUse",
>   "tool_name": "Bash",
>   "tool_input": {"command": "mv ./logs/debug.log ./archive/debug.log"},
>   "session_id": "test",
>   "cwd": "/tmp"
> }
> EOF
> fas eval --harness claude --config tests/policies --global-config /tmp/fas-nonexistent-global 2>/dev/null
{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"allow"}} (no-eol)
```

## tee to System Paths

`tee` is the canonical vector for writing to privileged files without a shell
redirect: `echo "..." | sudo tee /etc/sudoers.d/override` bypasses redirect
restrictions entirely. Policy denies direct `tee` invocations targeting system
prefixes at CRITICAL severity so this reason always surfaces.

### Blocks tee /etc/sudoers.d/override

```scrut
$ cat << 'EOF' |
> {
>   "hook_event_name": "PreToolUse",
>   "tool_name": "Bash",
>   "tool_input": {"command": "tee /etc/sudoers.d/override"},
>   "session_id": "test",
>   "cwd": "/tmp"
> }
> EOF
> fas eval --harness claude --config tests/policies --global-config /tmp/fas-nonexistent-global 2>/dev/null
{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"Writing to system paths via tee is blocked"}} (no-eol)
```

### Blocks tee -a /etc/cron.d/task

```scrut
$ cat << 'EOF' |
> {
>   "hook_event_name": "PreToolUse",
>   "tool_name": "Bash",
>   "tool_input": {"command": "tee -a /etc/cron.d/task"},
>   "session_id": "test",
>   "cwd": "/tmp"
> }
> EOF
> fas eval --harness claude --config tests/policies --global-config /tmp/fas-nonexistent-global 2>/dev/null
{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"Writing to system paths via tee is blocked"}} (no-eol)
```

### Allows tee ./build.log

```scrut
$ cat << 'EOF' |
> {
>   "hook_event_name": "PreToolUse",
>   "tool_name": "Bash",
>   "tool_input": {"command": "tee ./build.log"},
>   "session_id": "test",
>   "cwd": "/tmp"
> }
> EOF
> fas eval --harness claude --config tests/policies --global-config /tmp/fas-nonexistent-global 2>/dev/null
{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"allow"}} (no-eol)
```
