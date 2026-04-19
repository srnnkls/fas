# quae — Context

## Background

AI coding agents (Claude Code, Cursor, OpenCode, Factory AI) expose hook systems that intercept tool calls. Current policy engines for these hooks rely on string/regex matching or OPA Rego compiled to WASM — models that force imperative pattern matching on raw command strings, producing false positives (e.g., `2>/dev/null` triggering system path rules because `/dev/` is a substring).

quae takes a different approach: structural pattern matching via CUE unification, operating on preprocessed canonical structures (`#Parsed`) rather than raw strings. A modular parser pipeline normalizes tool-specific input into a uniform shape (actions, targets, flags, attributes), and optional Wasm signal modules enrich input with computed properties before rule evaluation.

## Prior Art

| Tool | Approach | Limitation |
|------|----------|------------|
| OPA/Rego | Logic programming + WASM | Verbose imperative syntax, string matching false positives, external binary dependency |
| CEL | Expression evaluation | Imperative boolean logic, not structural matching |
| Cedar | Policy language | AWS-centric, request-authorization model doesn't fit hook enrichment |
| Nickel | Contracts + merging | Config-generation focused, not runtime policy evaluation |
| KCL | Constraint validation | Config-language DNA, not designed for runtime matching |
| Polar (Oso) | Pattern matching rules | Deprecated, unmaintained |

CUE was selected for its unification semantics: values are constraints, matching is structural, and the reference implementation is native Go.

## Constraints

- Pure Go binary — `CGO_ENABLED=0`, no subprocess calls, no external runtime
- All non-Go execution via Wasm (wazero) with fuel/memory limits
- <50ms per evaluation for the core pipeline (excluding Wasm signal execution)
- Configurable fail mode: default fail-open, optional fail-closed
- No built-in rules ship by default — blank slate
- Adapters are compiled Go; no user-configurable transforms on stdin→eval or eval→stdout
- Executable Wasm modules must be pinned by sha256 in lockfile

## Assumptions

- Vendors' hook JSON schemas are stable enough to write adapters against
- CUE's `Value.Unify()` API supports the "try-match, check-for-error" pattern for rule evaluation
- CUE file loading and compilation is fast enough for per-invocation use (<50ms budget)
- wazero provides sufficient Wasm runtime performance for signal modules within the latency budget
- Tree-sitter Wasm grammars are available for languages commonly manipulated by AI coding agents
