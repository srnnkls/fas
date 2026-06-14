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
structural negation — and emits a decision: allow, deny, ask, inject,
modify, or continue. Matching is plain CUE subsumption, so there is no predicate
dialect and no embedded scripting; the rule language is CUE itself.

The runtime reads JSON on stdin and writes its decision on stdout. An
embedded stdlib bundles rule-authoring vocabularies for the domains *fas*
already knows about — `cue/catalog` for the harness-agnostic vocabulary of
tool, subagent, and event names; `cue/hook`, `cue/tool`, and `cue/agent` for
AI coding agent harnesses (`PreToolUse`, `PostToolUse`, …); `cue/bash` for the
executable inside a Bash call; `cue/path` for filesystem patterns; `cue/flag`
for command-line flag shapes; and so on — so rules compose pre-built
constraints (`hook.#PreToolUse & tool.#Bash & path.#hasSystemTarget`)
instead of restating each protocol from scratch.

## Install

Via [mise](https://mise.jdx.dev/) (recommended) — pulls the prebuilt
release binary from GitHub:

While the repo is private, mise needs a GitHub token to reach the release
API. Export one (`export GITHUB_TOKEN="$(gh auth token)"`) before installing.

```bash
# pin in the current project's mise.toml
mise use github:srnnkls/fas@0.1.0-alpha.3

# or install globally on your PATH
mise use -g github:srnnkls/fas@0.1.0-alpha.3

# track the latest release instead of pinning
mise use github:srnnkls/fas@latest
```

Verify the install:

```bash
fas --version
```

Via `go install` (builds from source, tracks main):

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
	when: hook.#PreToolUse & tool.#Bash & {
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

## Continuation

Most actions answer a *permission* question — allow, deny, ask. A `Stop` (or
`SubagentStop`) rule answers a different one: *should the turn end?* The
`continue` action means "do not end the turn — hand this reason back to the
agent", which the `claude` harness renders as its block-and-continue contract.
The reason is delivered to whichever turn is ending: the main agent on `Stop`,
the subagent on `SubagentStop`.

Scope the `when` to a condition you can actually detect, so the `reason` fits
every turn it fires on. This rule reminds an `Explore` subagent to surface its
findings before it finishes — and stays silent for other subagents and for the
main turn:

```cue
// .fas/rules/explore_must_summarize.cue
package rules

import (
	"github.com/srnnkls/fas/cue/hook"
	"github.com/srnnkls/fas/cue/agent"
)

explore_must_summarize: {
	when: hook.#SubagentStop & agent.#Explore
	then: continue: {
		rule_id: "explore-must-summarize"
		reason:  "Before finishing, list the concrete file:line references you found."
	}
}
```

```console
$ echo '{"hook_event_name":"SubagentStop","agent_type":"Explore"}' | fas eval --harness claude
{"decision":"block","reason":"Before finishing, list the concrete file:line references you found."}
$ echo '{"hook_event_name":"SubagentStop","agent_type":"Plan"}'    | fas eval --harness claude
{}
```

Loop safety is stateless: the harness flags a turn it is already re-prompting
(`stop_hook_active`), and *fas* suppresses the continuation on that turn so a
continuation rule can never wedge a session into a non-terminating loop. Within
a single turn exactly one continuation wins (lowest `rule_id`).

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
