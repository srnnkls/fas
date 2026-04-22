---
created: 2026-04-22
status: draft
---

# Design — Compiler-style diagnostics

## Core principle

**Errors are source code, not strings.** Every diagnostic carries positions at the finest granularity that matters (path segment, constraint leaf, disjunction arm). Rendering is a view over structured data, not a formatted string constructed at the error site.

> **Note on path references.** Rules are patterns — the `when` block describes the shape the input must have. There is no `$input` binding; the rule's nested struct IS the reference to the input. Diagnostics describe mismatches as "path X in the input does not satisfy rule constraint Y" — the path is derived from walking the `when` AST, not from a user-facing sigil.

## The `Diagnostic` type

```go
// internal/diag/diagnostic.go

type Severity int

const (
    SeverityError Severity = iota
    SeverityWarning
    SeverityNote
)

type Diagnostic struct {
    Code     string      // "E0201"
    Severity Severity
    Title    string      // short, one line: "key not found"
    Primary  Label       // main span — the caret location
    Notes    []Label     // secondary spans + inline messages
    Help     string      // optional trailing "= help: ..." line
}

type Label struct {
    Pos  token.Pos   // cue/token.Pos — file + offset
    Len  int         // byte width of the underlined span
    Msg  string      // inline message rendered next to the caret
}
```

Diagnostics are values, not errors. The internal API returns `(T, []Diagnostic)` — plain slice, no error interface. An empty/nil slice means "no observations," not "error state."

Where a diagnostic must cross Go's `error` interface (the `loader.go` boundary, where stdlib callers expect `error`), wrap it in a narrow adapter: `type DiagError struct{ D Diagnostic }`. `errors.As(err, &dErr)` recovers the structured diagnostic for rendering. This adapter exists at one boundary, not throughout the package.

## Three semantic lanes

The evaluator has one entry point, three orthogonal return lanes:

```go
func Evaluate(rules []Rule, input cue.Value) ([]Match, []Diagnostic, error)
```

- `[]Match` — the result. Rules that fired.
- `[]Diagnostic` — observations. Rules that didn't fire, and why. Informational.
- `error` — the engine itself broke. Bad CUE value, nil input, unreadable source — something that makes the result meaningless.

