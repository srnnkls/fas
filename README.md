# fas

*Code by Decree*

> *fās* • indeclinable noun, neuter; from Proto-Italic \*fasom, from \*feh₂- ("to speak")
>
> Pronunciation: /faːs/ (Classical)
>
> divine law; that which is right, lawful, or permitted by divine decree

*fas* is a policy DSL and runtime built on top of the incredible
[CUE](https://cuelang.org/). It declares what an AI coding agent is allowed
to do — gating, asking, or rewriting Bash commands, file edits, and tool
calls before they run — using CUE's own structural constraints as the rule
language. No bolted-on predicate dialect, no embedded scripting; matching
is plain CUE subsumption, so anything CUE can constrain (regex, bounds,
list patterns, disjunctions, structural negation) is something *fas* can
match.

*fas* speaks vendor-native hook protocols: it reads the event JSON on
stdin, evaluates every rule against it, and emits a decision — allow,
deny, ask, inject, or modify — that the host harness already knows how to
honour. A small embedded stdlib (`cue/hook`,
`cue/tool`, `cue/path`, `cue/flag`, …) ships with the binary so rules
compose pre-built constraints (`hook.#PreToolUse & tool.#isBash &
path.#hasSystemTarget`) instead of restating each harness's hook protocol
every time.

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

Pure Go, `CGO_ENABLED=0`. `go build ./...` and `go test ./...` work standalone.
Integration tests use [scrut](https://github.com/facebookincubator/scrut)
(installed via mise: `mise install`).

## License

TBD. Project is in alpha; a license will be added before `v0.1.0`.
