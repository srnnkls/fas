# fas

*Code by Decree*

> *fās* • indeclinable noun, neuter; from Proto-Italic \*fasom, from \*feh₂- ("to speak")
>
> Pronunciation: /faːs/ (Classical)
>
> divine law; that which is right, lawful, or permitted by divine decree

*fas* is a policy DSL and runtime built on top of the incredible
[CUE](https://cuelang.org/). It evaluates structured input against rules
expressed as pure CUE — regex, bounds, list patterns, disjunctions,
structural negation — and emits a decision: allow, deny, ask, inject, or
modify. Matching is plain CUE subsumption, so there is no predicate
dialect and no embedded scripting; the rule language is CUE itself.

The runtime reads JSON on stdin and writes its decision on stdout. An
embedded stdlib bundles rule-authoring vocabularies for the domains *fas*
already knows about — `cue/hook` and `cue/tool` for AI coding agent
harnesses (`PreToolUse`, `PostToolUse`, …), `cue/path` for filesystem
patterns, `cue/flag` for command-line flag shapes, and so on — so rules
compose pre-built constraints (`hook.#PreToolUse & tool.#isBash &
path.#hasSystemTarget`) instead of restating each protocol from scratch.

## Install

Via [mise](https://mise.jdx.dev/):

```bash
mise use github:srnnkls/fas@0.1.0-alpha.1
```

Via `go install` (tracks main):

```bash
go install github.com/srnnkls/fas/cmd/fas@latest
```

## Quick start

Drop a CUE rule under `.fas/rules/`:

```cue
// .fas/rules/no_rm_home.cue
package rules

import (
	"list"

	"github.com/srnnkls/fas/cue/hook"
	"github.com/srnnkls/fas/cue/tool"
)

no_rm_home: {
	when: hook.#PreToolUse & tool.#isBash & {
		tool_input: {
			command: =~"^rm\\b"
			parsed: {
				flags:   list.MatchN(>0, =~"^-[a-zA-Z]*r[a-zA-Z]*$|^--recursive$")
				targets: list.MatchN(>0, =~"^(~|\\$HOME)$")
			}
		}
	}
	then: deny: {
		rule_id:  "no-rm-home"
		reason:   "Recursive deletion of the home directory is blocked"
		severity: "HIGH"
	}
}
```

Wire it into Claude Code as a `PreToolUse` hook; *fas* reads the JSON event
on stdin and writes the response on stdout:

```bash
fas eval --harness claude < event.json
```

## Pipeline

```
stdin JSON
  → adapter.ParseInput          (vendor → canonical envelope)
  → parser.Preprocess           (extract tool_input.parsed)
  → config.LoadRules            (global, then project)
  → pipeline.EvaluatePhases     (subsume input against every when:)
  → synthesis.Synthesize        (gates short-circuit, effects accumulate)
  → adapter.RenderOutput
  → stdout
```

Two rule sets layer: `~/.config/fas/rules/*.cue` (global, evaluated first) and
`.fas/rules/*.cue` (project). Blocking gates short-circuit; non-blocking
effects (inject, modify) accumulate across layers.

## Diagnostics

`fas eval --explain` (or `FAS_EXPLAIN=1`) emits compiler-style diagnostics on
stderr explaining why a rule did not fire — caret frames, `want:`/`got:`
labels, error codes (`E0201`, `E0301`, etc.). `fas explain <rule_id>` runs a
single rule and prints the same trace; `fas explain --code E0201` prints the
help text for an error code.

Three output formats: `--format=text` (default, ANSI-coloured), `--format=json`
(NDJSON, one diagnostic per line), `--format=sarif` (SARIF 2.1.0).

## Configuration

| Flag                 | Env var       | Default                  |
|----------------------|---------------|--------------------------|
| `--harness <name>`   | —             | `claude`                 |
| `--config <path>`    | —             | `.fas/rules`             |
| `--global-config <path>` | —         | `~/.config/fas/rules`    |
| `--fail-closed`      | —             | off (fail-open)          |
| `--explain[=MODE]`   | `FAS_EXPLAIN` | off                      |
| `--format <fmt>`     | `FAS_FORMAT`  | `text`                   |
| `--color <mode>`     | `FAS_COLOR`   | `auto`                   |

`fas --version` prints the build version. `fas --help` documents every flag.

## Building

Pure Go, `CGO_ENABLED=0`. Developer tasks are exposed as
[mise](https://mise.jdx.dev/) tasks (`mise tasks` lists them):

| Task                       | What it runs                                          |
|----------------------------|-------------------------------------------------------|
| `mise run build`           | `go build ./...`                                      |
| `mise run install`         | `go install ./cmd/fas`                                |
| `mise run test`            | unit + scrut integration tests (via `hk run test`)    |
| `mise run test-unit`       | `go test ./...`                                       |
| `mise run test-integration`| [scrut](https://github.com/facebookincubator/scrut) over `tests/*.md` |
| `mise run lint`            | gofmt, govet, golangci-lint, gomod-tidy (via `hk`)    |
| `mise run fix`             | gofmt + `go fix` modernizer (via `hk`)                |
| `mise run check`           | full validation: lint + build + tests + scrut        |

`mise install` provisions the toolchain (Go, scrut, hk, golangci-lint, pkl)
and registers the `pre-commit` hook. Bare `go build ./...` and `go test ./...`
also work standalone.

## License

TBD. Project is in alpha; a license will be added before `v0.1.0`.
