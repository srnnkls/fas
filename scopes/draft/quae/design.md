# quae — Design

> **quae** (Latin, "that which") — a CUE-based policy engine that determines *which* payloads match and *which* effects to emit.

---

## Architecture

```
┌─────────────┐     ┌──────────────┐     ┌──────────────┐
│   stdin      │────▶│ Go Adapter   │────▶│ CUE Validate │
│   (JSON)     │     │ ParseInput() │     │ #Input       │
└─────────────┘     └──────────────┘     └──────┬───────┘
                                                 │
                                          ┌──────▼───────┐
                                          │ Preprocessor │
                                          │ (Parsers)    │
                                          │ → parsed     │
                                          └──────┬───────┘
                                                 │
                                          ┌──────▼───────┐
                                          │ Signals      │
                                          │ (Wasm mods)  │
                                          │ → signals.*  │
                                          └──────┬───────┘
                                                 │
┌──────────────┐     ┌──────────────┐     ┌──────▼───────┐
│   stdout     │◀────│ Go Adapter   │     │ CUE          │
│   (JSON)     │     │ RenderOutput │     │ Evaluator    │
└──────────────┘     └──────┬───────┘     └──────┬───────┘
                            ▲                     │
                     ┌──────┴───────┐      ┌─────▼────────┐
                     │ Engine       │◀─────│ Synthesizer  │
                     │ (gate        │      │ Gate+Effects │
                     │  dispatch)   │      └──────────────┘
                     └──────────────┘
```

---

## Evaluation Pipeline

1. **Adapter (input):** `Adapter.ParseInput()` — compiled Go code normalizes vendor JSON to `Input`. Handles JSON-in-JSON parsing, field renaming, event type inference, tool name normalization. No user-configurable code.
2. **Schema validate:** Unify against `#Input` — defense-in-depth check that the adapter produced valid internal input.
3. **Preprocessor:** Dispatch to parser by `tool_name` — load `parser.cue` config, run backend (builtin, regex, tree-sitter, or lockfile-referenced module). Emit `#Parsed` at `tool_input.parsed`. Unknown tools pass through. Parsers write only to the `tool_input.parsed` namespace.
4. **Signals:** Compute `needed_signals = union(meta.requires)` across **all loaded rules** (global + project). Run only those Wasm modules. Attach results at `signals.<name>`. "Demand-driven" means only *referenced* signals run (statically), not conditionally on match.
5. **CUE evaluator:** Load rules, unify each rule's `when` against enriched input (including `signals.*`), evaluate `if` guards, collect actions from rules where `then` exists after evaluation.
6. **Synthesizer:** Produce an `OutputEnvelope`:

   * pick the winning **gate** (deny > ask > allow)
   * aggregate **inject** effects (sorted by `(priority, rule_id)`, deduped by `rule_id`, truncated by size budget)
   * select the winning **modify** effect (highest priority, tie-broken by `rule_id`)
   * **If the gate is Blocking:** drop `UpdatedInput` entirely (blocked actions have no payload to rewrite).
7. **Gate dispatch (Go, hardcoded):** Map the gate action to a category — Blocking/Asking/Allowing. Not configurable.
8. **Adapter (output):** `Adapter.RenderOutput()` — compiled Go code renders vendor-specific JSON from the full `OutputEnvelope`. Cannot change the category. Routes `UserReason`/`AgentReason` to vendor-appropriate fields by category and maps effects where supported.

---

## Two-Phase Evaluation

```
Global rules (~/.config/quae/rules/*.cue)
    │
    ├── gate = halt/deny/block? → short-circuit gate, but still collect effects
    │
    └── gate = ask/allow or effects only? → continue to project rules
                │
                ▼
Project rules (.quae/rules/*.cue)
    │
    └── synthesize gate + effects → OutputEnvelope
```

Effects (inject, modify) accumulate across both phases. Only the **gate** can short-circuit; effects always aggregate (subject to synthesizer rules like "drop UpdatedInput if Blocking").

---

## Tech Decisions

### Go + CUE

Go is the implementation language. CUE is the **only** policy language.

* `when` matches by **structural unification** (constraints unify against input).
* `if` guards handle cross-field logic (comparisons, arithmetic, existence branching).

No CEL/Rego/Cedar in the pipeline.

### Go Adapters

Adapters are compiled Go code. No user-configurable transforms exist between stdin and policy evaluation, or between policy evaluation and stdout. This is a deliberate security property.

