---
scope: cue-native-diagnostics
created: 2026-04-23
---

# Design — CUE-native Reason model

## Problem statement

`diag.Label.Msg` is an opaque string. Everything downstream (text renderer,
any future JSON/SARIF consumer, any tool that wants to group or filter
diagnostics) has to parse that string back into structure it never should
have been flattened out of. v0 `localize.go` also *throws away* the
structured `cue.Error` returned by `Subsume`, re-formatting a best-guess
string. The result: diagnostics can't reason about kind lattice position,
can't decompose `A & B & C` into which conjunct failed, can't rank
disjunction arms, and can't surface cross-file provenance when a constraint
was introduced via unification.

This design introduces a typed `Reason` payload alongside `Msg`, populated
by walking `cue.Value.Expr()` in `localize`, and consumed by format-specific
renderers (text / JSON / SARIF / color TTY) via a single type-switch dispatch.

## Invariants

- **I1.** A nil `Reason` is valid and means "use legacy `Msg` rendering." No
  call site is required to populate `Reason`. Migration is per-code.
- **I2.** Reason construction happens only when `explainEnabled()` is true.
  The fast path touches no Reason code, pays no Reason cost.
- **I3.** Every renderer (text, JSON, SARIF, color) exhausts the same sealed
  set of Reason variants. Adding a variant requires updating every renderer
  — the unexported `reason()` seal makes this enforceable at review time.
- **I3a.** Each singular variant describes exactly one shape. Multiplicity
  at a single yield point is expressed by the Label — `Label.Reasons` is
  `[]Reason` — never by growing singular variants into collections or by
  introducing wrapper variants. Preserves the property that a reader
  inspecting (say) `ConjunctFailed` knows it refers to one conjunct; when
  several conjuncts fail at the same leaf, the Label carries multiple
  singular `ConjunctFailed` entries in its `Reasons` slice.
- **I4.** Any renderer given a Reason whose positions resolve to `token.NoPos`
  or invalid files degrades gracefully, never panics (NF3 carry-over).
- **I5.** Identical inputs produce byte-identical output for text / JSON /
  SARIF (NF2). Any heuristic with ties (arm ranking, provenance ordering)
  breaks them deterministically by source position.

## Alternatives considered

### A1 — Tagged struct with Kind enum instead of sealed interface

```go
type Reason struct {
    Kind ReasonKind
    // ... all possible fields, nullable
    KindWant, KindGot cue.Kind
    BoundOp string
    BoundBound, BoundActual string
    // ...
}
```

**Pros:** Single concrete type; marshals trivially; no type-switch.

**Cons:** Every consumer must branch on `Kind` and know which fields to
read for which kind; no compile-time exhaustiveness; adding a variant silently
breaks consumers that forgot to update their switch; the "nullable fields"
pattern invites confusion about which fields are load-bearing for which kind.

**Rejection:** Loses the main benefit of the typed model — making the renderer
code-review-enforceable when variants are added.

### A2 — Replace `Label.Msg` entirely, big-bang migration

**Pros:** Clean end-state; no "two ways to carry information" ambiguity.

**Cons:** Forces every existing Diagnostic emission site to populate a Reason
in one commit. The `ruleLoadError` bridge (`diag.FromCueError`) doesn't
naturally map to any specific Reason variant — CUE's load errors are more
granular than the 7 variants we enumerate. Migration becomes a blocker
rather than a per-code choice.

**Rejection:** Preserves optionality. We can migrate leaf/disjunction first
(biggest UX wins), leave loader-bridge diagnostics on `Msg` indefinitely, and
revisit once the shape stabilizes.

### A3 — Double-dispatch via Visitor interface on Reason

```go
type Reason interface {
    AcceptText(TextVisitor) string
    AcceptJSON(JSONVisitor) json.RawMessage
    AcceptSARIF(SARIFVisitor) sarif.Result
}
```

**Pros:** Each variant carries its own rendering; no central switch.

**Cons:** Adding a new format requires adding a method to every variant.
Adding a new variant requires every format's visitor to grow a method.
That's N×M methods for N variants × M formats, when the single-switch
pattern is N+M (one switch per format, one variant per reason).

**Rejection:** We expect more formats (LSP diagnostics, plain-GitHub, plain-
CircleCI annotations) than variants. One switch per format scales better.

### A4 — SAT-style minimal-diff solver for arm ranking

**Pros:** Arm ranking becomes "minimal edit to make input satisfy arm X",
which is theoretically the right answer.

**Cons:** Expensive (involves unification and value-space search); adds a
runtime-failure surface; overkill for rule-author debugging where the author
usually already knows the schema and just mistyped a field.

**Rejection:** 3-tier heuristic (kind > structural > value distance) catches
the cases that matter (typo of a string enum, wrong field in a struct, out-
of-range number) at O(n) cost, and falls back to source order on ties.

## Reason variants — shape and rationale

