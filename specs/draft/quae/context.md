# quae — Context

## Background

AI coding agents (Claude Code, Cursor, OpenCode, Factory AI) expose hook systems that intercept tool calls. Current policy engines for these hooks rely on OPA Rego compiled to WASM — a model that forces imperative string matching on command strings, producing false positives (e.g., `2>/dev/null` triggering system path rules because `/dev/` is a substring).

quae takes a different approach: structural pattern matching via CUE unification, operating on preprocessed ASTs rather than raw strings.

## Prior Art

| Tool | Approach | Limitation |
|------|----------|------------|
| OPA/Rego | Logic programming + WASM | Verbose imperative syntax, string matching false positives, external binary dependency |
| CEL | Expression evaluation | Imperative boolean logic, not structural matching |
| Nickel | Contracts + merging | Config-generation focused, not policy evaluation |
| KCL | Constraint validation | Config-language DNA, not designed for runtime matching |
| Polar (Oso) | Pattern matching rules | Deprecated, unmaintained |

CUE was selected for its unification semantics: values are constraints, matching is structural, and the reference implementation is native Go.

## Constraints

- Pure Go binary — no CGo, no subprocess calls, no external runtime
- <50ms per evaluation (CUE compile + unify)
- Configurable fail mode: default fail-open, optional fail-closed
- No built-in rules ship by default — blank slate

## Assumptions

- Vendors' hook JSON schemas are stable enough to write adapters against
- mvdan.cc/sh can parse the bash commands that AI agents generate (typically simple single-line commands)
- CUE's Unify() API supports the "try-match, check-for-error" pattern for rule evaluation
- CUE file loading and compilation is fast enough for per-invocation use (<50ms budget)
