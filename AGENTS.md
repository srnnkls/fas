# AGENTS.md

Guidance for AI coding agents working in this repository.

## Language

Go. All code is pure Go (`CGO_ENABLED=0`).

## Authoritative guidance

**Consult `/loqui` for all Go patterns, idioms, and best practices.** Do not duplicate or paraphrase its content here. When in doubt, re-read it before writing code.

## Write modern Go (2026)

Training data skews toward pre-1.21 Go. Resist that bias. This project targets current Go, and code should look like it.

- **Use the modern stdlib.** `slices`, `maps`, `cmp` (1.21+), `min`/`max`/`clear` builtins, `errors.Join`, `log/slog`, `cmp.Or`.
- **`range` over integers and functions.** `for i := range 10` and `for v := range seq` are the defaults — not C-style `for i := 0; i < n; i++` or index-into-slice loops.
- **Loop-scoped variables.** Don't reintroduce the `i := i` / `v := v` shadow — 1.22+ scopes loop variables per iteration.
- **Generics where they clarify.** Prefer `any` over `interface{}`. Use type parameters instead of `reflect` or code generation where a generic fits.
- **Context-aware errors.** Wrap with `fmt.Errorf("...: %w", err)`; check with `errors.Is` / `errors.As`.
- **`new(expr)` (1.26+).** `new(42)` instead of a temporary variable or a `ptr[T]` helper.
- **Self-referential generic types (1.26+).** OK to use when modelling recursive constraints.

If you find yourself writing an idiom that looks like 2018 Go, stop and check whether a stdlib or language feature replaces it.

## Modernizer

After any toolchain bump, run `go fix ./...` from a clean working tree. It is safe and behavior-preserving, and will rewrite obsolete idioms across the repo.

## When uncertain

Ask `/loqui` first. Only fall back to training-data recall when the answer isn't there.

## Authoring rules

A rule's `when` block is a **pattern**, not a predicate. There is no `$input`
binding, no `self`, no match variable — the pattern IS the description of the
input shape that matches. An input matches the rule iff CUE subsumption accepts
the input as an instance of the `when` pattern.

### Subsumption is the primitive

`Evaluate` calls `rule.When.Subsume(input)` per rule. Subsumption handles every
leaf constraint CUE itself handles:

- Literals: `tool_name: "Bash"`
- Bounds: `count: >=2`, `tool_name: !="Read"`
- Regex: `command: =~"^rm\\s+-rf"`, `command: !~"^git\\s+"`
- List element patterns: `targets: [...=~"^/etc/"]`
- Optional fields: `flags?: force?: !=true`
- Struct-level disjunction: `{tool_name: "Bash"} | {tool_name: "Write"}`

No custom operator, no evaluator-level fallback. If CUE can check it, `when`
can express it.

### Structural negation via De Morgan

CUE has no `!` over structs. Push negation to the leaves:

| Wanted                            | Express as                               |
| --------------------------------- | ---------------------------------------- |
| `tool_name` is not `"Bash"`       | `tool_name: !="Bash"`                    |
| `command` does not match `^rm`    | `command: !~"^rm"`                       |
| not (`a=1` AND `b=2`)             | `{a: !=1} \| {b: !=2}`                   |
| not (match `X` AND match `Y`)     | `X_negated \| Y_negated` (each at leaf)  |

### Patterns Subsume does not evaluate

Subsumption checks the `when` pattern **statically**. It does not substitute
input values into the pattern's own references. The following shapes compile
and unify at schema time, but at match time they do not reflect input content:

- **Sibling references inside `when`.** `{command: targets[0], targets: [...]}`
  — the pattern requires `command` to equal `targets[0]` *within the pattern*,
  not within the input. Matching inputs must structurally carry both fields in
  that relationship.
- **`let` bindings over input-derived values.** `let cmd = tool_input.command`
  names a path inside the pattern; it does not bind the input's command.
  **Rejected at load time (E0506).**
- **Input-dependent `if`/`for` clauses inside `when`.** `if list.Contains(flags,
  "x") { command: =~"..." }` evaluates the `if` against the pattern's own
  `flags` (which is a type, not a value), so the gated fields do not appear
  conditionally based on the input. `for` comprehensions have the same problem.
  **Rejected at load time (E0507).**
- **Computed hidden count fields.** `_n: len(flags)` with `_n: >=2` —
  `flags: [...string]` materialises as `[]` at pattern level, so `_n` is `0`
  regardless of input. **Rejected at load time (E0508).** Use `list.MatchN`
  instead: `flags: list.MatchN(>=2, string)`.
- **`close` over a `when` struct.** `when: close({tool_name: "Bash"})` closes an
  open struct pattern, so on extensible hook payloads the closed pattern never
  subsumes the input and the rule silently never matches. `close` is excluded
  from the curated universe builtins for exactly this reason.

Express these via:

- **List patterns.** `targets: [...=~"^/etc/"]` (every element matches),
  `flags: list.MatchN(>=2, string)` (at least two elements).
- **Regex on the raw field.** For count-or-shape checks, regex over
  `tool_input.command` is usually enough.
- **Multiple conjunctive fields** combined with struct-level `|` for
  disjunction.

### What the lint rejects at load time

`internal/config/lint.go` walks every rule's `when` subtree and rejects:

- **Cross-rule refs.** `when: other_rule.when.foo` (or `.then.bar`, `.meta.x`).
  Share values through hidden siblings (`_foo`) or stdlib imports.