#### Adapter Interface

```go
type Category int

const (
    Blocking Category = iota  // halt, deny, block
    Asking                     // ask (+confirm mode modify)
    Allowing                   // allow (+silent mode modify)
)

type OutputEnvelope struct {
    Category          Category
    UserReason        string
    AgentReason       string
    AdditionalContext string
    UpdatedInput      json.RawMessage
}

type Adapter interface {
    Name() string
    ParseInput(raw json.RawMessage) (*Input, error)
    RenderOutput(out OutputEnvelope) (json.RawMessage, error)
}
```

#### Gate Categories (Core-Owned)

| Category     | Gate actions                 | Meaning             |
| ------------ | ---------------------------- | ------------------- |
| **Blocking** | deny                         | Action is forbidden |
| **Asking**   | ask, modify(mode:"confirm")  | User must confirm   |
| **Allowing** | allow, modify(mode:"silent") | Action is permitted |

Inject and modify are **effects**; only the gate determines category.

#### Adapter Capabilities

Not every vendor hook protocol supports every effect. Adapters declare which effects they can render; rules that produce unsupported effects are rejected at **rule-load time** (not silently dropped at evaluation).

Claude Code's `PreToolUse` hook supports `allow` / `deny` / `ask` plus a reason string — it has **no payload-rewrite mechanism**. The Claude Code adapter therefore rejects rules that emit `modify` actions. `modify` remains defined for vendors whose protocols support input rewriting (e.g. Cursor, OpenCode).

---

## Data Model

### Internal Input Schema

Parsers write only to `tool_input.parsed`. Signals write only to `signals.<name>`.

```go
type Input struct {
    HookEventName string                 `json:"hook_event_name"`
    ToolName      string                 `json:"tool_name,omitempty"`
    ToolInput     json.RawMessage        `json:"tool_input,omitempty"`
    SessionID     string                 `json:"session_id,omitempty"`
    CWD           string                 `json:"cwd,omitempty"`
    Signals       map[string]SignalResult `json:"signals,omitempty"`
}

type SignalResult struct {
    OK   bool            `json:"ok"`
    Data json.RawMessage `json:"data,omitempty"`
    Err  string          `json:"err,omitempty"`
}
```

CUE schema:

```cue
#Input: {
    hook_event_name: string
    tool_name?:      string
    tool_input?: {
        command?: string
        parsed?:  #Parsed
        ...
    }
    session_id?: string
    cwd?:        string
    signals?: {[string]: #SignalResult}
}

#SignalResult: {
    ok:    bool
    data?: _
    err?:  string
}
```

### Rule Schema

```cue
#Rule: {
    when:  {...}
    then?: #Action
    meta?: #Meta
}

#Meta: {
    requires?: [...string]
}

#Action: #Deny | #Ask | #Modify | #Inject | #Allow

#Deny:  deny:  { rule_id: string, reason: string, severity: *"HIGH" | "CRITICAL" | "MEDIUM" | "LOW" }
#Ask:   ask:   { rule_id: string, reason: string, question: string }
#Allow: allow: true

#Inject: inject: {
    rule_id:  string
    priority: *50 | int & >=1 & <=100
    channel:  *"agent" | "user"
    text:     string
    tags?:    [...string]
}

#Modify: modify: {
    rule_id:       string
    reason:        string
    updated_input: _
    priority:      *50 | int & >=1 & <=100
    mode:          *"confirm" | "silent"
}
```

---

## Parsers

Parsers transform raw tool input into canonical `#Parsed` at `tool_input.parsed`.

* Builtin (Go)
* Regex (config-driven)
* Tree-sitter (embedded Wasm grammars)
* Wasm (lockfile module)

Parsers are **structure only**: they do not emit policy decisions and do not compute derived "booleans for rules".

### Action vocabulary

`parsed.actions` contains **semantic verbs** only (e.g. `"remove"`, `"delete"`, `"truncate"`). Command names (`rm`, `psql`, `dd`) are not in `actions` — they live on the raw input (`tool_input.command` for Bash) or in tool-specific parser output. Rules that want to match a specific executable match the raw field; rules that want to match intent match `actions`.

---

## Signals

Signals are Wasm modules that enrich input at `signals.<name>`.

* `needed_signals = union(meta.requires)` across **all loaded rules** (global + project)
* A signal not referenced by any rule never runs
* Results are always `#SignalResult`

---

## Modules & Lockfile

Executable artifacts must be declared and pinned by hash.

