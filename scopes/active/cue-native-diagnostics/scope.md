---
created: 2026-04-23
status: draft
issue_type: Feature
---

# Scope — CUE-native diagnostics (typed Reason model)

## Goal

Turn fas's diagnostics from "Rust-style string-formatted messages" into **CUE-native inference reports**: carry the structured reason a subsumption failed (kind mismatch, bound violation, regex divergence, failed conjunct, ranked disjunction arms, constraint provenance) in the diagnostic itself, and let the renderer present each reason idiomatically. The typed reason tree also enables JSON / SARIF output for LSP and CI consumption, and a color-TTY path for terminal readability — all from one source of truth.

This scope assumes the prior `diag-errors` scope (v0, flat `Label.Msg` model) has shipped. It is the "path (b)" choice from the design exploration: rather than improving the hand-formatted strings, we extend the model.

## Context

### What v0 leaves on the floor

`internal/evaluator/localize.go:74` calls `ruleNext.Subsume(next, cue.Final(), cue.Schema())`, discards the returned `error`, then hand-formats `want: <f.Value source>` / `got: <actual.Syntax())`. The whole CUE inference layer is invisible to the diagnostic. `internal/diag/diagnostic.go:17-31` models a failure as free-form `Msg` strings with no slot for kind, conjunct/disjunct structure, or provenance.

Consequence: rendered output restates information the reader can already see (title + carets), can't break down composite constraints (`>=5 & <=10 & !=7`), can't rank disjunction arms by closeness, can't show where a unified constraint was introduced, and can't emit structured output for tools.

### The CUE APIs we start using

- `cue.Value.Expr()` returns `(Op, []Value)` — decomposes `A & B & C` into conjuncts, `A | B | C` into disjuncts, `>=5` into bound + operand. **Primary lever for decomposition.**
- `cue.Value.Kind()` / `IncompleteKind()` — lattice position; lets us report kind-mismatch before value-mismatch.
- `cue.Value.Err()` from failed Subsume — structured error chain with positions; we currently throw it away.
- `cue.Value.Pos()` on each conjunct/disjunct — origin position for provenance.
- `ast.Node.Comments()` — doc comments on rule fields become free `help:` text.

### Key files

| File | Lines | Role |
|------|-------|------|
| `internal/diag/diagnostic.go` | 17-31 | Add `Reason` slot on `Label`; keep `Msg` as fallback |
| `internal/diag/codes.go` | 76-115 | Adopt existing `E0303` (type mismatch) for KindMismatch Reason; revise help strings as needed. No new code allocation — E0302 (bound violation), E0303 (type mismatch), E0304 (enum) are already allocated by the shipped `diag-errors` scope |
| `internal/diag/render.go` | 165-191 | Per-Reason rendering; collapse same-span labels; drop restatements |
| `internal/evaluator/localize.go` | 66-127 | Rewrite leaf path via `cue.Value.Expr()` decomposition; emit Reasons |
| `internal/evaluator/localize.go` | 129-152 | Disjunction with ranked arms (closeness heuristic) |
| `internal/evaluator/localize.go` | 92-104 | Absent-key did-you-mean (Levenshtein over listKeys) |
| `cmd/fas/main.go` | — | `--format=text|json|sarif` flag; color/NO_COLOR gating |
| `tests/diagnostics.md` | all | Rewrite goldens to minimal (post-"no restate") form |
| `tests/diagnostics_formats.md` (NEW) | — | Scrut for JSON + SARIF + color output |

### Architecture decisions

**AD-1: Reason sum type as interface + concrete variants**