They are orthogonal. Matches and diagnostics can coexist (three rules evaluated, one fired, two didn't). Zero diagnostics and nil error is the clean-match case. Error non-nil means the other two are untrustworthy — don't render them.

**The discipline:** engine failures never flow through `[]Diagnostic`. A diagnostic is about *rule evaluation* ("rule X didn't match because path Y is missing"); an error is about *the evaluator itself* ("couldn't subsume because the CUE value is invalid"). The caller's code becomes explicit:

```go
matches, diags, err := evaluator.Evaluate(rules, input)
if err != nil {
    return err // engine failure — don't trust matches or diags
}
for _, d := range diags {
    render(d) // informational — engine worked, the rule just didn't match
}
```

This mirrors the shape `io.ReadAll` uses (`([]byte, error)` with partial bytes on error); our case is cleaner because the three concerns don't overlap.

Engine-level failures use package-level sentinel errors (Go stdlib convention, checked with `errors.Is`):

```go
var (
    ErrInvalidInput = errors.New("evaluator: input is not a valid CUE value")
    ErrRuleMissing  = errors.New("evaluator: rule not found")
)
```

Custom error types are reserved for cases where callers need structured context — `DiagError` at the loader boundary is the one such case. Everywhere else, sentinels plus `errors.Is` / `errors.As`.

## Error code registry

Codes are **stable across versions** once shipped. Breaking a code is a breaking change for anyone filtering / grepping on them.

| Range | Class | Examples |
|---|---|---|
| E01xx | Rule load | E0101 schema mismatch on `then`; E0102 unknown action kind; E0103 rule field name reserved |
| E02xx | Path resolution | E0201 key not found; E0202 indexing non-list; E0203 indexing out of range |
| E03xx | Leaf constraint | E0301 regex unsatisfied; E0302 value out of range; E0303 type mismatch; E0304 not in allowed set |
| E04xx | Disjunction | E0401 no arm matched; E0402 ambiguous default under pattern match |
| E05xx | Scope / binding | E0501 unresolved identifier; E0502 cross-rule ref; E0503 self-ref into then/meta |

Codes live in `internal/diag/codes.go` as typed constants with help strings:

```go
var E0201 = CodeInfo{
    Code: "E0201",
    Help: `A path segment referenced in the rule does not exist in the input.
    
Under closed-world pattern-match semantics, every path referenced in ` + "`when`" + `
must exist in the input for the rule to match. Absent paths cause the rule
to silently not fire; the diagnostic shows which segment broke the chain.`,
}
```

`quae explain --code E0201` prints the help. Good for users who don't remember what a code means.

## Renderer

One pass over a `Diagnostic`, one source lookup, one output. No templates, no configuration, no styling switches (for v0).

```
error[E0201]: key not found
  --> tests/policies/git.cue:12:24
   |
12 |     tool_input: flags: force: true
   |                 ^^^^^ key "flags" not found in input at path tool_input
   |
   = help: input.tool_input has keys: command, file_path
```

Implementation:

```go
// internal/diag/render.go

type SourceCache interface {
    // Returns the line containing pos, the 1-based line number, and
    // the 1-based column offset within that line.
    LineAt(pos token.Pos) (line string, lineNum, col int, ok bool)
}

func Render(d Diagnostic, src SourceCache) string {
    var b strings.Builder
    writeHeader(&b, d)           // "error[E0201]: key not found"
    writeLocation(&b, d.Primary) // "  --> file:line:col"
    writeSnippet(&b, d.Primary, src)
    for _, n := range d.Notes {
        writeSnippet(&b, n, src)
    }
    if d.Help != "" {
        writeHelp(&b, d.Help)
    }
    return b.String()
}
```

Source cache loads files once per rendering session. Positions arrive as `token.Pos` from CUE (opaque offsets resolved against the token.File registry).

## AST retention for `when`

The evaluator needs the `when` AST to localize failures. `config.Rule` grows one field:

```go
type Rule struct {
    Source     string
    When       cue.Value
    WhenSyntax ast.Expr   // NEW — the parsed syntax for when, with positions
    WhenMap    map[string]any
    Then       *Action
    Meta       *Meta
}
```

`WhenSyntax` is populated from the pre-Unify field value's `Source()`, not from `Syntax()`. CUE's `Value.Syntax()` pretty-prints the evaluated value back into an AST with **no source positions** — useless for diagnostics. `Value.Source()` returns the parser-emitted node (typically `*ast.Field` whose `.Value` is the `ast.Expr` we retain). Unify drops Source, so the lookup runs on the original `fieldVal` threaded into `decodeRule`, not the unified value:

```go
// internal/config/loader.go
func whenSyntax(v cue.Value) (ast.Expr, bool) {
    switch n := v.Source().(type) {
    case *ast.Field:
        if e, ok := n.Value.(ast.Expr); ok { return e, true }
    case ast.Expr:
        return n, true
    }
    return nil, false
}
```

Same pattern applies for T4/T5 when retaining `then:` / `meta:` / selector positions — always `Source()`, always on the pre-Unify value.

## The debug path: `localize`

One entry point, three lanes:

```go
func Evaluate(rules []Rule, input cue.Value) ([]Match, []Diagnostic, error) {
    if !input.Exists() {
        return nil, nil, ErrInvalidInput // engine-level — sentinel, checked via errors.Is
    }
    matches := make([]Match, 0, len(rules))
    var diags []Diagnostic
    for _, rule := range rules {
        if err := rule.When.Subsume(input); err == nil {
            matches = append(matches, Match{Rule: rule, Action: rule.Then})
            continue
        }
        if !explainEnabled() {
            continue // fast path — diags stays nil, localize never invoked
        }
        for d := range localize(rule, input) {
            diags = append(diags, d)
        }
    }
    return matches, diags, nil
}
```

Fast path: the CLI never flips explain on, `localize` is never invoked, `diags` is nil — one `Subsume` call per rule, same cost as today. Explain path: the CLI flips it on once at startup (via `--explain`, `QUAE_EXPLAIN`, or the `explain` subcommand), the walk runs on non-match, diagnostics accumulate.

Debug activation is a package-level toggle (`explainEnabled()`), not an API parameter. The three-lane signature stays clean; activation is a process-lifecycle concern (one setting per invocation), not something every call site should plumb through.

`localize` walks `rule.WhenSyntax` paired with the input value. It returns `iter.Seq[Diagnostic]` (Go 1.23 range-over-func) so the walker yields lazily and callers can stop after the first failure. Three species:

### Path-segment localization (E0201)

When `when` declares a nested struct whose path `a.b.c` is missing in the input:

1. Walk the `when` AST top-down, carrying an accumulated input path alongside the current AST node.
2. At each struct field, check whether the input has the corresponding key via `current.LookupPath(cue.MakePath(cue.Str(fieldName)))`.
3. At the first absent key, emit `E0201` with the field's source position and a help listing available keys at the parent.

```go
func localize(rule Rule, input cue.Value) iter.Seq[Diagnostic] {
    return func(yield func(Diagnostic) bool) {
        walkStruct(rule.WhenSyntax, input, nil, yield)
    }
}

func walkStruct(node ast.Expr, current cue.Value, path []string, yield func(Diagnostic) bool) bool {
    st, ok := node.(*ast.StructLit)
    if !ok {
        return true
    }
    for _, decl := range st.Elts {
        f, ok := decl.(*ast.Field)
        if !ok {
            continue
        }
        name := fieldName(f.Label)
        next := current.LookupPath(cue.MakePath(cue.Str(name)))
        if !next.Exists() {
            d := Diagnostic{
                Code:     "E0201",
                Severity: SeverityError,
                Title:    "key not found",
                Primary: Label{
                    Pos: f.Label.Pos(),
                    Len: len(name),
                    Msg: fmt.Sprintf("key %q not found in input at path %s",
                        name, joinPath(path)),
                },
                Help: fmt.Sprintf("input.%s has keys: %s",
                    joinPath(path), strings.Join(listKeys(current), ", ")),
            }
            if !yield(d) {
                return false
            }
            continue
        }
        if inner, ok := f.Value.(*ast.StructLit); ok {
            if !walkStruct(inner, next, append(path, name), yield) {
                return false
            }
        }
    }
    return true
}
```

The `yield` callback returns false when the caller breaks out of the range loop — the walker propagates that to halt cleanly. A caller needing only the first failure writes:

```go
for d := range localize(rule, input) {
    emit(d)
    break
}
```

The `slices` and `maps` packages gained iterator-aware helpers in 1.23 — `slices.Collect(localize(rule, input))` gathers all diagnostics, and filters compose cleanly over the stream.

### Leaf constraint localization (E0301)

When a scalar constraint at a leaf (regex, range, type) fails:

1. Find the leaf field in `WhenSyntax` whose value is the constraint expression.
2. Emit `E0301` with caret on the constraint expression.
3. Add `Notes` with `want:` (rendered constraint) and `got:` (rendered input value).

### Disjunction localization (E0401)

When `Subsume` fails on a disjunction:

1. Find the `ast.BinaryExpr{Op: token.OR}` chain that was the failing node.
2. Emit `E0401` with `Primary` pointing at the whole disjunction and `Notes` for each arm's span labeling "not equal X" / "regex failed" / etc.

## CLI surface

Three entry points, same renderer:

```
quae eval --explain=missed < input.json
  → stdout: vendor response
  → stderr: diagnostics (one per non-firing rule)

QUAE_EXPLAIN=1 quae eval < input.json
  → same as --explain=missed

quae explain my_rule_id < input.json
  → runs only my_rule_id
  → stdout: empty (or matched response if requested via --render)
  → stderr: diagnostic if no match
  → exit 0 on match, 1 on no-match, 2 on engine error
```

Flag parsing added in `cmd/quae/main.go` alongside existing flags. `explain` subcommand is a new case in the dispatch at the top of `run`.

## Migration: `ruleLoadError` → `diag.Diagnostic`

Today's `ruleLoadError` carries a CUE `*errors.Error`. Migration:

1. `internal/diag` provides `FromCueError(err error) Diagnostic` which extracts positions from the CUE error chain and assigns a code (E01xx family for load errors).
2. `loader.go` wraps with `&DiagError{D: diag.FromCueError(err)}` instead of the old string-builder. `Error()` renders via the same path as evaluator diagnostics. `errors.As(err, &dErr)` recovers the structured diagnostic. This is the one place a custom error type is warranted.
3. When a single load pass surfaces multiple independent failures (several rules in one file each fail schema/lint), the loader returns `errors.Join(errs...)` (Go 1.20+) rather than only the first. Callers unwrap with `errors.Is` / `errors.As`; the renderer prints each on its own.
4. Lint rejections from the `subsume-evaluator` scope emit diagnostics directly — E0501 (unresolved identifier), E0502 (cross-rule ref), E0503 (self-ref into then/meta).

Net effect: every error the user sees — load, lint, eval — goes through `diag.Render`. Consistent visual language, consistent code stability.

## Disjunction — closest-match arm

For v0 we render all arm failures equally. For v1, rank arms by **subsumption distance** (how far each arm got before failing) so the trace highlights the arm the author most likely intended. CUE's subsumption error may already carry this information; otherwise we approximate by counting satisfied fields per arm.

## Cost

| Component | LOC |
|---|---|
| `internal/diag` — types, codes, renderer | ~200 |
| AST retention in `config.Rule` | ~20 |
| `localize` — debug-path evaluator | ~150 |
| `ruleLoadError` → `diag.Diagnostic` migration | ~50 |
| CLI wiring (`--explain`, env var, `explain` subcommand) | ~80 |
| Tests (renderer, codes, localize, CLI) | ~300 |
| New scrut contract `tests/diagnostics.md` | ~8 blocks |

Total: ~500 LOC + ~300 LOC tests. Larger than `subsume-evaluator` because the UX surface area is larger.

## Open questions

- **Color output**: terminal color for error codes / severity markers. Default on when stderr is a tty; `--no-color` to disable. Trivial to add but adds dep (or a small ANSI helper).
- **Multi-line span rendering** when a constraint spans multiple lines (rare in practice). V0 renders only the first line; v1 can extend if it matters.
- **Closest-match arm ranking** under E0401 — v0 shows all arms equally; v1 ranks. Design TBD.
- **JSON output** for tooling integration. Not in v0; add `--format=json` in a follow-up if requested.
