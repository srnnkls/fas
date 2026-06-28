# The fas guide

This is the long-form companion to the [README](README.md). The README is the
map — terse, every flag in one place. This guide is the walkthrough: it opens
with what fas does and how it feels to author rules, works through the decision
model, and then goes under the hood into how the engine actually matches,
combines, and explains. Read it top to bottom the first time; after that, jump
to the section you need.

If you just want the command, it's in the README. If you want to understand why
a rule fired — or why it didn't — you're in the right place.

## Contents

- [How fas works](#how-fas-works)
- [Your first rule](#your-first-rule)
- [The standard library](#the-standard-library)
- [Writing `when`: patterns, not predicates](#writing-when-patterns-not-predicates)
  - [Subsumption is the primitive](#subsumption-is-the-primitive)
  - [The parsed view](#the-parsed-view)
  - [Structural negation](#structural-negation)
  - [What `when` cannot express](#what-when-cannot-express)
- [Deciding: the `then` verbs](#deciding-the-then-verbs)
- [How decisions combine](#how-decisions-combine)
- [Layering: global and project](#layering-global-and-project)
- [Organizing rules into packages](#organizing-rules-into-packages)
- [Debugging: explain, vet, diagnostics](#debugging-explain-vet-diagnostics)
- [Wiring into Claude Code](#wiring-into-claude-code)
- [Under the hood](#under-the-hood)
  - [Subsumption is the engine](#subsumption-is-the-engine)
  - [The preprocessor and the parsed bridge](#the-preprocessor-and-the-parsed-bridge)
  - [Closed-world matching](#closed-world-matching)
  - [The stdlib as a module overlay](#the-stdlib-as-a-module-overlay)
  - [The two-phase pipeline](#the-two-phase-pipeline)
  - [Synthesis: one gate, many effects](#synthesis-one-gate-many-effects)
  - [Diagnostics: localize then render](#diagnostics-localize-then-render)
  - [Fail-open by default](#fail-open-by-default)
  - [The catalog as single source of truth](#the-catalog-as-single-source-of-truth)
- [Design motivations](#design-motivations)
- [When something looks wrong](#when-something-looks-wrong)
- [Where to look next](#where-to-look-next)

## How fas works

fas reads a structured event on stdin, matches it against rules, and writes one
decision on stdout. The event is an AI coding agent's *hook payload* — a tool
about to run, a prompt submitted, a subagent starting. The decision is one of
five verbs: *allow*, *deny*, *ask*, *inject*, or *modify*.

The whole engine is one pipeline, and every event runs through it the same way:

```
stdin JSON
  → adapter.ParseInput      (vendor payload → canonical envelope)
  → parser.Preprocess       (enrich tool_input with a parsed view)
  → config.LoadRules        (global layer, then project layer)
  → pipeline.EvaluatePhases (subsume the input against every when:)
  → synthesis.Synthesize    (one gate wins; effects accumulate)
  → adapter.RenderOutput
  → stdout
```

The one idea everything else falls out of: a rule's `when` block is a *pattern*,
not a predicate. There is no `$input` variable, no callback, no embedded
scripting. The pattern is the shape of the inputs it matches, and an event
matches when [CUE](https://cuelang.org/) accepts it as an instance of that shape
— plain subsumption. That is why fas has no predicate dialect to learn: the rule
language is CUE itself, and every constraint CUE can check (regex, bounds, list
patterns, disjunctions, optional fields) is a constraint a rule can express.

A rule has three parts. `when` is the pattern. `then` is the decision to emit on
a match. `meta` is optional bookkeeping.

```cue
some_rule: {
	when: { /* a pattern over the input */ }
	then: deny: { rule_id: "...", reason: "...", severity: "HIGH" }
	meta: requires: ["..."]   // optional
}
```

## Your first rule

Drop a CUE file under `.fas/rules/`. This one blocks a recursive delete of the
home directory:

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

Read the `when` as a description: a `PreToolUse` event, for the `Bash` tool,
whose command starts with `rm`, carries a recursive flag, and names `~` or
`$HOME` among its targets. An event matching all of that is denied.

Before wiring it anywhere, validate it. `fas vet` loads every rule, runs the
package, lint, and schema checks, and prints what it found — no stdin needed:

```bash
$ fas vet --config .fas/rules
ok: 1 rules loaded (global: 0, project: 1)
  project: no-rm-home
```

Then run an event through it. `fas eval` reads the hook payload on stdin and
writes the decision on stdout:

```bash
$ echo '{"hook_event_name":"PreToolUse","tool_name":"Bash",
        "tool_input":{"command":"rm -rf ~"},"session_id":"s","cwd":"/tmp"}' \
  | fas eval --config .fas/rules
{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"Recursive deletion of the home directory is blocked"}}
```

A command that doesn't match every conjunct falls through to allow:

```bash
$ echo '{"hook_event_name":"PreToolUse","tool_name":"Bash",
        "tool_input":{"command":"rm -rf ./build"},"session_id":"s","cwd":"/tmp"}' \
  | fas eval --config .fas/rules
{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"allow"}}
```

`./build` is a relative path, so it never matches the `~|$HOME` target
constraint — the rule stays silent and the call is allowed. That asymmetry is the
whole safety story: a rule only ever speaks when its pattern matches, and a
pattern that doesn't match contributes nothing.

## The standard library

The `no_rm_home` rule could have spelled out `tool_name: "Bash"` and
`hook_event_name: "PreToolUse"` by hand. It didn't, because fas ships a
*stdlib* — a set of CUE packages that name the vocabulary for you, so a rule
composes pre-built constraints with `&` instead of restating each protocol from
scratch. Import what you need; reference the `#defs`.

| Package | Import path | What it gives you |
| --- | --- | --- |
| `hook` | `github.com/srnnkls/fas/cue/hook` | per-event shapes: `#PreToolUse`, `#PostToolUse`, `#UserPromptSubmit`, `#Stop`, `#SubagentStart`, `#SubagentStop`, `#Notification` |
| `tool` | `…/cue/tool` | tool identities: `#Bash`, `#Edit`, `#Write`, `#WebFetch`, … and `#Known` (any built-in tool) |
| `agent` | `…/cue/agent` | subagent identities: `#Explore`, `#Plan`, `#GeneralPurpose`, `#Known` |
| `bash` | `…/cue/bash` | the executable inside a Bash call: `#command`, `#subcommand`, `#commandOrRaw` |
| `path` | `…/cue/path` | filesystem patterns: `#hasSystemTarget`, `#hasSystemInCommand`, `#systemTarget`, `#SystemPrefixes` |
| `flag` | `…/cue/flag` | command-line flag shapes: `#hasFlagMatching`, `#hasOption` |
| `action` | `…/cue/action` | destructive semantic verbs: `#hasDestructiveAction` |
| `escalation` | `…/cue/escalation` | privilege-escalation prefixes: `#hasPrivilegeEscalation` |
| `catalog` | `…/cue/catalog` | the raw name tables (`#ToolName`, `#AgentType`, `#EventName`) the layers above are built from |

Composition reads left to right as a conjunction — *this event, and this tool,
and this property*:

```cue
when: hook.#PreToolUse & tool.#Bash & (bash.#command & {#name: "tee"}) & path.#hasSystemInCommand
```

That matches a `tee` invocation — even behind `sudo tee` or `FOO=1 tee`, because
`bash.#command` reads the *parsed* executable rather than the raw string — whose
command line references a system path like `/etc`. The stdlib matchers prefer
parsed facts over raw-string scans for exactly this reason: they survive the
prefixes (`sudo`, env assignments, leading whitespace) that defeat a naive
`^tee\b`.

The catalog is the single source of every name. `catalog.#ToolName.Bsh` (a typo)
is an undefined field the loader rejects at load time, not a rule that silently
never matches. The names are Claude Code's identities; a different harness would
ship its own catalog and the layers above it would follow.

## Writing `when`: patterns, not predicates

### Subsumption is the primitive

The evaluator calls `when.Subsume(input)` per rule. Subsumption asks: is the
input an instance of this pattern? It handles every leaf constraint CUE handles,
with no custom operators and no evaluator-level fallback:

| Want | Write |
| --- | --- |
| literal | `tool_name: "Bash"` |
| bound | `retry_count: <=10`, `tool_name: !="Read"` |
| regex | `command: =~"^rm\\s+-rf"`, `command: !~"^git\\s"` |
| every list element matches | `targets: [...=~"^/etc/"]` |
| at least N elements match | `flags: list.MatchN(>=2, =~"^-")` |
| optional field | `flags?: force?: !=true` |
| struct disjunction | `{tool_name: "Bash"} \| {tool_name: "Write"}` |

If CUE can check it, `when` can express it. The curated universe builtins `and`,
`or`, `matchN`, `matchIf`, and `len` are also available bare inside `when`.

### The parsed view

Matching a raw command string is brittle: `sudo rm`, `FOO=1 rm`, `  rm`, and
`rm` in the second clause of `a && rm -rf /etc` all defeat a `^rm\b` regex. So
before evaluation, the preprocessor parses every Bash command into a structured
*parsed view* and hangs it at `tool_input.parsed`:

| Field | Holds |
| --- | --- |
| `parsed.commands` | resolved executable names (`rm`, `git`), prefixes stripped |
| `parsed.subcommands` | subcommand tokens (`add`, `commit`) |
| `parsed.targets` | path-like arguments, walked from the AST |
| `parsed.flags` | flag tokens (`-rf`, `--force`) |
| `parsed.actions` | destructive semantic verbs, when present |
| `parsed.calls` | per-command groups: each call's own `command`, `subcommand`, `action`, `targets`, `flags` |
| `parsed.attributes` | side facts: `prefix_commands` (sudo/doas/su), `parse_error` |

The AST walk reaches every simple command inside `&&`, `||`, `;`, pipelines, and
loops — not just the first token — so a destructive command hidden in a compound
line still surfaces in `parsed`. This is why the stdlib matchers (`bash.#command`,
`path.#hasSystemTarget`, `flag.#hasOption`) read `parsed.*`: they match the
meaning of the command, not its surface syntax. When the parser fails on
malformed input, `bash.#commandOrRaw` falls back to an anchored raw-string scan
so deny coverage survives.

The flat `parsed.commands`/`parsed.targets` lists are deny-safe but lossy: they
cannot say which target a given command acts on, so `cat README && rm .env`
puts a read verb and a secret in the same lists even though nothing reads the
secret. `parsed.calls` groups each invocation with its own arguments, so a rule
can match a call that *both* runs a read verb and targets a secret:

```cue
tool_input: parsed: calls: list.MatchN(>0, {
	command: _readVerb
	targets: list.MatchN(>0, _secretFile)
	...
})
```

(Key on `command` against the verb set you care about — `action` only carries
the destructive-verb table, so read-only verbs like `grep` or `base64` never
populate it.)

This is not a relational match against the input (see *Sibling references*
below) — the parser pre-joins each command with its arguments at parse time, so
the constraint is an ordinary existential over one struct's fields: "is there a
call whose verb is read and whose own target is a secret."

### Structural negation

CUE has no `!` over structs, so push negation down to the leaves — De Morgan by
hand:

| Wanted | Express as |
| --- | --- |
| `tool_name` is not `"Bash"` | `tool_name: !="Bash"` |
| `command` does not match `^rm` | `command: !~"^rm"` |
| not (`a=1` and `b=2`) | `{a: !=1} \| {b: !=2}` |
| not (match `X` and match `Y`) | `X_negated \| Y_negated` |

### What `when` cannot express

Subsumption checks the pattern statically — it does not substitute input
values into the pattern's own references. One genuine limitation follows from
that; the rest are natural-looking CUE idioms that the lint rejects at load
time before they can misfire.

The limitation: relating two input fields by value. `{command: targets[0],
targets: [...]}` constrains `command` to equal `targets[0]` *within the
pattern*, not within the input — CUE regex is RE2, so backreferences can't
smuggle it in either. Constrain each field on its own observable shape instead:
`tool_input: {command: =~"^rm\\b", parsed: targets: [...=~"^/etc/"]}` matches
an `rm` whose targets are under `/etc` without tying the two together. When you
genuinely need a command bound to *its own* argument (read verb acting on a
secret), use `parsed.calls`, where the parser has already grouped each call
with its arguments — the *parsed view* section shows the pattern. For the rare
case where you genuinely need cross-field equality (e.g. `cwd` equals a
target), the `@bind` escape hatch exists — see the README.

The lint catches four idioms that look right but evaluate against the pattern's
types rather than the input's values:

- `let` in `when` (E0506). `let cmd = tool_input.command` binds the pattern's
  type (`string`), not the input's value. `cmd & =~"^git"` then accepts every
  string, not just git commands. Drop the `let` and constrain the path in place:
  `tool_input: command: =~"^git\\b"`.
- `if` or `for` in `when` (E0507). `if list.Contains(flags, "--force") { ... }`
  evaluates the guard against the pattern's `flags` field — a type like
  `[...string]`, not a concrete list — so the gated fields either always or
  never appear regardless of input. A `for` comprehension iterates the same
  inert type. Since a pattern is already a conjunction, conjoin the fields
  directly:
  `tool_input: {parsed: flags: list.MatchN(>0, =~"--force"), command: =~"^git push"}`.
  For "either of these shapes," use a struct-level disjunction (`{...} | {...}`).
- Computed counts. `_n: len(flags)` with `_n: >=2` materialises `flags` as
  `[]` at pattern level, so `_n` is always `0`. CUE catches the static conflict
  (`0 & >=2`), so the rule fails to load. Count with a list pattern instead:
  `tool_input: parsed: flags: list.MatchN(>=2, =~"^-")`.
- `close` over a `when` struct (E0501). Closing an open hook payload makes the
  pattern never subsume an extensible input, so the rule silently never matches.
  `close` is excluded from the permitted universe builtins. Constrain the leaf
  and leave the struct open: `tool_name: !="Bash"` to exclude a tool.

## Deciding: the `then` verbs

A matched rule emits one action. Five verbs, two kinds:

```cue
then: deny: { rule_id: "...", reason: "...", severity: *"HIGH" | "CRITICAL" | "MEDIUM" | "LOW" }
then: ask:  { rule_id: "...", reason: "...", question: "..." }
then: allow: true
then: inject: { rule_id: "...", text: "...", channel: *"agent" | "user", priority: *50 | >=1 & <=100, tags?: [...] }
then: modify: { rule_id: "...", reason: "...", updated_input: _, mode: *"confirm" | "silent", priority: *50 | >=1 & <=100 }
```

*Gates* decide whether the call proceeds: `deny` blocks it, `ask` routes it to a
human, `allow` lets it through. Exactly one gate wins per evaluation.

*Effects* enrich the call without gating it. `inject` adds context — `channel:
"agent"` rides along as `additionalContext`, `channel: "user"` appends to the
decision reason. `modify` rewrites the tool input before it runs.

A note on `modify` under Claude Code: the `PreToolUse` hook has no
input-rewrite channel, so the `claude` harness rejects any ruleset that emits
`modify` at load time — a configuration error, surfaced by `vet`, not a silent
drop. `modify` is in the schema for harnesses that can honor it.

`severity` matters when two denies match the same event; it is the first
tiebreak (see [How decisions combine](#how-decisions-combine)).

## How decisions combine

A single event can match many rules. Synthesis reduces every match into one
envelope by two independent rules — one for the gate, one for the effects.

One gate wins. Across all matched gates, kind order is `deny > ask > allow`.
Within `deny`, the tiebreak is severity (`CRITICAL > HIGH > MEDIUM > LOW`) then
`rule_id` ascending; within `ask`, `rule_id` ascending. So a `CRITICAL` deny
beats a `HIGH` deny, and any deny beats any ask.

Effects accumulate. Every matching `inject` contributes, sorted by priority
descending then `rule_id` ascending, joined by newlines, capped at an 8 KiB
budget. `agent`-channel text becomes `additionalContext`; `user`-channel text is
appended to the reason. A `modify` winner is picked by priority — but dropped if
the final decision is blocking, since a denied call has no input to rewrite.

The two combine freely. An event can be denied and carry injected context: the
deny gates the call, the inject still rides along in `additionalContext`. An
`inject` on an otherwise-unremarkable call produces an `allow` that carries a
note:

```bash
$ echo '{"hook_event_name":"PreToolUse","tool_name":"WebFetch",
        "tool_input":{"url":"https://x"},"session_id":"s","cwd":"/tmp"}' \
  | fas eval --config .fas/rules
{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"allow","additionalContext":"Prefer the local docs cache before fetching the network."}}
```

## Layering: global and project

fas evaluates two rule sets. The *global* layer
(`~/.config/fas/rules/`, override with `--global-config`) is evaluated first;
the *project* layer (`.fas/rules/`, override with `--config`) second. The split
lets a machine-wide baseline live alongside per-repo rules.

The phases are not independent. A *blocking deny* anywhere in the global layer
short-circuits the pipeline: the project layer is never evaluated, because the
call is already dead. Every other outcome — `ask`, `allow`, and all effects —
lets phase two run, and the two phases' matches concatenate in source order
before synthesis sees them.

So a global `inject` and a project `deny` both apply (the inject doesn't gate, so
phase two still runs):

```bash
$ echo '{"hook_event_name":"PreToolUse","tool_name":"Bash",
        "tool_input":{"command":"rm -rf ~"},"session_id":"s","cwd":"/tmp"}' \
  | fas eval --global-config ~/.config/fas/rules --config .fas/rules
{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"Recursive deletion of the home directory is blocked","additionalContext":"Bash call audited by the global policy layer."}}
```

But a global `deny` ends it there — the project layer's rules never run:

```bash
$ echo '{"hook_event_name":"PreToolUse","tool_name":"Bash",
        "tool_input":{"command":"git add --force secret.env"},"session_id":"s","cwd":"/tmp"}' \
  | fas eval --global-config ~/.config/fas/rules --config .fas/rules
{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"git add --force is blocked by the global policy layer","additionalContext":"Bash call audited by the global policy layer."}}
```

## Organizing rules into packages

A rules directory loads as CUE *packages*, recursively. Each directory of `.cue`
files is one package; subdirectories are separate, independent packages. A tree
can split rules by category and share schema between them:

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

A few rules govern loading:

- One package per directory. The `package` clause is optional — files that
  omit it adopt the directory's canonical name (the one explicit name declared
  there, or `rules`). Two or more different explicit names in one directory is
  a load error (`E0505`). Sibling files share scope: a `_helper` or `#Def` in
  one file is visible to the others.
- Subdirectories are independent packages. The same rule name may appear in
  different packages; a duplicate top-level rule name *within* one package is a
  load error (`E0504`).
- Pruned paths. Dotfile dirs (`.x`), underscore dirs (`_x`), and `cue.mod`
  are skipped with their subtrees; non-`.cue` files are ignored.
- Total order. Rules load by module-relative directory (lexical), then
  filename, then declaration order — independent of traversal order.

Sharing schema. The rules tree is the CUE module `fas.local/rules`. Put
reusable definitions in a `schema/` package and import it by module-relative
path:

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
	then: deny: { rule_id: "protect-system-paths", reason: "Writes under system paths are blocked", severity: "HIGH" }
}
```

Visibility caveat. Only `#defs` cross a package boundary. A `_hidden` field
is package-private — it is not visible to an importing package, and every regular
top-level field is extracted as a *rule*. A shared-defs package must therefore
expose `#defs`, not regular fields.

## Debugging: explain, vet, diagnostics

Most rule-authoring time goes to one question: *why didn't my rule fire?* fas
answers it with compiler-style diagnostics rather than a silent non-match.

`fas vet` is the first line of defense — it validates without any input, so it
drops into CI or a pre-commit hook. It catches package-clause conflicts,
duplicate rule names, the structural lint (cross-rule references, unbound
identifiers), schema violations, and adapter-capability mismatches:

```bash
$ fas vet --config .fas/rules
ok: 4 rules loaded (global: 0, project: 4)
  project: tee-system
  project: webfetch-reminder
  project: confirm-force-push
  project: no-rm-home
```

`fas eval --explain` (or `FAS_EXPLAIN=1`) emits diagnostics on stderr explaining
why each rule did or didn't fire — caret frames, `want:`/`got:` labels, error
codes. Three filter modes: `--explain=missed` (the default — only non-firing
rules), `--explain=fired`, and `--explain=both`.

`fas explain <rule_id>` runs a single rule against stdin and exits 0 on match, 1
on no-match (with the diagnostic), 2 on an unknown rule. `fas explain --code
E0201` prints the help text for an error code:

```bash
$ fas explain --code E0201
E0201

A path segment referenced in the rule does not exist in the input.

Under closed-world pattern-match semantics, every path referenced in
`when` must exist in the input for the rule to match. Absent paths
cause the rule to silently not fire; the diagnostic shows which
segment broke the chain.
```

The error codes you'll meet:

| Code | Means |
| --- | --- |
| `E0201` | a `when` path is absent from the input (closed-world key miss) |
| `E0301` | a leaf constraint failed (regex, bound) |
| `E0303` | a kind mismatch (string where int wanted) |
| `E0401` | no disjunction arm matched |
| `E0501` | unbound identifier in `when` (load-time) |
| `E0502` | cross-rule reference (load-time) |
| `E0504` / `E0505` | duplicate rule name / conflicting package names (load-time) |
| `E0506` | `let` clause in `when` — binds pattern type, not input value (load-time) |
| `E0507` | `if`/`for` comprehension in `when` — evaluates against pattern, not input (load-time) |
| `E0601` | `@bind` variable mismatch — bound fields resolved to different values |

Diagnostics render three ways: `--format=text` (default, ANSI-colored, honors
`--color` and `NO_COLOR`), `--format=json` (NDJSON, one object per diagnostic),
and `--format=sarif` (SARIF 2.1.0 for code-scanning tooling).

For deep debugging, `FAS_LOG=<dir>` records one JSON file per invocation — raw
input, preprocessed input, loaded rules, matches, rendered output. These are
written unredacted at mode `0600` and may contain secrets, so point `FAS_LOG`
only at a trusted directory. `FAS_LOG_TTL` (default `1h`) garbage-collects old
logs.

## Wiring into Claude Code

fas speaks the Claude Code hook protocol. Register it as a `PreToolUse` hook (and
the other events you want to govern); the harness pipes the JSON event to fas's
stdin and reads the decision from stdout. The default harness is `claude`, so the
flag is optional:

```bash
fas eval < event.json          # --harness claude is the default
```

The decision shape is what Claude Code expects: a `hookSpecificOutput` carrying a
`permissionDecision` of `allow`, `deny`, or `ask`, plus an optional
`permissionDecisionReason` and `additionalContext`. Context-only events
(`SubagentStart`, `SubagentStop`) get a decision-free shape carrying just
`additionalContext`.

## Under the hood

Everything above is the contract; this is the machinery behind it. None of it is
needed to write a rule, but it is what lets you reason about the edge cases —
and what the engine is actually doing with your event.

### Subsumption is the engine

There is no bespoke matcher. `evaluator.Evaluate` compiles each rule's `when` to
a `cue.Value` and calls `when.Subsume(input)` — CUE's own "is this an instance
of that" check. A match is a `nil` error from `Subsume`; a non-match is the error
CUE returns, which is exactly the material the diagnostic layer localizes. The
engine adds ordering, layering, and synthesis around that primitive, but the
matching itself is borrowed wholesale from the language. This is the deliberate
center of the design: no predicate dialect to specify, implement, and keep in
sync with CUE — the rule language and the matcher are the same thing.

### The preprocessor and the parsed bridge

The adapter parses the vendor payload into a canonical `envelope.Input`; the
preprocessor then enriches it. For a Bash tool it parses the command into the
`parsed` view (`commands`, `subcommands`, `targets`, `flags`, `actions`,
`attributes`) and repacks it at `tool_input.parsed`, where the stdlib matchers
read it. The parser walks the shell AST, so it sees every simple command in a
compound line — the compound-command coverage isn't a special case in the rules,
it is a property of where `parsed.targets` comes from. The enriched input is
JSON-round-tripped into a `cue.Value`, which is what the evaluator subsumes
against. The lowercase struct tags on the Go side align with the lowercase
field names the CUE schema expects, so the bridge needs no field-name mapping.

### Closed-world matching

`when` references are matched against the input under closed-world semantics:
every path the pattern names must exist in the input for the rule to match. An
absent path is not a wildcard — it is a miss, surfaced as `E0201`. This is the
safe direction for a policy engine: a rule that references `signals.user_confirmed`
must not silently match an input that never carried a `signals` field. The
diagnostic names the segment that broke the chain and lists the keys actually
present at that level, so the fix is mechanical.

### The stdlib as a module overlay

Rules import the stdlib by its module path
(`github.com/srnnkls/fas/cue/...`) and the rules tree by `fas.local/rules`, yet
neither is fetched from anywhere. The stdlib `.cue` files are embedded into the
binary and mounted into a synthetic CUE module overlay at load time, under a
virtual `cue.mod/pkg/` root. The loader compiles your rules against that overlay,
so imports resolve offline and version-locked to the binary. One visible
consequence: a diagnostic whose constraint originated in the stdlib reports a
provenance path inside that virtual root
(`/__fas_rules__/cue.mod/pkg/github.com/srnnkls/fas/cue/path/path.cue:42:1`)
rather than an on-disk file — the constraint genuinely lives in the embedded
overlay.

### The two-phase pipeline

`EvaluatePhases` runs the global layer, then the project layer. The only
short-circuit is a blocking deny in phase one: if any global rule denies, phase
two is skipped and phase one's matches are returned as-is. Every non-blocking
outcome lets phase two run, and the result is the concatenation of both phases'
matches in source order. Diagnostics are pass-through — they only populate when
the explain toggle is on, so the default eval path pays nothing for the
diagnostic machinery.

### Synthesis: one gate, many effects

`Synthesize` buckets every match by kind, then reduces. The gate is picked by the
`deny > ask > allow` order with the severity/rule_id tiebreaks. Injects are
cloned, sorted by `(priority DESC, rule_id ASC)`, and concatenated under the size
budget, split by channel into `additionalContext` (agent) and the reason text
(user). A modify winner is the highest-priority modify — but it is dropped when
the final category is blocking, because there is no surviving call to rewrite.
The category is the join of the two: a deny or ask gate sets blocking/asking; a
`confirm`-mode modify with no gate raises asking; everything else allows.

### Diagnostics: localize then render

When the explain toggle is on, a non-match runs through `localize`, which walks
the failed conjuncts, classifies each (`KeyMissing`, `BoundViolation`,
`KindMismatch`, `DisjunctionFailed`), and attaches source spans. The renderer
turns that into the Rust-style caret frames you see — and it is deliberately
minimal: no "constraint not satisfied" restatement, a `want:` line only when
the caret doesn't already show the constraint, same-span labels collapsed under
one source line. The same localized data drives all three formats, so JSON and
SARIF carry the full structure (every disjunction arm's span, for instance) even
when the text renderer elides it for readability.

### Fail-open by default

An *engine* error — malformed input, a preprocessor failure — does not block the
user. The CLI folds it into an allowing envelope so a buggy hook can never wedge
a workflow. `--fail-closed` flips that to a blocking envelope that names the
underlying error, for production enforcement where a broken policy should stop
the call rather than wave it through. *Rule-loading* errors are always fatal,
though: a misconfigured policy is a configuration bug, surfaced loudly, not an
engine error to swallow.

### The catalog as single source of truth

`cue/catalog` holds pure name tables — `#ToolName`, `#AgentType`, `#EventName` —
with no wire-field binding. The `tool`, `agent`, and `hook` packages all
reference the catalog by field, so the member set has exactly one definition. A
typo in a catalog reference (`catalog.#ToolName.Bsh`) is an undefined field the
loader rejects, which is what turns "I misspelled a tool name" from a silent
policy gap into a load-time error. The catalog values are Claude Code's
identities; porting fas to another harness is, at the vocabulary level, shipping
a different catalog.

## Design motivations

- CUE, not a predicate DSL. A policy engine needs a constraint language:
  regex, bounds, list shapes, disjunction, negation. CUE already is one, with a
  formal subsumption check at its core. Borrowing it means there is no dialect to
  invent or maintain, and rules get CUE's whole expressive surface for free.
  Subsumption *is* matching.
- Patterns over predicates. A predicate (`fn(input) → bool`) is opaque: when
  it returns false you get nothing. A pattern is structural, so a non-match can
  be *localized* to the conjunct that failed and the input value that broke it.
  The diagnostic surface is a direct dividend of choosing patterns.
- Parsed facts over raw strings. Shell syntax is adversarial to regex —
  prefixes, compounds, quoting. Parsing once into a structured view and matching
  on that makes `sudo rm` and `rm` the same fact, and makes compound-command
  coverage fall out of the AST walk instead of needing a rule per shape.
- Fail-open engine, fail-loud config. The two error classes get opposite
  defaults on purpose: a runtime hiccup must not block the user (fail-open), but
  a broken rule file must not ship silently (fatal). They are different failures
  and deserve different dispositions.
- Layering with a single short-circuit. Global-then-project with only a
  blocking-deny short-circuit keeps the model predictable: a machine baseline can
  hard-stop a call, but everything softer composes, so per-project rules and
  global reminders coexist without surprising precedence.

## When something looks wrong

- A rule won't fire. Run `fas explain <rule_id> < event.json`. A no-match
  prints the failing conjunct with a caret; an absent-path `E0201` means a `when`
  path isn't in the input (check the parsed view with `FAS_LOG`).
- A rule fires when it shouldn't. Check the parsed view — `sudo`/compound
  forms surface in `parsed`, so a matcher reading raw `command` and one reading
  `parsed.commands` can disagree. Prefer the parsed matchers.
- `vet` rejects the tree. Read the code — `E0504`/`E0505` name the offending
  files (duplicate rule, conflicting package), `E0501`/`E0502` name the bad
  identifier or cross-rule reference, `E0506`/`E0507` flag a `let` or `if`/`for`
  inside `when` that would silently misfire.
- A `modify` rule is rejected. The `claude` harness can't honor `modify`;
  that's a capability error, not a bug.
- Decisions look merged oddly. Re-read [How decisions combine](#how-decisions-combine):
  one gate wins by `deny > ask > allow` then severity; effects accumulate
  independently.

## Where to look next

- [README](README.md) — the terse reference: every flag, env var, and build task.
- [AGENTS.md](AGENTS.md) — authoring rules in depth, the lint contract, and the
  stdlib test invariants.
- `tests/policies.md` — the shipped policy suite, every rule with its deny and
  near-miss-allow cases, as runnable scrut blocks.
- `tests/diagnostics.md` — the diagnostic surface, one block per error code and
  explain mode.
- `tests/guide.md` — the worked examples from this guide, runnable end to end.
