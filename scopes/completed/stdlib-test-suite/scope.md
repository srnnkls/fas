---
created: 2026-06-05
status: active
issue_type: Feature
---

# stdlib-test-suite

## Goal

Harden the CUE stdlib test suite so a single matcher bug cannot reach a shipped
policy. The canonical catalog made the matcher vocabulary single-source — which
means one wrong matcher (a regex that misses `rm -R`, a prefix that over-matches
`/devops`) silently propagates to every rule that composes it. Two such bugs
escaped a suite that already had per-matcher tests, because the test cases were
authored from the implementation rather than from the domain. This scope rebuilds
the suite around **oracle independence** and adds the layers that catch the class
of bug that escaped: cross-matcher differential tests and property/fuzz.

## Context

<!-- What we know about the current state that shapes the approach. -->

The stdlib lives under `cue/` as catalog + binder + matcher sub-packages, composed
in rule `when` clauses via `&`. Tests already exist and are non-trivial; the gap is
methodology, not file coverage. Two confirmed bugs (now fixed) motivate the work:

- `flag.#hasRmRecursive` matched only lowercase `r`; `rm -R ~` bypassed a
  HIGH-severity deny rule. Fixed to `[rR]` — but `TestFlag_hasRmRecursive` had
  positive/negative cases and still missed it.
- `path.#systemTarget` lacked a trailing component boundary; `/etcfoo`, `/devops`,
  `/system` were wrongly classified as system paths, diverging from the correct
  `path.#systemInCommand`. Fixed with `($|/)` — `TestPath_SystemTarget_Regex`
  existed and still missed it.

Root cause in both: the corpus mirrored the regex instead of stating domain truth.

### Key Files