```cue
#Module: {
    name:    string
    kind:    "signal" | "parser"
    format:  "wasm"
    sha256:  string

    deps?: [...string]

    limits: {
        mem_mb:      *16 | int & >=1 & <=256
        fuel:        *1000000 | int & >=1000
        timeout_ms:  *5000 | int & >=100 & <=30000
        max_out_kb:  *64 | int & >=1 & <=1024
    }
    caps: {
        wasi_fs:  *false | bool
        wasi_net: *false | bool
    }
}

modules: [...#Module]
```

Resolution:

```
1. quae.lock.cue
2. ~/.config/quae/modules/<n>.wasm (verified against sha256)
```

---

## CUE Matching Strategy

### Design Principles

1. **No derived booleans.** Rules are constraints; the preprocessor emits structure only.
2. **Use definitions/aliases, not hidden fields.** Prefer `#Something` over `_something` for reusable logic.
3. **Lists are the source of truth.** Derive `or()` and regexes from lists.
4. **Three-layer pattern.** Value lists → derived constraints → structural constraints.

### The Standard Library (`quae.cue`)

The stdlib has two responsibilities:

1. Ship **stable vocabulary** (paths, action names, flag specs).
2. Provide **composable constraints** ("traits") that attach validators to canonical fields.

#### Layer 1 — Value Lists

```cue
package quae

#SystemPrefixes:     ["/etc", "/sys", "/proc", "/boot", "/dev"]
#EscalationCommands: ["sudo", "doas", "su"]

// Cross-tool action vocabulary — semantic verbs only (normalized by parsers into parsed.actions).
// Command names like "rm" stay on the raw input, not here.
#DestructiveActions: ["delete", "drop", "remove", "destroy", "truncate"]
```

#### Layer 2 — Derived Constraints (Regex / Disjunction)

```cue
import "strings"

#systemTarget:      =~"^(\(strings.Join(#SystemPrefixes, "|")))"
#escalationCommand: or(#EscalationCommands)
#destructiveAction: or(#DestructiveActions)
```

#### Layer 3 — Structural Constraints

```cue
import "list"

#hasSystemTarget: {
    tool_input: parsed: targets: list.MatchN(>0, #systemTarget)
}

#hasPrivilegeEscalation: {
    tool_input: parsed: attributes: prefix_commands: list.MatchN(>0, #escalationCommand)
}

#hasDestructiveAction: {
    tool_input: parsed: actions: list.MatchN(>0, #destructiveAction)
}

#isPreToolUse: { hook_event_name: "PreToolUse" }
#isUserPrompt: { hook_event_name: "UserPromptSubmit" }
#isBash:       { tool_name: "Bash" }
```

> **`list.MatchN` argument shape.** The second argument is a **single schema**, not a list of matchers. CUE `list.MatchN(>0, S)` succeeds when more than N elements unify with `S`. Wrapping `S` in `[S]` would require each element to itself be a one-element list — which is almost never what you want.

### Flags: Per-Tool Constraint Files

Flags are *tool-specific vocabulary*. Each tool's flag set lives in its own file as **inline per-flag constraints** — no template, no comprehension, no compose-via-building-block.

Key properties:

* No "semantic option parsing" (no `{"force": true}` normalization).
* Works with the canonical representation: a **list of flag tokens** (`parsed.flags`).
* Handles **short-combos** safely by constraining combos to the *known short-letter set* for that tool (avoids false positives like `-force` matching `-r` just because it contains `r`).

#### Building Block (standalone): `#HasFlagMatching`

```cue
package quae

import "list"

#HasFlagMatching: {
    #re: string
    tool_input: parsed: flags: list.MatchN(>0, =~#re)
}
```

Use this **only in isolation** when parameterizing a regex from outside (e.g. tests, ad-hoc rules).

> **Why per-flag constraints don't compose via `#HasFlagMatching`.** The obvious shorthand — `#HasRmForce: #HasFlagMatching & {#re: "^--force..."}` — looks cleaner, but it fails AND-composition. CUE exposes `#re` as a struct field on the unified value; unifying `#HasRmForce & #HasRmRecursive` produces conflicting `#re` values ("force" regex vs "recursive" regex) and the unification errors. The per-flag constraints must therefore **inline** the `list.MatchN` directly, with no `#re` field on the exposed shape.

#### Per-Tool Example (`rm`)

