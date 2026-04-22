---
created: 2026-04-22
status: draft
---

# Design — Compiler-style diagnostics

## Core principle

**Errors are source code, not strings.** Every diagnostic carries positions at the finest granularity that matters (path segment, constraint leaf, disjunction arm). Rendering is a view over structured data, not a formatted string constructed at the error site.

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

Diagnostics are values, not errors. A function that may fail with a diagnostic returns `(T, *Diagnostic)`. Functions that may fail in multiple places return `(T, []Diagnostic)`.

Where a `Diagnostic` needs to flow through Go's error interface (e.g., `ruleLoadError` migration), wrap it: `type DiagError struct { D Diagnostic }; func (e *DiagError) Error() string { return render(e.D, sourceCache{}) }`.

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
   |                 ^^^^^ key "flags" not found in $input.tool_input
   |
   = help: $input.tool_input has keys: command, file_path
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

`WhenSyntax` is populated in `decodeRule` from `when.Syntax(cue.All(), cue.Docs(true))`, which returns the `ast.Expr` with preserved positions. No re-parsing; CUE already has this from `compileRuleFile`.

## The debug path: `localize`

Fast path (production) — unchanged from `subsume-evaluator`:

```go
func Evaluate(rules []Rule, input cue.Value) ([]Match, error) {
    // ... uses Subsume == nil as match primitive
}
```

Debug path — added:

```go
func Explain(rules []Rule, input cue.Value) ([]Match, []Diagnostic) {
    matches := make([]Match, 0, len(rules))
    var diags []Diagnostic
    for _, rule := range rules {
        bound := rule.When.FillPath(cue.ParsePath("$input"), input)
        if err := bound.Subsume(input); err == nil {
            matches = append(matches, Match{Rule: rule, Action: rule.Then})
            continue
        }
        // Non-match → localize to a diagnostic.
        d := localize(rule, input)
        diags = append(diags, d)
    }
    return matches, diags
}
```

`localize` walks `rule.WhenSyntax` paired with the input value. Three species:

### Path-segment localization (E0201)

When `when` references `$input.a.b.c` and the chain breaks at `b`:

1. Flatten the `ast.SelectorExpr` chain to `["$input", "a", "b", "c"]` with per-segment positions.
2. Walk input segment-by-segment.
3. At the first absent segment, emit `E0201` with the segment's position and a help listing available keys at the parent.

```go
func localizeSelector(sel ast.Expr, input cue.Value) *Diagnostic {
    segments := flattenSelector(sel)  // [(name, pos), ...]
    current := input
    for i, seg := range segments[1:] {  // skip $input root
        next := current.LookupPath(cue.ParsePath(seg.Name))
        if !next.Exists() {
            available := listKeys(current)
            return &Diagnostic{
                Code:     "E0201",
                Severity: SeverityError,
                Title:    "key not found",
                Primary: Label{
                    Pos: seg.Pos,
                    Len: len(seg.Name),
                    Msg: fmt.Sprintf("key %q not found in %s",
                        seg.Name, joinPath(segments[:i+1])),
                },
                Help: fmt.Sprintf("%s has keys: %s",
                    joinPath(segments[:i+1]), strings.Join(available, ", ")),
            }
        }
        current = next
    }
    return nil
}
```

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
2. `loader.go` wraps with `&DiagError{D: diag.FromCueError(err)}` instead of the old string-builder. `Error()` renders via the same path as evaluator diagnostics.
3. Lint rejections from the `subsume-evaluator` scope emit diagnostics directly — E0501 (unresolved identifier), E0502 (cross-rule ref), E0503 (self-ref into then/meta).

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