**Invariant:** every Reason field is a JSON-serializable primitive or DTO.
No `ast.Expr`, no `token.Pos`, no `cue.Value`. AST/position data is resolved
to strings+spans at Reason construction time inside `localize`. This is what
makes T13's "marshal → unmarshal → remarshal byte-identical" requirement
achievable.

```go
type Reason interface {
    reason() // sealed
}

// Shared DTO — replaces every ast.Expr / token.Pos in the model.
type Span struct {
    File   string
    Line   int
    Col    int
    Length int
}

// ----- Failure reasons (carried on Primary/Note labels) -----

type KindMismatch struct {
    Want, Got cue.Kind // cue.Kind is int; JSON-stable via string-tag at marshal layer
    Actual    string   // concrete input value, pre-formatted (e.g. `"three"`)
}

type BoundViolation struct {
    Op            string // ">=", "<=", ">", "<", "!="
    Bound, Actual string // pre-formatted
    Distance      string // "off by 2", "", etc.
}

type RegexMismatch struct {
    Pattern, Input string
    DivergeAt      int // byte offset; -1 if unavailable
}

type ConjunctFailed struct {
    Expr string // formatted source of the failing conjunct
    Span Span   // position of the conjunct for caret underlining
    Sub  Reason // nested: BoundViolation, RegexMismatch, etc.
}

type ArmResult struct {
    Arm   string // formatted source of the arm
    Span  Span   // position of the arm for caret underlining
    Inner Reason // why this arm failed
    Score int    // higher = closer; hidden from renderer output, controls order
}

type DisjunctionFailed struct {
    Arms []ArmResult // sorted by Score desc, source order on ties
}

type KeyMissing struct {
    Key           string
    AvailableKeys []string
    Suggestion    string // "" if no close match (Levenshtein > 2)
}

// ----- Metadata reasons (carried on footer labels) -----

type Provenance struct {
    Span    Span
    Snippet string
}
```

### Where multiplicity lives: slice on Label, not wrapper variant

We rejected two earlier candidates before settling here:

- **Make `ConjunctFailed.Sub` a `[]Reason`.** Silently breaks sum-type
  purity — a reader seeing `ConjunctFailed` could no longer trust that
  the variant describes exactly one failing conjunct. Rejected.
- **Introduce a `Multi{Reasons []Reason}` wrapper variant.** Preserves
  singular-variant purity but adds a variant whose purpose is *only* to
  hold a slice, and pays renderer dispatch cost at every format for a
  problem that exists at one site. Moves the slice somewhere less
  convenient while calling it a design pattern. Rejected.

Settled design: `Label.Reasons []Reason`. Rationale:

- Multiplicity is a property of the **attachment point** — a Label — not
  a property of a Reason. The slice belongs where it's semantically owned.
- Each singular variant stays singular (I3a) — `ConjunctFailed` means one
  conjunct failed, period.
- Renderers just iterate `label.Reasons` — no "Multi case" in the
  type-switch. For `len(Reasons) >= 2`, the first entry renders on the
  caret row, subsequent entries stack as aligned message rows (T10
  same-span collapse applies identically to how multiple same-span Notes
  would collapse).
- JSON becomes `{... "reasons": [...]}` — flatter schema, consumers
  iterate the array.
- `len(Reasons) == 0` is the v0-compat fallthrough (renderer uses `Msg`).
  `len(Reasons) == 1` is the common case.

