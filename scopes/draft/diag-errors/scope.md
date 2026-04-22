---
created: 2026-04-22
status: draft
issue_type: Feature
---

# Scope — Compiler-style diagnostics for quae

## Goal

Give quae a Rust-compiler-style diagnostic system: error codes, file:line:col, source snippets, carets under the specific failing token (path segment, leaf constraint, disjunction arm). One renderer serves load-time, lint-time, and evaluation-time errors. `quae explain` becomes a first-class UX for "why didn't this rule fire?".

Depends on the `subsume-evaluator` scope landing first — diagnostics attach to subsumption failures.

## Context

Today's error UX is thin. `ruleLoadError` wraps CUE's message and prefixes the rule file path. Evaluation failures return `bool`; there's no "why." Rule authors debugging a non-firing rule have to guess — print the input, re-read the rule, run again.

The `subsume-evaluator` scope gives us the fast path (`Subsume` returns nil → match). This scope gives us the slow path: on `Subsume` failure, walk the rule's AST paired with the input value, emit structured diagnostics that point at the **specific failing token** — not just the rule, not just the field, but the exact segment where the mismatch localizes.

Reference output shape (target):

```
error[E0201]: key not found
  --> policy.fas:12:24
   |
12 |     tool_input: flags: force: true
   |                 ^^^^^ key "flags" not found in input at path tool_input
   |
   = help: tool_input has keys: command, file_path
```

Three failure classes, three error shapes:

- **Path resolution** (`E02xx`) — a rule references a path `a.b.c` where a segment is absent from the input. Caret points at the first missing segment.
- **Leaf constraint** (`E03xx`) — regex / range / type mismatch at a leaf. Caret on the constraint, labels show `want` / `got`.
- **Disjunction** (`E04xx`) — all arms of a `|` failed. Caret highlights each arm's span with a per-arm reason.

Plus `E01xx` for load errors (schema mismatch, unknown action kind) and `E05xx` for scope/binding errors (unresolved identifier at load, rejected cross-rule ref).

## Requirements

### Functional

- **F1** — New package `internal/diag` defines `Diagnostic`, `Label`, `Severity` types and a compiler-style renderer (file, line:col, source snippet with caret, error code, Rust-ish format). Rendering is deterministic and stable across runs.
- **F2** — Error code registry: constants file with `E01xx` (load), `E02xx` (path resolution), `E03xx` (leaf constraint), `E04xx` (disjunction), `E05xx` (scope/binding). Each code has a short help string; `quae explain --code E0201` prints the help.
- **F3** — `config.Rule` retains an `ast.Expr` for `when` alongside the semantic `cue.Value`. Threaded through `compileRuleFile` / `extractFileRules` / `decodeRule`. Used by the debug-path localizer.
- **F4** — Evaluator gains a debug path. Same subsumption verdict as the fast path; on failure, walks the rule AST + input pairwise to produce `[]Diagnostic`. Debug path activates only when debug mode is on (flag / env var / `explain` subcommand) — zero cost on production eval.
- **F5** — `localize` emits per-segment diagnostics:
  - Path-segment missing → `E0201` with caret on the segment.
  - Leaf constraint failure → `E0301` with caret on the constraint span, labels for `want` and `got`.
  - Disjunction-all-fail → `E0401` with each arm's span labeled and the closest-match arm noted.
- **F6** — Existing `ruleLoadError` migrates to emit `Diagnostic` via `internal/diag`. Loader-level errors (schema mismatch, unknown action kind, lint rejections from `subsume-evaluator`) use the same output shape as evaluator-level errors. One visual language across the tool.
- **F7** — CLI surface:
  - `--explain[=fired|missed|both]` flag on `quae eval`. Default filter: `missed`. Emits diagnostics to stderr after the normal response on stdout.
  - `QUAE_EXPLAIN=1` env var enables the same behavior without a flag (hook-debugging use case).
  - New `quae explain <rule_id> < input.json` subcommand: runs one rule against stdin, always prints diagnostic, exits 0 on match / 1 on no-match / 2 on engine error.

### Non-functional

- **NF1** — Zero cost on the production path. Debug-mode code runs only when flag/env/subcommand opts in; fast-path `Subsume` call is unchanged.
- **NF2** — Renderer output is deterministic — same inputs → byte-identical output. Tests can snapshot diagnostics.
- **NF3** — Diagnostic emission is best-effort: if AST positions are unavailable for any reason, the diagnostic still renders with a degraded "position unknown" label rather than crashing.
- **NF4** — No dependency on an external rendering library (codespan-reporting equivalents exist in Go — prefer a small in-tree implementation over a dep).

## Out of scope

- Any evaluator semantics changes — those live in `subsume-evaluator`.
- Multi-file diagnostic cross-references (e.g., "this rule conflicts with that one across files") — single-rule localization only.
- JSON / machine-readable output for diagnostics — text-only v0. Add a `--format=json` flag in a follow-up if needed.
- IDE / LSP integration — separate concern.

## Verification

### Acceptance tests

- **Given** a rule whose `when` requires `tool_input.flags.force` and input lacks `flags`, **when** `quae explain` runs, **then** output shows `E0201` with caret under `flags` and a help listing actual keys at `tool_input`.
- **Given** a rule with `tool_input: command: =~"^rm "` and input `command: "ls -la"`, **when** `quae explain` runs, **then** output shows `E0301` with caret under the regex, `want:` and `got:` labels.
- **Given** a rule with `tool_name: "Bash" | "Write" | "Edit"` and input `tool_name: "Read"`, **when** `quae explain` runs, **then** output shows `E0401` with each arm's span highlighted and an arm-by-arm "not equal Read" label.
- **Given** a rule whose `when` has a cross-rule ref, **when** loader runs, **then** error prints `E0502` with caret on the cross-rule selector expression and help suggesting a hidden sibling.
- **Given** `quae eval --explain=missed < input.json` with three rules (one fires, two don't), **when** run, **then** stdout contains the vendor response, stderr contains exactly two diagnostics (one per non-firing rule).
- **Given** `QUAE_EXPLAIN=1` is set, **when** `quae eval` runs, **then** behavior matches `--explain=missed`.
- **Given** `quae explain my_rule < input.json` with a matching rule, **when** run, **then** exit 0, no diagnostic printed.
- **Given** the same command with a non-matching rule, **when** run, **then** exit 1, diagnostic printed to stderr.

### Commands

```
go test ./internal/diag/... ./internal/evaluator/... ./cmd/quae/...
go test ./...
scrut test tests/policies.md
scrut test tests/diagnostics.md  # NEW — CLI surface tests
```

## Critical files

| Path | Role |
|------|------|
| `internal/diag/` (NEW) | `Diagnostic`, `Label`, `Severity`, renderer, error codes registry |
| `internal/diag/codes.go` (NEW) | `E01xx`..`E05xx` constants + help strings |
| `internal/diag/render.go` (NEW) | Compiler-style renderer (line snippet, caret, labels) |
| `internal/config/loader.go` | Thread `ast.Expr` for `when` through load |
| `internal/config/loader.go` | `ruleLoadError` → `diag.Diagnostic` |
| `internal/config/lint.go` | Lint rejections emit `diag.Diagnostic` |
| `internal/evaluator/evaluator.go` | Debug-path `Explain` returns `(bool, []Diagnostic)` |
| `internal/evaluator/localize.go` (NEW) | AST-paired walk; per-segment diagnostic emission |
| `cmd/quae/main.go` | `--explain` flag; `QUAE_EXPLAIN` env; `explain` subcommand |
| `tests/diagnostics.md` (NEW) | Scrut contract for CLI diagnostic output |