- **Context:** Need a typed, extensible failure-reason payload that survives JSON marshalling and pattern-matching in the renderer.
- **Decision:** `type Reason interface { reason() }` sealed by an unexported method. Variants hold **only serializable DTO fields** — no `ast.Expr`, no `token.Pos` — so the Reason tree round-trips through JSON byte-identically (T13, NF2). AST/position data is resolved to strings+spans at Reason construction time inside `localize`. Seven variants in two groups:
  - *Failure reasons* (singular — each describes exactly one failure shape): `KindMismatch{Want, Got cue.Kind, Actual string}`, `BoundViolation{Op, Bound, Actual, Distance string}`, `RegexMismatch{Pattern, Input string, DivergeAt int}`, `ConjunctFailed{Expr string, Span Span, Sub Reason}`, `DisjunctionFailed{Arms []ArmResult}` (where `ArmResult{Arm string, Span Span, Inner Reason, Score int}`), `KeyMissing{Key string, AvailableKeys []string, Suggestion string}`.
  - *Metadata reason* (decorates footer labels): `Provenance{Span Span, Snippet string}`.
  - Shared DTO: `Span{File string, Line, Col, Length int}` — replaces any `token.Pos`/`ast.Expr` in the model.

  **Invariant (I3a):** Each singular variant describes exactly one shape. Multiplicity is represented at the **attachment point** — `Label.Reasons` is `[]Reason` — never by growing singular variants into collections or introducing wrapper variants. A reader seeing `ConjunctFailed` knows it's about one conjunct; when several conjuncts fail at the same leaf, the Label carries multiple singular `ConjunctFailed` entries. Nested Reason positions (`ConjunctFailed.Sub`, `ArmResult.Inner`) are semantically singular in current scope; if a future site needs plurality there, it gains its own slice field (additive).

  `cue.Kind` is an `int` in the CUE API, marshals cleanly as a JSON number; we stabilise it via a string tag at the JSON layer (not in the Go struct) so rename-safety holds. `Label` gets a new `Reasons []Reason` field (slice, not singleton); zero-length means "no structured reason" and renderer falls through to `Msg` (NF5 migration buffer).
- **Alternatives:**
  - *Tagged struct with `Kind` enum + nullable fields* — marshals cleanly but requires every reader to switch on `Kind`; no compile-time exhaustiveness.
  - *Replace `Msg` entirely* — cleanest but forces a big-bang migration; we'd lose the incremental path.

**AD-2: Renderer is a dispatch table keyed on Reason type**

- **Context:** Text, JSON, SARIF, and color TTY all need to consume the same Reason tree.
- **Decision:** Renderers are pure functions `func(Reason) X` where `X` is `string` / `json.RawMessage` / `sarif.Result`. Dispatch via type switch inside each format package (`render/text`, `render/json`, `render/sarif`). No reflection, no generics — one exhaustive switch per format.
- **Alternatives:**
  - *Double-dispatch via a Visitor interface on Reason* — more OO; adds method bloat to every variant for every format.
  - *Tag-dispatch via a format-agnostic marshaller* — loses per-format idioms (e.g., SARIF's `logicalLocations` nesting vs text's flat caret list).

**AD-3: Arm ranking is a scored heuristic, not a solver**

- **Context:** For `E0401` disjunction, we want "closest arm was B" rather than "all arms failed".
- **Decision:** Score each arm with a three-tier comparator: (1) `Kind` match vs actual (exact > compatible > incompatible); (2) structural match for struct arms (field-count overlap + nested kind match); (3) value distance for scalar arms (bound distance for numeric, edit distance for strings). Highest score wins; tie → source order. Scoring runs only when `explainEnabled`. **No-close-arm threshold:** score constants are `ScoreKindMatch` > `ScoreStructuralMatch` > `ScoreValueDistance`; when the top-ranked arm's score is below `ScoreKindMatch` (no arm even shares kind), the text renderer suppresses ranked caret frames and emits a flat "no arm was close" summary with arms listed in a footer note. Data-model arms (including Score) are still fully populated and marshalled to JSON/SARIF — suppression is text-layer only, preserving NF2 determinism across formats.
- **Alternatives:**
  - *SAT-style minimal-diff solver* — accurate but expensive; overkill for rule-author debugging.
  - *No ranking, first-arm-only* — preserves current behavior but wastes the opportunity.

**AD-4: Provenance is best-effort; absent positions render as "origin unknown"**

