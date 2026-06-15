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

## Organizing rules

A rules directory is loaded as CUE **packages**, recursively. Each directory
of `.cue` files is one package; subdirectories are separate, independent
packages. This lets a tree split rules by category and share schema between
them:

```
.fas/rules/
├── schema/
│   └── targets.cue        # package schema — reusable #defs
├── security/
│   ├── git.cue            # package rules
│   └── rm.cue             # package rules
└── workflow/
    └── commits.cue        # package rules
```

Loading rules:

- **Every `.cue` file in a directory shares one package.** All files in a
  directory must declare the same single explicit `package` clause (e.g.
  `package rules`); an absent, mixed, or divergent clause is a load error
  (`E0505`). Files in a package share scope — a hidden `_helper` or a `#Def`
  declared in one file is visible to its siblings.
- **Subdirectories are independent packages**, loaded recursively. The same
  rule name may appear in different packages; a duplicate top-level rule name
  *within* one package (across its files) is a load error (`E0504`).
- **Pruned paths.** Dotfile dirs (`.x`), underscore dirs (`_x`), and `cue.mod`
  are skipped with their subtrees; non-`.cue` files are ignored.
- **Total order.** Rules load in a stable order: by module-relative directory
  (lexical), then filename (lexical), then declaration order within a file.

### Sharing schema

The rules tree is the CUE module `fas.local/rules`. Put reusable definitions in
a `schema/` package and import it by module-relative path:

```cue
// .fas/rules/schema/targets.cue
package schema

#SystemPath: =~"^(/etc|/sys|/proc)\\b"
```

```cue
// .fas/rules/security/git.cue
package rules

import (
	"github.com/srnnkls/fas/cue/hook"
	"github.com/srnnkls/fas/cue/tool"

	"fas.local/rules/schema"
)

protect_system_paths: {
	when: hook.#PreToolUse & tool.#Bash & {
		tool_input: parsed: targets: [...schema.#SystemPath]
	}
	then: deny: {
		rule_id:  "protect-system-paths"
		reason:   "Writes under system paths are blocked"
		severity: "HIGH"
	}
}
```

**Visibility caveat.** Only `#defs` cross a package boundary. A `_hidden` field
is package-private — it is *not* visible to an importing package, and every
regular top-level field is extracted as a **rule**. A shared-defs package must
therefore expose `#defs`, not regular fields. (Same-package files still share
`_hidden` helpers; only *cross-package* sharing requires `#defs`.)

Layering global and project rule sets — `replace`, `extend`, `disable`, and
cross-layer precedence — is a separate concern; see the rule-precedence model.
This section covers only how rules are organized *within* one layer.

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