Nesting concern: `ConjunctFailed.Sub` and `ArmResult.Inner` are both
semantically singular in current scope (one conjunct's failure reason,
one arm's failure reason). If a future site ever needs plurality there,
*that* site gains its own slice field — additive, non-breaking.

Design notes:

- **Pre-formatted strings in `BoundViolation`** (`Bound`, `Actual`,
  `Distance`) rather than typed `cue.Value` — keeps the Reason serializable
  and the distance formula is kind-specific ("off by 2" for int, "2 chars
  short" for string length) which is a rendering concern, not a data
  concern.

- **`ConjunctFailed.Expr`/`Span` are captured at construction**. The
  `localize` walker has the `ast.Expr` at hand and resolves it via
  `format.Node(expr)` + `expr.Pos()` → `Span{File: pos.Filename(), Line:
  pos.Line(), Col: pos.Column(), Length: exprLen(expr)}`. Renderers never
  see the AST — they see strings.

- **`ArmResult.Arm`/`Span`** likewise — AST resolved at construction.

- **`ConjunctFailed.Sub` is a nested Reason**, not a flat list, because
  `A & (B | C)` means "A failed, OR the (B | C) sub-disjunction failed" —
  recursion is the natural shape. Bounded depth in practice (rule complexity
  is shallow), but guarded in tests.

- **`ArmResult.Score` is internal** — renderers see the already-ordered
  `Arms []ArmResult` and render them in the given order. The score itself
  is not user-facing (reveals heuristic internals, changes between versions).

- **`Provenance` is a "metadata reason".** It decorates a footer label
  rather than describing why a constraint failed. Carried on `Label.Reason`
  alongside the failure reasons because the renderer dispatch treats them
  uniformly (one type-switch, one set of format-specific handlers). The
  separation is taxonomic (AD-1 lists them in two groups), not structural.

- **`cue.Kind` in `KindMismatch`** is the CUE API's int-typed kind. The Go
  struct holds it as `cue.Kind`; the JSON marshaller emits a string tag
  (e.g. `"int"`, `"string"`) alongside the raw number so rename-safety
  holds across CUE version upgrades. Tag mapping lives in the JSON layer,
  not the Go struct — the struct stays idiomatic.

## Renderer dispatch architecture

Each format is a package-level function:

```go
// text
func renderReasonText(r Reason, col int) (msg string, carets int, caretOffset int)

// json
func reasonToJSON(r Reason) any

// sarif
func reasonToSARIF(r Reason, loc sarif.Location) sarif.Message
```

Each contains one `switch r := r.(type)` covering all 7 variants explicitly,
plus a `default:` that returns the zero / fallback output (enforcing I4).
Adding a variant to the sealed interface without updating every renderer
fails review — the default cases are tombstones.

`Label` renders via:

1. If `Reason != nil`: dispatch to the format's `renderReason*`.
2. Else: fall through to legacy `Msg` path (I1).

Color is not a separate renderer — it's a `Palette` injected into the text
renderer. `RenderJSON` and `RenderSARIF` never color.

## Fast path analysis

v0 fast path, non-matching rule, `explainEnabled=false`:

1. `Subsume(rule, input)` returns non-nil
2. `evaluator.Evaluate` skips `localize` (per T6 from diag-errors scope)
3. Return `([]Match{...}, nil, nil)` — diagnostics lane is nil

v1 fast path, same conditions: identical. No Reason code runs, no Expr()
walks, no allocations beyond v0.

Only `explainEnabled=true` pays the Reason cost. Verified in T3-T9 task tests
via allocation counters or explicit localize-hook counters.

## Complexity

| Step | Big-O | Notes |
|------|-------|-------|
| Expr() decomposition | O(conjuncts + disjuncts) | Linear in expression size |
| Bound distance | O(1) | Subtraction / strlen |
| Regex diverge | O(pattern × input) worst case | Via syntax.Regexp walk |
| Arm ranking | O(arms × score-cost) | score-cost is O(1) for scalar arms, O(fields) for struct arms |
| Levenshtein did-you-mean | O(keys × key-len²) | `len(keys)` is typically 1-20; key-len < 30 chars |
| Provenance walk | O(conjuncts) | Capped at 3 entries |

All steps run only when `explainEnabled=true`. Total per-diagnostic cost is
dominated by regex diverge for regex-heavy rules, otherwise linear in the
constraint's AST size.

## Migration plan within this scope

v0 diagnostics are emitted by 3 code sites:
1. `localize.go` (evaluator) — migrated in T3-T9 (all reasons populated).
2. `loader.go` / `cuebridge.go` (load errors) — **not migrated this scope**;
   continue to emit `Label.Msg` only. Reason slot stays nil, renderer falls
   through to legacy path. Future scope if needed.
3. `lint.go` (lint diagnostics) — **not migrated this scope**; same
   reasoning. Lint rejections are typically single-label, single-span;
   Reason adds less value.

Benefit of partial migration: we ship a meaningful slice (eval-time debugging
is the highest-leverage UX) without churning the loader and lint paths, which
have their own quirks (error aggregation via `errors.Join`, CUE's own error
chain).

## Risks

- **SARIF schema drift.** SARIF 2.1.0 is stable, but consumer tools (GitHub,
  SonarQube) sometimes expect optional fields. Mitigation: test against a
  real consumer (GitHub Code Scanning) as part of T14 acceptance.
- **Regex divergence offset surprises.** Our offset is byte-based, but users
  think character-based. Mitigation: document in help text for E0301; include
  unicode input in T6 tests.
- **Regex divergence — no in-house matcher.** `DivergeAt` is computed via
  `regexp/syntax.Parse` (structural parser only, used for cut-point
  enumeration) + Go's stdlib `regexp.FindStringIndex` (does all actual
  matching against the real input). We never re-implement regex semantics.
  Patterns whose `syntax.Regexp` root is not a concatenation, or that use
  alternation / nested quantified groups / lookarounds, return
  `DivergeAt = -1` immediately — the renderer falls back to "regex did not
  match" without carets. Deliberate correctness-over-coverage choice:
  misaligned carets are worse than absent ones.
- **Provenance noise on heavily-unified fields.** If a single field unifies
  10 conjuncts across 5 files, showing 3 footers feels arbitrary. Mitigation:
  cap is explicit; ordering is deterministic; revisit after real rules with
  heavy unification exist in the wild.
- **Arm ranking misranking.** If the "closest" arm by our heuristic isn't the
  one the author intended, they waste time. Mitigation: show multiple ranked
  arms (not just the closest); source order breaks ties.
