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
- **Input-dependent `if` clauses inside `when`.** `if list.Contains(flags, "x")
  { command: =~"..." }` evaluates the `if` against the pattern's own `flags`
  (which is a type, not a value), so the gated fields do not appear
  conditionally based on the input.
- **Computed hidden count fields.** `_n: len(flags)` with `_n: >=2` —
  `flags: [...string]` materialises as `[]` at pattern level, so `_n` is `0`
  regardless of input.

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
- **Unbound identifiers.** Any ident that is neither a stdlib import binding
  nor a locally-visible hidden sibling (`_foo`).

Imports, predeclared names (`string`, `int`, `or`, `len`, ...), hidden local
helpers, and bare references to sibling top-level rule structs all pass.
