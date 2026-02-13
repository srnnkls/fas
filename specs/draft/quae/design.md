# quae — Design

## Architecture

```
┌─────────────┐     ┌──────────────┐     ┌──────────────┐
│   stdin      │────▶│ Vendor       │────▶│ Preprocessor │
│   (JSON)     │     │ Adapter (in) │     │ (AST parse)  │
└─────────────┘     └──────────────┘     └──────┬───────┘
                                                 │
                    ┌──────────────┐     ┌───────▼──────┐
                    │ Vendor       │◀────│ CUE          │
                    │ Adapter (out)│     │ Evaluator    │
                    └──────┬───────┘     └──────┬───────┘
                           │                    │
                    ┌──────▼───────┐     ┌──────▼──────┐
                    │   stdout     │     │ Synthesizer  │
                    │   (JSON)     │     │ (priority)   │
                    └──────────────┘     └─────────────┘
```

### Evaluation Pipeline

1. **Vendor adapter (in):** Normalize vendor-specific JSON to internal schema
2. **Preprocessor:** Parse bash commands via mvdan.cc/sh, extract structured AST, resolve paths
3. **CUE evaluator:** Load rules, attempt `Unify(rule.when, input)` for each rule, collect matched actions
4. **Synthesizer:** Merge matched actions by priority into a single decision
5. **Vendor adapter (out):** Format decision as vendor-specific response JSON

### Two-Phase Evaluation

```
Global rules (~/.config/quae/rules/*.cue)
    │
    ├── halt/deny/block? → short-circuit, return immediately
    │
    └── allow/inject? → continue to project rules
                │
                ▼
Project rules (.quae/rules/*.cue)
    │
    └── synthesize all matched actions → final decision
```

## Tech Decisions

### Go + CUE

Go as the implementation language. CUE's reference implementation (`cuelang.org/go`) is native Go, giving direct access to the unification API without FFI or subprocess overhead.

CUE as the policy language. Unification-based semantics where values are constraints. A rule's `when` clause is a structural constraint that unifies against the input — if unification succeeds (no validation error), the rule matches.

### mvdan.cc/sh for Bash Parsing

Mature Go bash parser producing full ASTs. Extracts program, arguments, targets, redirections, pipes, and subcommands as structured data. Moves intelligence from the policy language into the preprocessing layer, making structural matching sufficient for command analysis.

### CUE Standard Library (quae.cue)

Matching is done entirely in CUE using `list.MatchN`, `or()`, and `strings.Join`. No derived booleans are computed in Go — the preprocessor only parses and structures, CUE does all matching.

Three layers, each building on the previous:

**Layer 1 — Value lists (single source of truth):**
```cue
#SystemPrefixes:     ["/etc", "/sys", "/proc", "/boot", "/dev"]
#EscalationCommands: ["sudo", "doas", "su"]
#DestructiveFlags:   ["-rf", "--force", "--hard", "--no-preserve-root"]
```

**Layer 2 — Constraint definitions (derived validators):**
```cue
#systemTarget:      =~"^(\(strings.Join(#SystemPrefixes, "|")))"
#escalationCommand: or(#EscalationCommands)
#destructiveFlag:   or(#DestructiveFlags)
```

**Layer 3 — Structural constraints (composable matchers):**
```cue
#hasSystemTarget:        { tool_input: parsed: targets:         list.MatchN(>0, [#systemTarget]) }
#hasPrivilegeEscalation: { tool_input: parsed: prefix_commands: list.MatchN(>0, [#escalationCommand]) }
#hasDestructiveFlags:    { tool_input: parsed: flags:           list.MatchN(>0, [#destructiveFlag]) }

#isPreToolUse:  { hook_event_name: "PreToolUse" }
#isBash:        { tool_name: "Bash" }
#isFileWrite:   { tool_name: "Write" }
#isUserPrompt:  { hook_event_name: "UserPromptSubmit" }
```

Rules compose structural constraints via `&`. Power users can skip layer 3 and use `list.MatchN` directly with custom constraint definitions.

## API Contract

### CLI Interface

```
quae eval [--harness <vendor>] [--config <path>] [--fail-closed]
```

Reads JSON from stdin, writes JSON to stdout. Exit code 0 on success (any decision), non-zero on engine error.

### Input — Normalized Internal Schema

After vendor adapter normalization, all inputs conform to:

```json
{
  "hook_event_name": "PreToolUse",
  "tool_name": "Bash",
  "tool_input": {
    "command": "cat /etc/passwd 2>/dev/null",
    "parsed": {
      "program": "cat",
      "args": ["/etc/passwd"],
      "targets": ["/etc/passwd"],
      "redirections": [{"op": "2>", "target": "/dev/null"}],
      "pipes": [],
      "prefix_commands": [],
      "flags": []
    }
  },
  "resolved_file_path": null,
  "session_id": "...",
  "cwd": "/path/to/project"
}
```

### Output — Per Vendor

**Claude Code:**
```json
{"decision": "block", "reason": "Command targets critical system paths"}
```

