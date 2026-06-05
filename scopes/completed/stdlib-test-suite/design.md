# Design: stdlib-test-suite

---

## Problem

| Metric | Current | Target |
|--------|---------|--------|
| Bugs caught by per-matcher tests before review | 0/2 (both `-R` and `/etcfoo` escaped existing tests) | regressions of either ⇒ red |
| Matchers with a spec-derived (oracle-independent) corpus | 0 | all |
| Cross-matcher consistency checks | 0 | systemTarget ↔ systemInCommand (+ extensible) |
| Unenumerated-case discovery | manual review only | property/fuzz vs reference predicate |
| Drift guard coverage | 3 hardcoded regexes | all exported stdlib regexes |

The catalog refactor made the matcher vocabulary single-source. That is a win for
authoring (one name, typo-rejected at load) but it raises the cost of a matcher
bug: one wrong regex now propagates to every rule that composes it. The suite must
match that raised bar. It currently does not — two matchers shipped subtly wrong
*with* passing per-matcher tests, because the cases were derived from the code.

---

## Alternatives

### A. Just add more inline cases to the existing tests

Append `-R`, `/devops`, etc. to the current `[]string` literals.

**Rejected:** Treats the symptom. The next unenumerated case (`rmdir` vs `rm`,
`--recursive` bundling, a new system prefix) escapes the same way. No structural
defense against the *class* of bug.

### B. Snapshot/golden the matcher output over a generated input set

Generate inputs, record current verdicts, diff on change.

**Rejected:** Goldens encode whatever the matcher currently does, including its
bugs — a golden taken today would have blessed `rm -R ~` as allow. Mirrors the
implementation, the exact failure mode.

### Selected: Oracle-independent corpora + differential + fuzz

Three complementary defenses, each catching a different failure mode:
- **Spec-derived tables** state domain truth, so they can *contradict* the code.
- **Differential tests** catch disagreement between matchers over one vocabulary
  (the `/etcfoo` class) without needing to enumerate every input.
- **Fuzz vs reference predicate** finds unenumerated cases (the `-R` class)
  systematically rather than by reviewer luck.

This wins because each known bug maps to a layer that would have caught it, and the
layers generalize to bugs not yet seen.

---

## Invariants

| ID | Invariant | Enforcement |
|----|-----------|-------------|
| INV-1 | A matcher's accept/reject set equals the domain spec, not the regex | Spec-derived `testdata/*.tsv` tables (T002) |
| INV-2 | Matchers over one vocabulary agree on a shared corpus (modulo documented diffs) | Differential test (T003) |
| INV-3 | For all generated inputs, CUE matcher verdict == Go reference predicate | Fuzz harness + seed corpus (T004) |
| INV-4 | Every catalog member has exactly one binder member and vice versa | Derivation invariant test (T005) |
| INV-5 | A `when` chain matches iff every conjunct matches | Composition positive+negative (T006) |
| INV-6 | An undefined catalog member fails at rule load, not at match time | ALREADY ENFORCED — internal/config/loader_test.go: TestLoadRules_TypoedToolRef_Rejected, TestLoadRules_TypoedAgentRef_Rejected (STS-007 closed) |
| INV-7 | Every shipped policy is pinned by ≥1 deny and ≥1 near-miss allow | Scrut completeness (T008) |
| INV-8 | No policy inlines a regex an exported helper already provides | Generalized drift guard (T009) |

---

## Complexity

| Dimension | Before | After | Delta |
|-----------|--------|-------|-------|
| Test layers | 4 (unit, events, drift, scrut) | 8 (+ differential, fuzz, derivation, loader-contract) | +4 |
| Source of matcher cases | inline, code-mirrored | `testdata/*.tsv`, spec-derived | indirection, but reviewable |
| New runtime deps | — | none (stdlib-only Go) | 0 |
| CI gate cost | go test + scrut | + fuzz seed-run (deterministic) | negligible |

The added indirection (testdata files) is the point: it moves the oracle out of the
code so it can disagree with the code.

---

## Verification

### Test Cases

| Test | Validates | Expected |
|------|-----------|----------|
| Revert `flag/rm.cue` to `friv` lowercase-`r` | INV-1, INV-3 | table case `-R` red AND `FuzzRecursiveFlag` seed red |
| Revert `path.cue` trailing `($|/)` | INV-1, INV-2 | differential test red on `/etcfoo` |
| Add `catalog.#ToolName.Foo` without using it in `#Tool` | INV-4 | derivation invariant red |
| Break one conjunct of a 4-way chain | INV-5 | negative composition red |
| Reference `tool.#Tool.Bsh` in a fixture rule | INV-6 | load fails with undefined-field diagnostic |
| Remove a policy's allow case | INV-7 | scrut-completeness reports gap |
| Inline `^rm\b` in a fresh policy | INV-8 | drift guard red naming `command.#isRm` |

