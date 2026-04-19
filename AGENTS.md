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