- **Context:** When a rule field's constraint unifies from `stdlib/bash.cue:42` AND the rule file itself, we want to tell the reader both. CUE doesn't hand us this directly — we have to walk conjuncts of the resolved value back to their `Pos()`.
- **Decision:** `localize` walks `ruleNext.Expr()`; for each conjunct whose `Pos()` differs from `f.Value.Pos()`, record a `Provenance{Span, Snippet}` entry (position resolved to a serializable `Span{File, Line, Col, Length}` DTO at construction). Render as a secondary `= note: constraint introduced at <file:line:col>` footer, at most 3 per diagnostic. If `Pos()` is invalid for all conjuncts, drop the footer silently. Attached to footer Labels via `Label.Reason`, so the renderer type-switch handles Provenance and the six failure reasons uniformly.
- **Alternatives:**
  - *Full derivation tree* — accurate but too heavy for a debug footer.
  - *Skip entirely for v1* — leaves the most "inference-aware" feel on the floor.

### Constraints

- **Zero cost on the fast path.** Reason construction runs inside `localize`, which is gated by `explainEnabled`. The fast-path `Subsume` call at `localize.go:74` must remain unchanged in hot loops.
- **NF3 compliance from v0 holds.** No panics. Missing positions → degraded-but-rendered output.
- **Deterministic output across runs.** Arm ranking ties break by source order; provenance footer sorts by file then line.
- **No new external dependencies.** SARIF emitter is hand-rolled against the JSON schema (≈200 LOC); color uses stdlib ANSI codes; isatty detection via `golang.org/x/term` (already transitive).
- **Codes stable.** This scope allocates **no new codes**. E0301/E0302/E0303/E0304 are already allocated (regex / bound / type / enum) by the shipped `diag-errors` scope; we populate their diagnostics with Reason payloads rather than introducing new codes. If E0303's current help text needs revision, the edit goes in this scope — but the code identifier itself is stable from v0.

## Requirements

### Functional

- **F1 — `diag.Reason` sum type and Label enrichment.** `internal/diag` exports a sealed `Reason` interface with variants enumerated in AD-1. `Label` gains a `Reasons []Reason` field; zero-length Reasons falls through to legacy `Msg` rendering (NF5). Multiplicity lives here, not inside a Reason variant.
- **F2 — Use existing E0303 for KindMismatch.** No new code allocation. `KindMismatch` Reasons are attached to E0303 (type mismatch) diagnostics; `BoundViolation` Reasons attach to E0302 (value out of range); `RegexMismatch` Reasons attach to E0301 (regex). Verify each existing code's help text is still accurate in light of the new Reason-bearing output; revise where the help materially diverges from what the new renderer produces.
- **F3 — Localize rewrite (leaf path).** Replace the opaque Subsume-discard at `localize.go:74` with `cue.Value.Expr()` decomposition:
  - Kind mismatch before value → `KindMismatch` reason, E0303 diagnostic.
  - Each failing conjunct of `A & B & C` → one `ConjunctFailed{Expr, Span, Sub: <inner reason>}` entry. Multiple failing conjuncts at the same leaf → the Label's `Reasons` slice carries all entries in source order; the Reason variants themselves stay singular.
  - Bound failure → `BoundViolation{Op, Bound, Actual, Distance}` at E0302; distance pre-formatted per kind (numeric subtraction for int/float, "N chars short" for string length bounds).
  - Regex failure → `RegexMismatch{Pattern, Input, DivergeAt}` at E0301; `DivergeAt` computed via the strategy in the gotchas section (no hand-rolled matcher).
  - Code selection when a Label carries multiple Reasons of mixed type (e.g. a bound + a kind failure at the same leaf): highest-priority code wins — kind > regex > bound > enum. The Label's `Reasons` slice still carries every entry; the diagnostic code is chosen from among them.