- **Self-refs into `then`/`meta`.** `when` must be a pure pattern over the
  input; `then` and `meta` are not visible at match time.
- **Unbound identifiers.** Any ident that resolves to none of a stdlib import
  binding, a locally-visible hidden sibling (`_foo`), a curated universe builtin,
  or a bare sibling top-level rule struct.
- **`let` clauses inside `when`.** `let` binds the pattern's type, not the
  input's value; the resulting constraint silently misfires. E0506.
- **Comprehensions (`if`/`for`) inside `when`.** Guards and iterators evaluate
  against the pattern's own types, not the input's values, so guarded fields
  either always or never appear regardless of input. E0507.
- **`len()` calls inside `when`.** `len` computes over the pattern's
  materialised value (`[]` for open lists), not the input's. Use `list.MatchN`
  instead. E0508.

Imports, predeclared names (`string`, `int`, `number`, …), hidden local
helpers, and bare references to sibling top-level rule structs all pass. The
curated universe builtins `and`, `or`, `matchN`, and `matchIf` also pass
bare in `when`. `close`, `len`, and the arithmetic helpers (`div`, `mod`,
`quo`, `rem`) stay rejected.

### Organizing rules into packages

The loader walks a rules tree and loads each directory of `.cue` files as one
CUE **package**; subdirectories are separate, independent packages. The module
is `fas.local/rules`.

- **One package per directory.** The `package` clause is optional — files that
  omit it (or use `package _`) adopt the directory's canonical package: the one
  explicit name declared in that directory, or `rules` by default. Only **two or
  more different explicit** package names in one directory are a load error
  (`E0505`, naming the offending files); this matches CUE, which itself rejects
  only multiple named packages per directory. Sibling files in a package share
  scope: a `_helper` or `#Def` in one file is visible to the others. Declare an
  explicit `package <name>` matching the directory for a package meant to be
  *imported* (e.g. `schema/`).
- **Subdirs are independent packages**, loaded recursively. The same rule name
  in *different* packages is fine; a duplicate top-level rule name *within* one
  package (across its files) is a load error (`E0504`, naming both files).
- **Pruned.** Dotfile dirs (`.x`), underscore dirs (`_x`), and `cue.mod` are
  skipped with their subtrees; non-`.cue` files are ignored.
- **Total order.** Rules return in `CompareModulePath(ModuleRelPath)` order:
  dir-lexical, then basename, then declaration order within a file —
  independent of traversal order.

**Sharing schema.** Put reusable `#defs` in a `schema/` subdir (`package
schema`); other packages import it by module-relative path
(`import "fas.local/rules/schema"`) and reference `schema.#SomeDef`.

**Visibility caveat.** Only `#defs` cross a package boundary. A `_hidden` field
is package-private and is *not* visible to an importing package; every regular
top-level field is extracted as a **rule**. So a shared-defs package must expose
`#defs`, not regular fields. Same-package files still share `_hidden` helpers —
only cross-package sharing requires `#defs`.

Cross-*layer* concerns (global vs project, `replace`/`extend`/`disable`,
precedence) belong to the separate rule-precedence model; this section is only
about in-layer package organization.

## Testing the stdlib

### Oracle independence

Matcher corpora live in `cue/testdata/*.tsv` and state **domain truth** (`man rm`
recursion intent, path-component semantics), authored WITHOUT reading the matcher
— a case can contradict the code. Never derive cases from the regex; a corpus
that mirrors the implementation cannot catch a wrong implementation.

### testdata is single-source

Each `*.tsv` (`input <TAB> {match|nomatch}`) feeds the table tests, the
differential, AND the fuzz seed corpus. One file per vocabulary, reviewable
against a man page.

### Layers

| ID | Layer | Where |
| --- | --- | --- |
| INV-1 | Spec-derived matcher tables | `stdlib_test.go` ← `testdata/*.tsv` |
| INV-2 | `systemTarget` ↔ `systemInCommand` differential with an asserted whitelist | `stdlib_differential_test.go` |
| INV-3 | `FuzzRecursiveFlag` vs a hand-written man-rm reference predicate | `stdlib_fuzz_test.go` |
| INV-4 | catalog ↔ binder derivation | `stdlib_derivation_test.go` |
| INV-5 | composition (positive + negative `&` chain) | `stdlib_composition_test.go` |
| INV-6 | catalog typos rejected at rule load — enforced separately (not this suite's files) | `internal/config/loader_test.go` (`TestLoadRules_TypoedToolRef_Rejected` / `TestLoadRules_TypoedAgentRef_Rejected`) |
| INV-7 | scrut deny + near-miss-allow completeness | `tests/policy_coverage_test.go` |
| INV-8 | inline-regex drift guard | `tests/policy_drift_test.go` |

### Running

- `go test ./...` runs everything, including the fuzz seed corpora (the
  deterministic gate).
- `mise run test-fuzz` deepens the fuzz target manually (non-gating).
- ALWAYS `go install ./cmd/fas` (or `mise run install`) before scrut
  (`mise run test-integration`): scrut's subprocesses invoke the on-PATH `fas`,
  and a stale binary gives false failures. (`go test ./...` does not run `fas`.)

### New-bug-mid-implementation rule

If a spec-derived case surfaces a NEW matcher bug, open an issue, mark the case
`xfail` referencing it, and proceed — ship the detection. The fix is a separate
change; matcher behavior is out of this suite's scope.