**Cursor:**
```json
{"permission": "deny", "user_message": "...", "agent_message": "..."}
```

**OpenCode / Factory AI:** Adapter-specific, follows each vendor's hook response schema.

### Decision Types

| Decision | Priority | Semantics |
|----------|----------|-----------|
| halt | 1 (highest) | Immediate cessation |
| deny | 2 | Block the action |
| block | 2 | Block progression |
| ask | 3 | Require user confirmation |
| modify | 4 | Transform input and allow |
| inject | 5 | Add context, allow |
| allow | 6 (default) | Permit the action |

## Data Model

### CUE Rule Schema

```cue
#Rule: {
    when: {...}
    then: #Action
}

#Action: #Halt | #Deny | #Block | #Ask | #Modify | #Inject | #Allow

#Halt: halt: {
    rule_id:  string
    reason:   string
    severity: *"CRITICAL" | "HIGH" | "MEDIUM" | "LOW"
}

#Deny: deny: {
    rule_id:  string
    reason:   string
    severity: *"HIGH" | "CRITICAL" | "MEDIUM" | "LOW"
}

#Block: block: {
    rule_id:  string
    reason:   string
    severity: *"HIGH" | "CRITICAL" | "MEDIUM" | "LOW"
}

#Ask: ask: {
    rule_id:  string
    reason:   string
    question: string
}

#Modify: modify: {
    rule_id:       string
    reason:        string
    updated_input: _
    priority:      *50 | int & >=1 & <=100
}

#Inject: inject: string

#Allow: allow: true
```

### Example Rules

Using standard library structural constraints:

```cue
package rules

import "github.com/you/quae"

system_protection: #Rule & {
    when: quae.#isPreToolUse & quae.#isBash & quae.#hasSystemTarget
    then: halt: {
        rule_id:  "SYS-001"
        reason:   "Command targets critical system paths"
        severity: "CRITICAL"
    }
}

no_sudo: #Rule & {
    when: quae.#isPreToolUse & quae.#isBash & quae.#hasPrivilegeEscalation
    then: deny: {
        rule_id:  "SEC-001"
        reason:   "Privilege escalation not permitted"
        severity: "HIGH"
    }
}

coding_standards: #Rule & {
    when: quae.#isUserPrompt
    then: inject: """
        Use relative paths for all file operations.
        Run tests before committing.
        """
}
```

Power users mix library constraints with inline ones:

```cue
package rules

import (
    "list"
    "strings"
    "github.com/you/quae"
)

#sensitivePaths: ["/migrations", "/deploy", "/.env"]
#sensitiveTarget: =~"(\(strings.Join(#sensitivePaths, "|")))"

protect_sensitive: #Rule & {
    when: quae.#isPreToolUse & quae.#isFileWrite & {
        tool_input: parsed: targets: list.MatchN(>0, [#sensitiveTarget])
    }
    then: ask: {
        rule_id:  "PROJ-002"
        reason:   "Modifying sensitive project files"
        question: "This touches a protected path. Continue?"
    }
}
```

### Preprocessed Bash AST

```
ParsedCommand {
    program:            string
    args:               []string
    targets:            []string          // path-like arguments (resolved)
    redirections:       []Redirection
    pipes:              []ParsedCommand
    prefix_commands:    []string          // sudo, doas, env, etc.
    flags:              []string          // -rf, --force, etc.
    subcommands:        []ParsedCommand   // $(), ``
}

Redirection {
    op:     string      // ">", ">>", "<", "2>", etc.
    target: string      // "/dev/null", "output.log", etc.
}
```

### Config Resolution Order

```
1. .quae/rules/*.cue           (project — highest priority)
2. ~/.config/quae/rules/*.cue   (global)
```

Project rules evaluated after global rules. Global halt/deny short-circuits before project evaluation.

## Alternatives Considered

| Alternative | Why Not |
|-------------|---------|
| **OPA/Rego + WASM** | Verbose imperative syntax, string matching false positives, external binary dependency |
| **CEL** | Expression language — imperative boolean logic, not structural matching |
| **Nickel** | Rust-native, designed for config generation, not runtime policy evaluation |
| **KCL** | CNCF project, config-language DNA |
| **Dhall** | Typed config language, read-only Rust API |
| **Starlark** | Full scripting language — over-engineered for structural matching |
| **Polar (Oso)** | Pattern matching but deprecated |
| **Custom Rust matcher** | Loses CUE's constraint expressiveness and type system |

CUE won on: native Go implementation, unification semantics (values-as-constraints), structural matching without imperative logic, active maintenance.

## Invariants

1. **Preprocessing is mandatory** — no raw input reaches CUE evaluation
2. **Rules are pure constraints** — no side effects, no I/O in CUE files
3. **Decision priority is fixed** — Halt > Deny/Block > Ask > Modify > Allow
4. **Global before project** — global rules always evaluate first with early termination on blocking decisions
5. **Fail mode is explicit** — default fail-open, configurable to fail-closed