Mutation discipline: each known/likely bug must map to a row above that turns red
when the fix is reverted. A layer that catches nothing under mutation is dead.

---

## Specifications (resolved in /scope review)

### Corpus format (`cue/testdata/*.tsv`)

- Two columns, tab-separated: `input <TAB> expected`.
- `expected` ∈ `{match, nomatch}` (boolean classification of the matcher under test).
- No header row. Lines starting with `#` are comments. Blank lines ignored.
- A literal tab inside the `input` column is escaped as `\t`; the loader unescapes.
- One file per matcher vocabulary: `rm_flags.tsv`, `system_paths.tsv`, `commands.tsv`,
  `destructive_actions.tsv`, `escalation.tsv`. The same file feeds the table test and
  (for rm_flags) the fuzz/property seed corpus.

### Reference predicate (rm-flag family)

Authored from `man rm`, WITHOUT reading `rm.cue`, so it can disagree with the regex:
- accept: `-r`, `-R`, `--recursive`, `--recursive=<x>`, `-recursive`, and any short bundle
  matching `^-[A-Za-z]*[rR][A-Za-z]*$` (force/interactive/verbose analogues per their letter).
- reject: everything else (`-f` alone for recursive, `--force`, `-x`).
- STS-011 decides token-level vs list-level: the matcher is `list.MatchN(>0, …)` over
  `parsed.flags`, so the property compares the predicate applied to a flag LIST against the
  matcher; the seed corpus rows are single tokens lifted into single-element lists.

### Near-miss allow (for STS-008 / INV-7)

An input that shares the **triggering command/tool** with a deny case but lacks the specific
condition that causes the deny (wrong flag, non-system path, different tool_name). Examples:

| Policy | Deny case | Near-miss allow |
|--------|-----------|-----------------|
| destructive-home | `rm -rf ~` | `rm -rf ./build` (non-home target) |
| system-path | `rm /etc/x` | `rm /etcfoo` (boundary lookalike) |
| tee-system | `… \| tee /etc/x` | `… \| tee ./local` |

### STS-009 enumeration strategy

A **curated map** keyed by each exported stdlib regex string, maintained alongside the stdlib
(no source-grepping, no CUE-value-API walk — consistent with no-new-deps). Adding an exported
regex without registering it is itself a reviewable omission.

### Differential whitelist discipline (STS-003)

Restrict the differential to the input subset where `systemTarget` and `systemInCommand`
SHOULD agree (leading absolute paths, no command context). Each documented divergence is
enumerated with a per-case justification, and a meta-assertion fails if the whitelist contains
any prefix-boundary case (`/etcfoo`, `/devops`, …) — so the whitelist can never absorb the bug
class the test exists to catch.

### New-bug-mid-implementation rule

If a spec-derived case in STS-002 surfaces a THIRD matcher bug (beyond the two already fixed):
open a bug issue with the failing input, mark the case `xfail` referencing the issue, and
proceed. Do NOT block the STS scope on the fix — the scope ships the *detection*, the fix is
a separate change.

## Design Notes

- **Reference predicates encode intent, not the regex.** `isRecursiveFlag(tok)`
  should read like the man page (`-r`, `-R`, `--recursive`, any bundle containing
  `r`/`R`), written without looking at `rm.cue`. If it mirrors the regex, INV-3 is
  vacuous.
- **Differential whitelist must be explicit.** `systemTarget` (parsed targets) and
  `systemInCommand` (raw string) legitimately differ on some inputs (e.g. boundary
  treatment of `-`). The test whitelists those by name with a comment, so an
  *unexpected* divergence (the bug) still fails.
- **Fuzz stays out of the blocking gate.** CI runs the seed corpus via plain
  `go test`; `-fuzz` is a manual/scheduled deepening. Keeps the gate deterministic.
- **testdata is the single source.** The same `rm_flags.tsv` feeds the unit table
  and the fuzz seed corpus; `system_paths.tsv` feeds both matcher tables and the
  differential test. One file to review per vocabulary.
- **Reuse the overlay harness.** `loadSubPkg`/`unifyExpect*`/`matchRegexExpect*`
  already handle the CUE module overlay and `Concrete(true)` validation; new tests
  extend them rather than introducing a second loading path.
