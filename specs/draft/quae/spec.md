---
issue_type: initiative
name: quae
status: draft
created: 2026-02-13
---

# quae

Structural policy engine for AI coding agent hooks. Evaluates tool-call events against declarative CUE rules using unification-based pattern matching.

## Goals

1. Eliminate false positives from string/regex-based command matching
2. Provide a unified hooks interface across AI coding agent vendors
3. Enable declarative policy authoring without imperative logic
4. Support context injection as a first-class action alongside allow/deny

## Success Criteria

- Zero false positives on standard development workflows (git, npm, cargo, etc.)
- <50ms evaluation latency per hook event
- Vendor adapters for Claude Code, Cursor, OpenCode, and Factory AI
- CUE rule authoring requires no Go code — pure `.cue` files

## User Stories

### P1 — Core Engine

- As a developer, I can write CUE rules that structurally match tool-call events so that I don't need imperative guard logic
- As a developer, I can define deny/allow/inject actions declaratively so that hook behavior is self-documenting
- As a developer, bash commands are parsed into structured ASTs before matching so that path-based rules don't produce false positives on redirections, flags, or string coincidences
- As a developer, I can run `quae eval` with JSON on stdin and get a decision on stdout so that any vendor's hook system can integrate

### P2 — Multi-Vendor + Configuration

- As a developer, I can use quae with Claude Code, Cursor, OpenCode, or Factory AI with vendor-specific adapters normalizing input/output
- As a developer, I can layer global rules (~/.config/quae/) with project rules (.quae/) where project rules override global
- As a developer, vendor is auto-detected from the input payload when I don't pass --harness

### P3 — Advanced Matching + Tooling

- As a developer, I can use advanced CUE matching patterns (custom constraint definitions, negation via list.MatchN(0, ...)) for rules beyond the standard library
- As a developer, I can run `quae init` to scaffold a .quae/ directory with example rules
- As a developer, I can run `quae check` to validate my CUE rules without evaluating
- As a developer, I can run `quae test` to run rule assertions against fixture events

## Architecture Overview

```
stdin JSON → Vendor Adapter (normalize) → Preprocessor (AST parse, derive fields)
    → CUE Evaluator (unify rules against input) → Synthesizer (priority merge)
    → Vendor Adapter (format response) → stdout JSON
```

**Two-phase evaluation:**

1. Global rules (~/.config/quae/rules/*.cue) — early termination on halt/deny
2. Project rules (.quae/rules/*.cue) — evaluated only if global allows

**Decision priority:** Halt > Deny/Block > Ask > Modify > Allow (with context injection)

## Implementation Strategy

**MVP First:**

- **v0.1 (P1):** CUE engine + bash preprocessing + CLI + Claude Code adapter
- **v0.2 (P2):** All vendor adapters + layered config resolution + auto-detection
- **v0.3 (P3):** Advanced CUE features + developer tooling (init, check, test)