```cue
package quae

import "list"

// rm's known short-flag letters. Keep this string in sync with the
// set of #HasRm* constraints defined below.
#rmShortClass: "friv"

// Each constraint matches:
//   --long | --long=... | -long | -long=...  (long form, dashed or Go-style single-dash)
//   -[friv]*X[friv]*                         (short-combo containing letter X, made only of known rm letters)

#HasRmForce: {
    tool_input: parsed: flags: list.MatchN(>0, =~"^--force(=|$)|^-force(=|$)|^-[\(#rmShortClass)]*f[\(#rmShortClass)]*$")
}

#HasRmRecursive: {
    tool_input: parsed: flags: list.MatchN(>0, =~"^--recursive(=|$)|^-recursive(=|$)|^-[\(#rmShortClass)]*r[\(#rmShortClass)]*$")
}

#HasRmInteractive: {
    tool_input: parsed: flags: list.MatchN(>0, =~"^--interactive(=|$)|^-interactive(=|$)|^-[\(#rmShortClass)]*i[\(#rmShortClass)]*$")
}

#HasRmVerbose: {
    tool_input: parsed: flags: list.MatchN(>0, =~"^--verbose(=|$)|^-verbose(=|$)|^-[\(#rmShortClass)]*v[\(#rmShortClass)]*$")
}
```

Each `#HasRm*` is a **pure constraint** with no exposed fields beyond `tool_input.parsed.flags`, so multiple `#HasRm*` constraints AND-compose cleanly. Rules combine them directly via unification.

### AND / OR Composition

Because these are **pure constraints** (no generated fields), they compose cleanly:

```cue
// AND: require both
when: #isPreToolUse & #isBash & (#HasRmForce & #HasRmRecursive)

// OR: require either
when: #isPreToolUse & #isBash & (#HasRmForce | #HasRmRecursive)
```

---

## API Contract

### CLI

```
quae eval [--harness <vendor>] [--config <path>] [--fail-closed]
quae validate-adapter <vendor> [--fixture <path>]
quae validate-parser <tool> [--fixture <path>]
quae validate-modules
quae validate-rules
```

---

## Config Resolution Order

```
Rules:
  1. .quae/rules/*.cue                         (project)
  2. ~/.config/quae/rules/*.cue                (global)

Adapters:
  3. compiled into the binary                  (--harness)

Parsers:
  4. ~/.config/quae/parsers/<tool>/parser.cue  (user overrides)
  5. builtin parsers                           (shipped)

Modules:
  6. quae.lock.cue                             (manifest)
  7. ~/.config/quae/modules/<n>.wasm           (verified artifacts)

Schemas:
  8. builtin schema.cue                         (#Input, #Parsed, #Decision)
```

---

## Invariants

1. Parsers normalize to `#Parsed` (actions, targets, flags, attributes). No tool-specific shapes leak to the policy layer.
2. Parsers and signals write only to fixed namespaces: `tool_input.parsed`, `signals.*`.
3. Rules are pure constraints. No side effects, no I/O in CUE.
4. Gate priority is fixed: deny > ask > allow. Exactly one gate wins.
5. Modify: at most one wins (highest priority, tie by `rule_id`). **Dropped if gate is Blocking.**
6. Inject: many accumulate, sorted and size-truncated.
7. Adapters are compiled Go; no user-configurable transforms on stdin→eval or eval→stdout.
8. Global before project; blocking gates short-circuit phase 2, but effects aggregate across phases.
9. Signals are static-demand-driven: `union(meta.requires)` across all loaded rules.
10. Pure Go build: `CGO_ENABLED=0`; deps are pure Go or Wasm.
11. Adapter capabilities are checked at rule-load time; unsupported effects (e.g. `modify` on Claude Code) cause load failure, not silent drop.

---

## Non-Goals

- **Session state.** Each hook event is evaluated independently. No cross-call accumulation ("agent has tried this three times", "already approved this pattern once"). Rules are pure constraints over a single `#Input`; there is no state backend. Revisit post-v0.3 if real use cases emerge.
- **`modify` on Claude Code.** CC's `PreToolUse` hook has no payload-rewrite mechanism. The CC adapter rejects `modify` actions at rule-load time. Forward-compatible for Cursor / OpenCode.
- **jq parser backend.** Deferred until a sandboxed execution path exists. `gojq` is reproducible but lacks fuel/memory bounds — unacceptable for a security-critical hot path. Parser backends in v0.3 are: builtin Go, regex, tree-sitter (Wasm grammars), Wasm.
