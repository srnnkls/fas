---
issue_type: initiative
name: quae
status: draft
created: 2026-02-13
---

# quae

CUE-based policy engine for AI coding agent hooks. Evaluates tool-call events against declarative rules using structural unification, with a modular parser/signal pipeline for input enrichment.

## Goals

1. Eliminate false positives from string/regex-based command matching via structured preprocessing
2. Provide a unified hooks interface across AI coding agent vendors
3. Enable declarative policy authoring without imperative logic
4. Support context injection and input modification as first-class effects alongside gate actions
5. Allow extensibility through Wasm signals and modular parsers without compromising security

## Success Criteria

- Zero false positives on standard development workflows (git, npm, cargo, etc.)
- <50ms evaluation latency per hook event (excluding Wasm signal execution)
- Vendor adapters for Claude Code, Cursor, OpenCode, and Factory AI
- CUE rule authoring requires no Go code — pure `.cue` files
- All executable modules (Wasm, jq) pinned by sha256 in lockfile

## User Stories

### P1 — Core Engine

- As a developer, I can write CUE rules with `when` clauses that structurally match tool-call events via unification
- As a developer, I can use `if` guards in rules for cross-field logic (comparisons, arithmetic, existence branching)
- As a developer, I can define gate actions (halt/deny/block/ask/allow) and effects (inject/modify) declaratively
- As a developer, tool input is parsed by builtin Go parsers into canonical `#Parsed` structure (actions, targets, flags, attributes) before matching
- As a developer, I can run `quae eval` with JSON on stdin and get an `OutputEnvelope` on stdout
- As a developer, I can layer global rules (~/.config/quae/) with project rules (.quae/) where blocking gates short-circuit but effects accumulate
- As a developer, I can use the CUE standard library (`quae.cue`) with composable structural constraints and FlagSet templates
- As a developer, I can use quae with Claude Code via a compiled Go adapter

### P2 — Multi-Vendor + Tooling

- As a developer, I can use quae with Cursor, OpenCode, or Factory AI via compiled Go adapters
- As a developer, vendor is auto-detected from the input payload when I don't pass --harness
- As a developer, I can run `quae validate-rules` to check CUE rules against the schema
- As a developer, I can run `quae validate-adapter` and `quae validate-parser` with fixtures
- As a developer, I can run `quae init` to scaffold a .quae/ directory with example rules

### P3 — Extensibility

- As a developer, I can write Wasm signal modules that enrich input at `signals.<name>`, running only when referenced by rule `meta.requires`
- As a developer, I can use additional parser backends (regex, tree-sitter, Wasm, jq) to preprocess tool input for different tools
- As a developer, all executable modules are declared in `quae.lock.cue` with sha256 hashes and resource limits
- As a developer, I can run `quae validate-modules` to verify lockfile integrity

## Architecture Overview

```
stdin JSON → Go Adapter (ParseInput) → #Input validation
    → Preprocessor (parser dispatch by tool_name) → tool_input.parsed
    → Signals (demand-driven Wasm modules) → signals.*
    → CUE Evaluator (when unification + if guards) → matched actions
    → Synthesizer (gate + inject + modify → OutputEnvelope)
    → Gate Dispatch (Category: Blocking/Asking/Allowing)
    → Go Adapter (RenderOutput) → stdout JSON
```

**Two-phase evaluation:**

1. Global rules (~/.config/quae/rules/*.cue) — blocking gates short-circuit, effects accumulate
2. Project rules (.quae/rules/*.cue) — synthesize gate + effects into OutputEnvelope

**Gate priority:** halt > deny/block > ask > allow. Effects (inject, modify) are orthogonal to the gate.

## Implementation Strategy

**MVP First:**

- **v0.1 (P1):** CUE engine + builtin Go parsers + synthesizer + CLI eval + Claude Code adapter + stdlib
- **v0.2 (P2):** Remaining vendor adapters + auto-detection + validation commands + init
- **v0.3 (P3):** Wasm runtime + signals + custom parser backends + module lockfile