| File | Lines | Description |
|------|-------|-------------|
| `cue/stdlib_test.go` | ~700 | Per-matcher unification + regex tests; harness (`loadSubPkg`, `unifyExpect*`, `matchRegexExpect*`, `flagsInput`, `cueStringLit`) |
| `cue/events_test.go` | ~250 | Event-shape tests (#PreToolUse, #Agent, …) |
| `cue/schema_test.go` | ~150 | `#Input`/`#Rule` schema tests (test-only path) |
| `cue/eventset_drift_test.go` | ~150 | Drift guard across schema/catalog/hook event names |
| `tests/policy_drift_test.go` | ~50 | Anti-inline guard (hardcoded denylist of 3 regexes) |
| `tests/policies.md` | 37 cases | Scrut end-to-end policy goldens |
| `cue/flag/rm.cue`, `cue/path/path.cue`, `cue/command/command.cue`, `cue/{action,escalation,tool,catalog}/…` | — | Matchers under test |

### Architecture Decisions

#### AD-1: Oracle independence is the organizing principle

**Context:** Per-matcher tests existed for both escaped bugs; their cases were
derived from the implementation, so they could not contradict it.

**Decision:** Every matcher's corpus is authored from the domain spec (e.g. `man
rm`, what a path component is), stored as reviewable `cue/testdata/*.tsv` a person
can check without reading the matcher. Tables consume the corpus.

**Alternatives:**
- Keep ad-hoc inline cases: rejected — that is exactly what let the bugs through.
- Snapshot/golden of matcher output: rejected — still mirrors implementation.

#### AD-2: Add differential + fuzz layers, not just more cases

**Context:** The `/etcfoo` bug was a *disagreement between two matchers* over one
vocabulary; `-R` was an *unenumerated* case.

**Decision:** Cross-matcher differential tests (matchers over one vocabulary must
agree on a shared corpus) catch the first class; Go fuzz vs a hand-written
reference predicate catches the second.

### Constraints

- **No new Go module deps:** the repo pins a forked `cuelang.org/go` via `replace`
  and avoids `golang.org/x/term` etc. Fuzz reference predicates are hand-written
  stdlib-only Go.
- **CUE test harness:** new CUE-semantics tests reuse the existing `loadSubPkg`
  overlay harness in `cue/stdlib_test.go`; do not hand-roll a second loader.
- **Fuzz in CI = seed-run only:** `go test` executes the seed corpus deterministically;
  long fuzzing is manual/scheduled, never a blocking gate.
- **Scrut needs a fresh binary:** integration goldens run the on-PATH `fas`; the
  task must `go install ./cmd/fas` (via the existing `hk`/`mise` step) before scrut.

### Tech Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Corpus format | `cue/testdata/*.tsv` (`input <TAB> expected-classification`) | Reviewable against a man page; single source feeding tables + fuzz seeds |
| Fuzz model | CUE matcher verdict vs hand-written Go reference predicate | Divergence = bug; finds unenumerated cases like `-R` |
| Differential model | Shared path corpus → assert `systemTarget` and `systemInCommand` agree (modulo documented diffs) | Directly closes the `/etcfoo` class |
| Drift guard | Generalize to "any stdlib-exported regex literal inline in a policy fails" | Replaces the hardcoded denylist of 3 |

## Requirements

### Functional Requirements

- Each regex/disjunction matcher (`flag.#hasRm*`, `path.#systemTarget`/`#systemInCommand`/
  `#InCommandRe`, `command.#is*`, `action.#destructiveAction`, `escalation.#escalationCommand`)
  has a spec-derived table driven from `cue/testdata/`. (`tool.#Tool`/`hook.#Agent` are
  tool_name-pin structs, not corpus matchers — their correctness is the catalog↔binder
  derivation invariant below, not a `.tsv` table.)
- Differential test asserts `systemTarget` ↔ `systemInCommand` classify a shared
  path corpus consistently (documented divergences whitelisted explicitly).
- Property/fuzz harness compares CUE matcher verdicts to Go reference predicates for
  the rm-flag family, with a committed seed corpus.
- Vocabulary invariant test asserts `tool.#Tool` and `hook.#Agent` are *derived*
  from the catalog (every catalog member ⇒ a matcher member and vice versa) — this
  also constitutes their correctness coverage.
- Composition tests: a real 4-way `&` chain matches its intended input AND a
  negative composition (one failing conjunct ⇒ whole fails).
- Loader-contract (undefined catalog member fails at load): ALREADY SATISFIED by
  `internal/config/loader_test.go` (`TestLoadRules_TypoedToolRef_Rejected`,
  `TestLoadRules_TypoedAgentRef_Rejected`); no new work (STS-007 closed).
- Scrut completeness: every shipped policy has ≥1 deny case and ≥1 near-miss allow.
- `policy_drift_test` generalized to flag any exported stdlib regex literal inlined
  in a policy.

### Technical Requirements

- All new CUE-semantics tests reuse `cue/stdlib_test.go`'s overlay harness.
- `go test ./...` stays green and deterministic (no flaky fuzz in the gate).
- Fuzz seed-run wired into the `hk`/`mise` test task.

## Acceptance Criteria

- [ ] Given the `-R` recursive bug were reintroduced in `flag/rm.cue`
  When `go test ./cue/` runs
  Then a spec-derived table case AND `FuzzRecursiveFlag`'s seed corpus both fail.

- [ ] Given `path.#systemTarget`'s trailing boundary were removed
  When `go test ./cue/` runs
  Then the `systemTarget`↔`systemInCommand` differential test fails on `/etcfoo`.

- [ ] Given a catalog member were added without a corresponding binder member
  When `go test ./cue/` runs
  Then the catalog↔binder derivation invariant fails.

- [ ] Given a policy inlines any exported stdlib regex
  When `go test ./tests/` runs
  Then the generalized drift guard fails naming the helper to use.

- [ ] Given a shipped policy has a deny case but no near-miss allow case
  When the scrut-completeness check runs
  Then it reports the missing allow case.

## Dependency Graph

> Machine-readable: [dependencies.yaml](dependencies.yaml)

```
Phase 1 (Foundation)
└── STS-001 testdata corpora + Go loader (format pinned: input<TAB>{match|nomatch})

Phase 2 (Spec-derived units, need STS-001 — distinct files, parallel)
├── STS-002 matcher table rewrite (flag/command/action/escalation)  [stdlib_test.go]
├── STS-003 differential systemTarget ↔ systemInCommand            [stdlib_differential_test.go]
├── STS-011 SPIKE reusable compiled-matcher throughput             [stdlib_fuzz_test.go]
└── STS-004 property/fuzz rm-flag vs reference predicate (mode per STS-011)

Phase 3 (Structural, no deps — distinct files, parallel)
├── STS-005 catalog ↔ binder derivation (tool + agent axes)        [stdlib_derivation_test.go]
├── STS-006 composition positive + negative                        [stdlib_composition_test.go]
└── STS-009 generalize policy_drift_test (curated regex map)       [policy_drift_test.go]

Phase 4 (Integration)
├── STS-008 scrut allow/deny pairing completeness (needs STS-002/003)
└── STS-010 CI/docs: seed-run in hk/mise + oracle-independence note

CLOSED: STS-007 loader typo-rejection — already implemented (INV-6 satisfied).
```

## Non-Goals

- Changing matcher behavior beyond the two already-fixed bugs. This is a test
  scope; correctness fixes surfaced by new tests are filed as they appear, not
  bundled here.
- Testing the Go adapter / parser internals (covered by their own packages).
- Wiring `#Input` into the runtime eval path (deliberately test-only; out of scope).
- A second-harness catalog or capability layer.

## Verification

- `go test ./...` green; `go test ./cue/ -run Fuzz -fuzz=... ` reproduces a seeded
  `-R`-class find when the fix is reverted.
- Reverting each of the two known fixes turns at least one new test red (mutation
  check, recorded in design.md Verification table).
- `hk`/`mise` integration step (`go install ./cmd/fas` + scrut) stays green.

## Gotchas & Learnings

- `go test` alone does NOT exercise scrut — it runs the on-PATH `fas`. Always
  `go install ./cmd/fas` first (a stale binary produced a false 22-failure earlier).
- CUE regex matchers validate with `cue.Concrete(true)`; the harness helpers
  already encode this — reuse them.
- Over-matching in a deny rule is the safe direction; reference predicates should
  encode intended semantics, not be reverse-engineered from the regex.

## Open Questions

- [ ] Should the fuzz reference predicates live beside the tests or in a tiny
  internal package reusable by future matchers? (Default: beside tests.)
