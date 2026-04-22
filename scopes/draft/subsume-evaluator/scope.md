---
created: 2026-04-22
status: draft
issue_type: Task
---

# Scope — Subsume-based `when` evaluator

## Goal

Replace the hand-rolled `inputSatisfies` walker in `internal/evaluator/evaluator.go` with a single `cue.Value.Subsume` call, and bind the event payload into `when`'s CUE scope as `$input` so rule authors get CUE's full vocabulary (`let`, `for`, `if`, refs to input) natively.

This is the evaluator-correctness half of the `when`-sublanguage work. The diagnostic/error-UX half lives in a separate scope (`diag-errors`).

## Context

`internal/evaluator/evaluator.go:56-87` defines a ~30-line recursive walker that iterates `when` field-by-field, handles optionals, and unifies leaves. It exists because CUE's default unification semantics **propagate** constraints rather than evaluating them — unifying `{ tool_name: "Bash" }` with `{}` succeeds, silently masking an unsatisfied rule.

The walker was the workaround. The correct answer is CUE's **subsumption** operator: `when.Subsume(input) == nil` iff input is an instance of `when`. Subsumption evaluates closed-world pattern match natively — required fields must exist, leaf constraints must be satisfied, disjunction/optional/nested-struct semantics all correct by construction. It is the API we should have been calling all along.

Second change: rule authors today cannot reference the input inside `when` except by matching against it positionally. With `$input` bound into scope, they get `let _cmd = $input.tool_input.command`, `if list.Contains($input.tool_input.parsed.flags, "--force")`, `for t in $input.tool_input.parsed.targets`. CUE's own evaluator handles all of these once the binding is in place.

**Principle:** CUE resolves everything resolvable at build time; subsumption evaluates the rest. The only code quae still needs is the input-binding step, the subsumption call, and a narrow load-time lint that keeps unresolvable constructs out.

## Requirements

### Functional

- **F1** — `evaluator.Evaluate` uses `when.Subsume(input) == nil` as the single match primitive. `inputSatisfies` is deleted.
- **F2** — Before subsumption, the evaluator binds the concrete input into `when`'s CUE scope as `$input` (via `FillPath` or equivalent). `let`, `for`, `if`, and refs to `$input.*` inside `when` resolve natively via CUE.
- **F3** — Struct-level `|` inside `when` is **allowed** (subsumption handles it correctly). No lint rejection; no rewrite to a custom operator.
- **F4** — Structural negation is expressible via CUE-native operators (scalar `!=`/`!~`, optional `field?:`, `field?: _|_`, De Morgan'd disjunction of negated scalars). No `match.#not` primitive.
- **F5** — Load-time lint rejects three patterns inside `when`:
  - Refs to another rule's `when`/`then`/`meta` (cross-rule coupling).
  - Refs to the same rule's `then`/`meta` from within `when` (data-flow nonsense).
  - Unbound identifiers that are not stdlib imports, local hidden fields, or `$input`.
- **F6** — Absent paths under `$input` yield non-match (not error). An absent `$input.foo.bar` referenced in `when` resolves via CUE to bottom; subsumption returns a non-nil error; the rule does not fire.

### Non-functional

- **NF1** — All existing Go tests pass (`go test ./...`). The 14 evaluator tests and 27 loader tests must survive unchanged.
- **NF2** — Scrut contract (`tests/policies.md`) continues to pass. Fixtures may migrate syntax but observable behavior must not regress.
- **NF3** — No performance regression beyond a constant factor versus the current walker benchmark baseline.
- **NF4** — Breaking changes to rule-file syntax are acceptable on the working branch — no external users yet.

## Out of scope

- Compiler-style error messages, error code registry, per-segment provenance, `quae explain` CLI — all handled in the `diag-errors` scope.
- Retaining `ast.Expr` for `when` in `config.Rule` — added in `diag-errors` where diagnostics actually need it.
- Any custom operator / mini-language (`match.#all`, `match.#any`, `match.#not`) — explicitly rejected. CUE-native operators cover everything via De Morgan.
- Rewriting CUE's evaluator or switching rule language.

## Verification

### Acceptance tests

- **Given** a rule with `when: { tool_name: "Bash" }` and input missing `tool_name`, **when** evaluated, **then** subsumption fails → rule does not match.
- **Given** a rule with `when: { tool_input: command: =~"^rm " }` and input `{ tool_input: command: "rm -rf /" }`, **when** evaluated, **then** rule matches.
- **Given** a rule with `when: { tool_name: "Bash" } | { tool_name: "Write" }` and input `{ tool_name: "Bash" }`, **when** evaluated, **then** rule matches via struct-level `|`.
- **Given** a rule with `when: if list.Contains($input.tool_input.parsed.flags, "--force") { tool_input: command: =~"^git push" }`, **when** input has `--force` and command starts `git push`, **then** rule matches.
- **Given** a rule with `when: for t in $input.tool_input.parsed.targets { ... }`, **when** input has concrete targets, **then** comprehension unfolds and subsumption evaluates against the resulting value.
- **Given** a rule whose `when` references `$input.nonexistent.path`, **when** evaluated, **then** rule does not match (no runtime error).
- **Given** a rule whose `when` references `rules.other_rule.when.foo`, **when** loader runs, **then** load fails with a cross-rule-ref error.
- **Given** a rule whose `when` references an unbound identifier `foo`, **when** loader runs, **then** load fails naming `foo`.
- **Given** all 30 scrut test blocks, **when** run after migration, **then** all 30 still pass.

### Commands

```
go test ./internal/evaluator/... ./internal/config/... ./cue/...
go test ./...
scrut test tests/policies.md
```

## Critical files

| Path | Role |
|------|------|
| `internal/evaluator/evaluator.go:56-87` | `inputSatisfies` — delete; replace with `Subsume` call |
| `internal/evaluator/evaluator.go:21-35` | `Evaluate` — wire in `$input` binding + `Subsume` |
| `internal/evaluator/evaluator_test.go` | Expand coverage for `$input`, struct `|`, De Morgan negation |
| `internal/config/loader.go` | Add load-time lint entry point |
| `internal/config/lint.go` (NEW) | AST-walk `when`; three reject rules |
| `internal/config/loader_test.go` | Expand coverage for reject-lint cases |
| `tests/policies/*.cue` | Fixture migration where new vocabulary reads better |
| `tests/policies.md` | Scrut contract — must not regress |
