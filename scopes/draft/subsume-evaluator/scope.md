---
created: 2026-04-22
status: draft
issue_type: Task
---

# Scope — Subsume-based `when` evaluator

## Goal

Replace the hand-rolled `inputSatisfies` walker in `internal/evaluator/evaluator.go` with a single `cue.Value.Subsume` call. The rule's `when` clause IS the pattern the input must fit; subsumption answers "is the input an instance of this pattern?" natively, without a custom walker.

This is the evaluator-correctness half of the `when`-sublanguage work. The diagnostic/error-UX half lives in a separate scope (`diag-errors`).

## Context

`internal/evaluator/evaluator.go:56-87` defines a ~30-line recursive walker that iterates `when` field-by-field, handles optionals, and unifies leaves. It exists because CUE's default unification semantics **propagate** constraints rather than evaluating them — unifying `{ tool_name: "Bash" }` with `{}` succeeds, silently masking an unsatisfied rule.

The walker was the workaround. The correct answer is CUE's **subsumption** operator: `when.Subsume(input) == nil` iff input is an instance of `when`. Subsumption evaluates closed-world pattern match natively — required fields must exist, leaf constraints must be satisfied, disjunction/optional/nested-struct semantics all correct by construction.

### No input binding — the pattern *is* the input

An earlier draft of this scope introduced a `$input` binding so authors could write `let`, `for`, `if`, and refs against the input from inside `when`. That was Rego-shaped thinking. Under subsumption, rules are patterns — the entire `when` block describes the shape the input must have. Everything a rule author might want to reference "from the input" is already in the pattern itself:

| Seemed to need `$input` | Actually expressed by |
|---|---|
| `let cmd = $input.tool_input.command` | `let cmd = tool_input.command` — `let` inside a struct references sibling fields |
| `if list.Contains($input.flags, "--force") { ... }` | `flags: [...string]` + `if list.Contains(flags, "--force") { ... }` — `if` clauses reference siblings |
| `for t in $input.targets { ... }` | `targets: [...=~"^/etc/"]` — list patterns already mean "every element satisfies" |
| "count flags > 5" | `_n: len(flags)` + `_n: >5` — computed hidden field constrained to hold |
| "command equals first target" | `command: targets[0]` — sibling reference inside the struct |

There is no "the input" to reference separately; the rule IS the description of the input.

**Principle:** CUE resolves everything resolvable at build time; subsumption evaluates the rest. The only code quae still needs is the subsumption call and a narrow load-time lint that keeps unresolvable constructs out.

## Requirements

### Functional

- **F1** — `evaluator.Evaluate` uses `when.Subsume(input) == nil` as the single match primitive. `inputSatisfies` is deleted.
- **F2** — Struct-level `|` inside `when` is **allowed** (subsumption handles it correctly). No lint rejection; no rewrite to a custom operator.
- **F3** — Structural negation is expressible via CUE-native operators (scalar `!=`/`!~`, optional `field?:`, `field?: _|_`, De Morgan'd disjunction of negated scalars). No `match.#not` primitive.
- **F4** — Rule authors use CUE's native sibling references, `let`, `for`, `if`, and list patterns inside `when`. These all work because `when` is a self-contained pattern — the constraints reference fields within the pattern itself, not an externally-named "input."
- **F5** — Load-time lint rejects three patterns inside `when`:
  - Refs to another rule's `when`/`then`/`meta` (cross-rule coupling).
  - Refs to the same rule's `then`/`meta` from within `when` (data-flow nonsense).
  - Unbound identifiers — identifiers that are neither stdlib imports nor local hidden siblings (`_foo`).
- **F6** — Absent paths in the input yield non-match (not error). When `when` requires a field that the input lacks, subsumption returns a non-nil error; the rule does not fire. No runtime crash.

### Non-functional

- **NF1** — All existing Go tests pass (`go test ./...`). The 14 evaluator tests and 27 loader tests must survive unchanged.
- **NF2** — Scrut contract (`tests/policies.md`) continues to pass. Fixtures may migrate syntax but observable behavior must not regress.
- **NF3** — No performance regression beyond a constant factor versus the current walker benchmark baseline.
- **NF4** — Breaking changes to rule-file syntax are acceptable on the working branch — no external users yet.

## Out of scope

- Compiler-style error messages, error code registry, per-segment provenance, `quae explain` CLI — all handled in the `diag-errors` scope.
- Retaining `ast.Expr` for `when` in `config.Rule` — added in `diag-errors` where diagnostics actually need it.
- Any custom operator / mini-language (`match.#all`, `match.#any`, `match.#not`) — explicitly rejected. CUE-native operators cover everything via De Morgan.
- Any `$input` or equivalent binding — the pattern is self-describing.
- Rewriting CUE's evaluator or switching rule language.

## Verification

### Acceptance tests

- **Given** a rule with `when: { tool_name: "Bash" }` and input missing `tool_name`, **when** evaluated, **then** subsumption fails → rule does not match.
- **Given** a rule with `when: { tool_input: command: =~"^rm " }` and input `{ tool_input: command: "rm -rf /" }`, **when** evaluated, **then** rule matches.
- **Given** a rule with `when: { tool_name: "Bash" } | { tool_name: "Write" }` and input `{ tool_name: "Bash" }`, **when** evaluated, **then** rule matches via struct-level `|`.
- **Given** a rule with `when: { tool_input: { flags: [...string], if list.Contains(flags, "--force") { command: =~"^git push" } } }`, **when** input has `--force` and command starts `git push`, **then** rule matches via CUE sibling reference.
- **Given** a rule with `when: { tool_input: parsed: targets: [...=~"^/etc/"] }`, **when** input has all targets under `/etc/`, **then** rule matches via list pattern.
- **Given** a rule whose `when` references a path the input lacks, **when** evaluated, **then** rule does not match (no runtime error).
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
| `internal/evaluator/evaluator.go:21-35` | `Evaluate` — collapse to `rule.When.Subsume(input) == nil` |
| `internal/evaluator/evaluator_test.go` | Expand coverage for struct `|`, sibling refs, list patterns, De Morgan negation |
| `internal/config/loader.go` | Add load-time lint entry point |
| `internal/config/lint.go` (NEW) | AST-walk `when`; three reject rules |
| `internal/config/loader_test.go` | Expand coverage for reject-lint cases |
| `tests/policies/*.cue` | Fixture migration where new vocabulary reads better |
| `tests/policies.md` | Scrut contract — must not regress |
| `AGENTS.md` | Document subsumption as the evaluation primitive under "authoring rules" |