- **F4 — Localize rewrite (disjunction).** `E0401` emits `DisjunctionFailed{Arms []ArmResult}` where each `ArmResult` carries the arm's position, its inner reason, and a closeness score per AD-3. Primary message becomes "closest arm was `<arm>`, failed at `<sub-path>`".
- **F5 — Absent-key did-you-mean.** `E0201` gains `KeyMissing{Key, AvailableKeys, Suggestion}`. `Suggestion` = closest string in `AvailableKeys` by Levenshtein, distance ≤ 2, else empty. Renderer surfaces as `= hint: did you mean "..."?`. **Empty-parent case:** when `AvailableKeys` is empty, Suggestion is vacuously `""` (no hint), and the `= help:` footer replaces the empty "has keys: " list with `parent at <path> is an empty struct` — more informative than silent suppression.
- **F6 — Provenance enrichment.** `localize` walks `ruleNext.Expr()` conjuncts; records `Provenance{Pos, Snippet}` entries for conjuncts whose `Pos().Filename()` differs from `f.Value.Pos().Filename()`. Max 3 entries per diagnostic, sorted by file then line. Render as footer notes.
- **F7 — Renderer: no-restate rule.** Per `feedback_diag_no_restate.md`: drop Primary `Msg` when it duplicates the title; `want:` gate is purely AST-based (NOT span-text-based).
  - **Cheap gate:** emit `want:` iff `f.Value` is `*ast.Ident` or `*ast.SelectorExpr` — those are exactly the CUE reference shapes; literal constraints (`*ast.BasicLit`, `*ast.UnaryExpr` for `>=5` / `=~"..."` / `!=7`, `*ast.BinaryExpr` for `int & >=0` / `A | B`) never trigger it.
  - **Stronger gate** (runs when cheap gate didn't fire): emit `want:` iff `formatted(ruleNext.Expr()) != formatted(f.Value)` using `cue/format.Node`. Catches unification narrowing — source says `int`, resolved constraint is `int & >=0` from a stdlib import, formatted forms differ, `want:` fires with the expanded form.
  - No heuristic on span text (would misfire on regexes like `=~"foo\.bar"`).
  Collapse same-span labels: when a Note shares `Pos`+`Len` with the Primary or a prior Note, skip the snippet/caret lines, emit only an aligned message row.
- **F8 — Renderer: per-Reason text formatting.** Type-switch on `Label.Reason` produces idiomatic text:
  - `KindMismatch` → `expected <Want-kind>, got <Got-kind>: <Actual>` on the caret line (kind name *and* concrete value, since the kind alone is usually not enough to debug).
  - `BoundViolation` → `<actual> violates <op> <bound> (off by <distance>)`.
  - `RegexMismatch` → secondary snippet echoes the input with carets under `DivergeAt`, budget ≈ 60 chars total centred on the divergence byte (≥ 20 chars of context on each side when available). When the input exceeds the budget, trimmed edges render as a single `…` and the caret offset shifts by the prefix-trim amount so the marker still points at the correct byte. Tabs expand before measurement. Footer `regex first diverged at offset N`.
  - `ConjunctFailed` → underline the failing conjunct span specifically, not the whole `A & B & C`. Multiplicity at a single leaf is handled by the Label carrying multiple `ConjunctFailed` entries in `Reasons`; the variant itself stays singular.
  - **Labels with `len(Reasons) >= 2`** → iterate `Reasons`; first entry renders on the Label's caret row (message suffix), subsequent entries stack as aligned message rows beneath the same caret via the T10 same-span collapse. JSON/SARIF emit `label.reasons` as an array; consumers iterate.
  - `DisjunctionFailed` → primary underlines full OR chain; one note per arm with rank score hidden but ordered by rank. **No-close-arm case:** when `Arms[0].Score` is below `ScoreKindMatch` (i.e. no arm even shares kind with the input), suppress ranked caret frames entirely and render a flat summary: primary note "got `<Actual>` — no arm was close", plus a footer `= note: tried arms: <arm₀>, <arm₁>, ...`. `ArmResult.Score` is still populated in the model and marshalled to JSON/SARIF — suppression is a text-renderer decision only.
- **F9 — JSON output (`--format=json`).** Marshalls the full Reason tree. Schema is stable (documented in `internal/diag/json_schema.md`). One JSON object per diagnostic; ND-JSON on stderr when multiple diagnostics.
- **F10 — SARIF output (`--format=sarif`).** Emits SARIF 2.1.0 with `runs[].results[]` per diagnostic, `logicalLocations` for rule IDs, `relatedLocations` for provenance entries. Hand-rolled emitter (no new deps).
- **F11 — Color TTY.** When stdout/stderr is a TTY and `NO_COLOR` is unset, emit ANSI color: severity-word colored (red/yellow/cyan), caret line colored, file:line:col dimmed. `--color=auto|always|never` flag overrides detection.
- **F12 — CLI surface.**
  - `--format=text|json|sarif` on `fas eval` and `fas explain` (default `text`).
  - `FAS_FORMAT` env var mirrors the flag.
  - `--color=auto|always|never` on both commands.
  - `FAS_COLOR` env var mirrors the flag; the standard `NO_COLOR` env (community convention) forces disable. No separate `QUAE_NO_COLOR` — `FAS_COLOR=never` is the fas-specific override.
- **F13 — Per-rule diagnostic policy (inherited from v0, made explicit).** A single rule whose `when` has multiple independent mismatches yields multiple diagnostics. The policy:
  - **One diagnostic per failing yield point** — an absent path segment (E0201), a failed leaf (E0301/E0302/E0303/E0304), or a failed disjunction at a leaf (E0401).
  - **Siblings are independent.** Two absent keys under the same parent → two E0201 diagnostics.
  - **Structural failure blocks descent.** If path `a.b` is absent, nothing is emitted for `a.b.c` — there's nothing to walk into.
  - **Multi-conjunct failure at a single leaf lives *within* one diagnostic** — the single Label for that leaf carries multiple `ConjunctFailed` entries in its `Reasons` slice. Multi-failure richness is intra-diagnostic (one Label, multiple Reasons); multi-leaf failure is inter-diagnostic (multiple diagnostics).
  - **No cap, no dedup.** Output order is the source-order walk — deterministic, stable across runs.
  - **Regression guard:** `tests/diagnostics.md` has `absent_path.cue` already exercising multi-failure per rule. The rewrite in T17 preserves this.

### Non-functional

- **NF1 — Fast path unchanged.** `explainEnabled()` false → no Reason construction, no Expr() walks, zero allocations beyond v0.
- **NF2 — Deterministic output.** Identical inputs → byte-identical output across text/JSON/SARIF.
- **NF3 — No panics.** Any invalid position, unexpected Op, or nil Reason degrades to v0-style fallback text, never crashes.
- **NF4 — No new module deps.** Isatty via `golang.org/x/term` only if not already transitive; otherwise hand-rolled.
- **NF5 — Backwards compatibility.** v0 diagnostics (those still using `Label.Msg` only) continue to render identically through the new renderer — migration is per-code, not big-bang.

## Out of scope

- IDE / LSP integration — this scope produces the JSON/SARIF, consumers are separate.
- CUE `cue.Attribute` parsing for rule-author `@hint("…")` — follow-on once the Reason tree lands.
- Cross-file / cross-rule conflict diagnostics — single-rule localization only (same as v0).
- Evaluator semantics changes — none. The fast-path contract is identical.
- Rendering localization / i18n — English only.

## Verification

### Acceptance criteria

- [ ] **Given** a rule with `tool_input: command: =~"^rm "` and input `command: "ls"`, **when** `fas explain` runs, **then** output omits "constraint not satisfied" and `want: =~"^rm "` (both carried by title+carets), shows only `got: "ls"`, and has the source line printed exactly once (no 3× stacking).
- [ ] **Given** the same rule but constraint is a reference `tool_input: command: #DangerousCmds`, **when** `fas explain` runs, **then** `want:` *is* shown with the expanded constraint.
- [ ] **Given** a rule `count: int & >=5 & <=10` and input `count: 3`, **when** `fas explain` runs, **then** output underlines the `>=5` conjunct specifically (not the whole `int & >=5 & <=10`), message reads `3 violates >= 5 (off by 2)`. Label carries exactly one `ConjunctFailed` entry in `Reasons`.
- [ ] **Given** a rule `x: string & =~"^[a-z]+$" & strings.MinRunes(5)` and input `x: "AB"`, **when** `fas explain` runs, **then** the Label's `Reasons` slice contains two `ConjunctFailed` entries (regex, then min-runes, source order); primary caret row carries the regex failure message, a stacked aligned row beneath carries the min-runes failure message (T10 collapse); JSON emits `label.reasons` as a 2-element array.
- [ ] **Given** the same rule but only one conjunct fails (input `x: "abcdefg"` passes min-runes, fails some other check), **when** `fas explain` runs, **then** `Reasons` has length 1 and only one message row renders.
- [ ] **Given** a rule `tool_name: "Bash" | "Read" | "Write"` and input `tool_name: "Rd"`, **when** `fas explain` runs, **then** primary message reads "closest arm was `\"Read\"`" (Levenshtein-closest).
- [ ] **Given** a rule requiring `flags.force` and input has keys `flag`, `forced`, **when** `fas explain` runs, **then** E0201 footer shows `= hint: did you mean "flag"?` (closest by edit distance).
- [ ] **Given** a rule field constrained by `int & stdlib.Positive` where `Positive` is `>=0` defined in `stdlib/nums.cue`, **when** `fas explain` runs, **then** diagnostic footer shows `= note: constraint introduced at stdlib/nums.cue:<line>`.
- [ ] **Given** input `count: "five"` against `count: int`, **when** `fas explain` runs, **then** diagnostic is E0303 with primary message `expected int, got string: "five"` (Want kind name, Got kind name, then the concrete value).
- [ ] **Given** `fas eval --format=json < input.json`, **when** a rule fails, **then** stderr contains one JSON object per diagnostic with `code`, `severity`, `primary`, `reason.type`, `reason.*` fields.
- [ ] **Given** `fas eval --format=sarif < input.json`, **when** a rule fails, **then** stderr contains a SARIF 2.1.0 document validatable against the published schema.
- [ ] **Given** `fas eval --explain` from a TTY with `NO_COLOR` unset, **when** run, **then** output contains ANSI color codes for severity, caret, and location.
- [ ] **Given** the same command with `NO_COLOR=1`, **when** run, **then** output contains no ANSI codes.
- [ ] **Given** `--format=text` + `--color=never` + same inputs, **when** run twice, **then** output is byte-identical.
- [ ] **Given** a non-firing rule with `explainEnabled=false`, **when** `Evaluate` runs, **then** no Reason construction occurs (verified via a `testing.AllocsPerRun` or explicit hook counter).
- [ ] **Given** input `command: "rm -xf $(cat /etc/passwd | base64 | tr …)"` (80+ chars) failing regex `=~"^rm -rf "`, **when** `fas explain` runs, **then** the `= input` echo renders as a single line ≤ 60 chars with `…` trim markers and the caret correctly aligned under the divergence byte (offset 4 in the original input; offset is preserved, display position compensates for the prefix trim).
- [ ] **Given** input `tool_name: "XYZ123"` against disjunction `"Bash" | "Read" | "Write" | "Edit"` (all kinds string — actually kind-matched, but no arm within distance 2), **when** `fas explain` runs, **then** ranked caret frames still render (kind matches → `Score ≥ ScoreKindMatch`).
- [ ] **Given** input `tool_name: true` (bool) against the same string disjunction, **when** `fas explain` runs, **then** no ranked caret frames render; output is a flat summary "got `true` — no arm was close" plus a footer `= note: tried arms: "Bash", "Read", "Write", "Edit"`.
- [ ] **Given** a rule requiring `container.port` and input `{}` (empty struct at the parent path), **when** `fas explain` runs, **then** E0201 footer reads `= help: parent at <root> is an empty struct` (not `has keys: ` with an empty list) and no `= hint:` line appears.
- [ ] **Given** a rule's `f.Value` is a literal `int & >=0` and the resolved `ruleNext.Expr()` formats to the same `int & >=0`, **when** the constraint fails, **then** `want:` is NOT emitted (stronger gate returns formatted-equal).
- [ ] **Given** a rule's `f.Value` is `int` and unification with a stdlib import narrowed `ruleNext` such that formatted Expr is `int & >=0`, **when** the constraint fails, **then** `want:` IS emitted with the expanded form `int & >=0`.
- [ ] **Given** a single rule whose `when` has two absent keys under the same parent AND a leaf regex failure, **when** `Evaluate --explain` runs, **then** exactly three diagnostics are emitted for that rule (two E0201, one E0301), in source-walk order, with no dedup and no cap.

### Rendered examples (visual acceptance targets)

These are the concrete text-renderer targets for the new Reason variants.
They pair with the Given/When/Then criteria above: criteria describe *what*
must hold, these show *what it looks like*. Scrut goldens in
`tests/diagnostics.md` must match these shapes byte-for-byte.

All examples assume `--format=text --color=never`; color path is additive.
Same-span collapse, no-restate, and conditional `want:` rules apply
throughout (see `feedback_diag_no_restate.md`).

---

**Ex. 1 — Regex divergence (`RegexMismatch`)**

Rule snippet at `/__fas_rules__/bash_guard.cue`:
```
15 |     tool_input: command: =~"^rm -rf "
```
Input: `{"tool_input":{"command":"rm -xf /tmp"}}`

Target output:
```
error[E0301]: leaf constraint failed
  --> /__fas_rules__/bash_guard.cue:15:26
   |
15 |     tool_input: command: =~"^rm -rf "
   |                          ^^^^^^^^^^^^ got: "rm -xf /tmp"
   |
   = input  "rm -xf /tmp"
                 ^ regex diverged here (offset 4; expected 'r', got 'x')
```
Demonstrates: input-echo as a secondary snippet frame, caret under
`DivergeAt=4` byte offset, no `want:` (literal pattern already underlined
by the primary caret row).

---

**Ex. 2 — Conjunct decomposition + bound distance (`ConjunctFailed` ⊃ `BoundViolation`)**

Rule snippet at `/__fas_rules__/limits.cue`:
```
 8 |     retry_count: int & >=5 & <=10 & !=7
```
Input: `{"retry_count":12}`

Target output:
```
error[E0301]: leaf constraint failed
  --> /__fas_rules__/limits.cue:8:30
   |
 8 |     retry_count: int & >=5 & <=10 & !=7
   |                              ^^^^ 12 violates <= 10 (off by 2)
```
Demonstrates: primary caret on the specific failing conjunct (`<=10`), not
the whole `int & >=5 & <=10 & !=7`. Message comes from
`BoundViolation{Op:"<=", Bound:"10", Actual:"12", Distance:"off by 2"}`.
No "constraint not satisfied" restatement.

---

**Ex. 3 — Ranked disjunction arms (`DisjunctionFailed`)**

Rule snippet at `/__fas_rules__/tool_gate.cue`:
```
10 |     tool_name: "Bash" | "Read" | "Write" | "Edit"
```
Input: `{"tool_name":"Rd"}`

Target output:
```
error[E0401]: no disjunction arm matched
  --> /__fas_rules__/tool_gate.cue:10:14
   |
10 |     tool_name: "Bash" | "Read" | "Write" | "Edit"
   |                ^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^ got "Rd" — closest arm was "Read"
   |
10 |     tool_name: "Bash" | "Read" | "Write" | "Edit"
   |                         ^^^^^^ edit distance 2 (rank 1)
   |
10 |     tool_name: "Bash" | "Read" | "Write" | "Edit"
   |                                            ^^^^^^ edit distance 3 (rank 2)
```
Demonstrates: 3-tier ranking (kind → structural → value distance) puts
`"Read"` at rank 1, `"Edit"` at rank 2. Source order breaks ties
deterministically. Top 3 arms shown (cap configurable, rendered default = 3).

---

**Ex. 4 — Did-you-mean for absent key (`KeyMissing.Suggestion`)**

Rule snippet at `/__fas_rules__/confirm.cue`:
```
12 |     flags: force: true
```
Input: `{"flag":true,"forced":false}`

Target output:
```
error[E0201]: key not found
  --> /__fas_rules__/confirm.cue:12:5
   |
12 |     flags: force: true
   |     ^^^^^ key "flags" not found in input at path <root>
   |
   = help: input.<root> has keys: flag, forced
   = hint: did you mean "flag"?
```
Demonstrates: new `= hint:` footer when Levenshtein distance ≤ 2. `flag`
(distance 1) wins over `forced` (distance 3). Silent (no hint) when no key
is within distance 2.

---

**Ex. 5 — Kind mismatch (`E0303` + `KindMismatch`) with cross-file provenance (`Provenance`)**

Rule snippet at `/__fas_rules__/retry.cue`:
```
 8 |     retry: count: int & stdlib.Positive
```
Where `stdlib.Positive: int & >=0` is defined at `stdlib/nums.cue:7`.
Input: `{"retry":{"count":"three"}}`

Target output:
```
error[E0303]: type mismatch
  --> /__fas_rules__/retry.cue:8:21
   |
 8 |     retry: count: int & stdlib.Positive
   |                   ^^^^^^^^^^^^^^^^^^^^^ expected int, got string: "three"
   |
   = want: int & >=0
   = note: constraint introduced at stdlib/nums.cue:7:17
   = help: no value of kind `string` can satisfy a constraint of kind `int`
```
Demonstrates: existing `E0303` (type mismatch, already allocated by
`diag-errors` v0) now carries a `KindMismatch` Reason payload — no new
code. Primary caret-line format is `expected <Want>, got <Got>: <Actual>`
so the reader sees *both* the kind name and the concrete value.
`want:` *is* emitted because the caret span contains a reference
(`stdlib.Positive`) — the expanded form carries information unavailable from
the source snippet. `= note:` footer surfaces provenance (cross-file
origin walked via `cue.Value.Expr()` conjuncts). `= help:` is kind-aware,
explaining the lattice disjointness.

### Commands

```
go test ./internal/diag/... ./internal/evaluator/... ./cmd/fas/...
go test ./...
scrut test tests/diagnostics.md           # existing goldens, rewritten to minimal form
scrut test tests/diagnostics_formats.md   # NEW — JSON / SARIF / color surfaces
```

## Gotchas

- **`cue.Value.Expr()` returns syntactic AND semantic operands interleaved.** When unification introduces a conjunct, `Expr()` may return operands whose `Pos()` lives in the stdlib file, not the rule file. Exploit this for provenance — but also filter duplicates when the same conjunct appears at multiple levels of nesting.
- **`cue.Value.Pos()` returns `token.NoPos` more often than one expects** — for builtin kinds (`int`, `string`), for conjuncts born from a disjunction closure, for unified stdlib values resolved via `load.Instances`. Every render path must handle `!pos.IsValid()`.
- **Regex `DivergeAt` — no hand-rolled matcher.** Go's `regexp` doesn't expose partial-match offsets, but we do **not** re-implement matching. Strategy: parse the pattern with `regexp/syntax.Parse` to get its AST, use the AST *only* to enumerate cut points in a top-level concatenation (atoms: literal runs, char classes, anchors, simple quantifiers), then synthesize anchored prefix-patterns `Pᵢ = concat(r₀..rᵢ)` and match each with Go's stdlib `regexp.FindStringIndex` against the input. The largest `i` with a non-nil match wins; the match's end byte offset is `DivergeAt`. Go's engine does all matching — we use `syntax` purely as a structural parser. Complex patterns (top-level `|`, nested quantified groups, lookarounds, anything non-concat at the root) bail to `DivergeAt=-1` and the renderer falls back to "regex did not match". No attempt to "almost" handle these; the gate is intentionally strict.
- **SARIF's `logicalLocations` and `relatedLocations` have subtle nesting rules.** Validate early against the schema; don't ship an emitter untested against a real SARIF consumer.

## Open questions

- [ ] Should `BoundViolation.Distance` be a typed `cue.Value` (preserves kind) or a pre-formatted string (simpler renderer)? Leaning pre-formatted — renderer stays pure, JSON schema stays flat.
- [ ] Cap on `Provenance` entries: 3 sorted by pos, or 3 sorted by "most relevant" (some heuristic)? v1 = 3 by pos; revisit if UX feels off.
- [ ] Should `--format=json` emit ND-JSON or a single top-level array? ND-JSON is friendlier to streaming; array is friendlier to `jq`. Leaning ND-JSON.
