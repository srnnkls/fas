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

   * pick the winning **gate** (halt > deny/block > ask > allow)
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
| **Blocking** | halt, deny, block            | Action is forbidden |
| **Asking**   | ask, modify(mode:"confirm")  | User must confirm   |
| **Allowing** | allow, modify(mode:"silent") | Action is permitted |

Inject and modify are **effects**; only the gate determines category.

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

#Action: #Halt | #Deny | #Block | #Ask | #Modify | #Inject | #Allow

#Halt:  halt:  { rule_id: string, reason: string, severity: *"CRITICAL" | "HIGH" | "MEDIUM" | "LOW" }
#Deny:  deny:  { rule_id: string, reason: string, severity: *"HIGH" | "CRITICAL" | "MEDIUM" | "LOW" }
#Block: block: { rule_id: string, reason: string, severity: *"HIGH" | "CRITICAL" | "MEDIUM" | "LOW" }
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
* jq (lockfile module; runs in-process via gojq; reproducible but not fuel/mem sandboxed)

Parsers are **structure only**: they do not emit policy decisions and do not compute derived "booleans for rules".

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
    format:  *"wasm" | "jq"
    sha256:  string

    deps?: [...string]

    if format == "wasm" {
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
}

modules: [...#Module]
```

Resolution:

```
1. quae.lock.cue
2. ~/.config/quae/modules/<n>.wasm or <n>.jq (verified against sha256)
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

// Cross-tool action vocabulary (normalized by parsers into parsed.actions)
#DestructiveActions: ["delete", "drop", "remove", "rm", "destroy", "truncate"]
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
    tool_input: parsed: targets: list.MatchN(>0, [#systemTarget])
}

#hasPrivilegeEscalation: {
    tool_input: parsed: attributes: prefix_commands: list.MatchN(>0, [#escalationCommand])
}

#hasDestructiveAction: {
    tool_input: parsed: actions: list.MatchN(>0, [#destructiveAction])
}

#isPreToolUse: { hook_event_name: "PreToolUse" }
#isUserPrompt: { hook_event_name: "UserPromptSubmit" }
#isBash:       { tool_name: "Bash" }
```

### Standardlib Augmentation: Flags as `#Flags` (Template-Based Vocabulary)

Flags are a *tool-specific vocabulary*. The stdlib provides a **template** to define a flag set once and then consume it ergonomically from rules.

Key properties:

* No "semantic option parsing" (no `{"force": true}` normalization).
* Works with the canonical representation everybody expects: a **list of flag tokens** (`parsed.flags`).
* Handles **short-combos** safely by constraining combos to the *known short-letter set* for that tool (avoids false positives like `-force` matching "-r" just because it contains an `r`).

#### Building Block: "has a flag token matching regex"

```cue
package quae

import "list"

#HasFlagMatching: {
    #re: string
    tool_input: parsed: flags: list.MatchN(>0, [=~#re])
}
```

#### Template: Define a FlagSet once, get `.when` traits for free

```cue
package quae

import "strings"

// A FlagSet is a tool-specific map: name -> {short?, long?}
#FlagSpec: {
    short?: string  // single letter (e.g. "f")
    long?:  string  // long name (e.g. "force")
}

#FlagSet: {
    // user provides these entries:
    [Name=_]: #FlagSpec

    // derived short-letter class for safe short-combo matching (e.g. "friv")
    #shortLetters: [ for _, v in _|_ {
        // this comprehension is "templated" below; see note
    } ]

    // Template applied per field: adds a stable `.when` constraint per flag name.
    [Name=_]: {
        name: Name

        // Optional: accept both --long and -long (Go-style single-dash long).
        // Also accept --long=value.
        // For short-combos: match only tokens made of known short letters.
        //
        // NOTE: the short-combo part is only enabled when `short` is set.
        re: string

        when: #HasFlagMatching & { #re: re }
    }
}
```

CUE doesn't let us write a true "function", but templates *do* let us stamp per-flag constraints idiomatically. Here's the concrete, fully working version for a **specific tool** (example: `rm`), which is how you'll actually ship it:

```cue
package quae

import (
    "strings"
)

// rm's *known* short flags (subset; extend as desired)
#RmFlags: {
    force:     { short: "f", long: "force" }
    recursive: { short: "r", long: "recursive" }
    interactive: { short: "i", long: "interactive" }
    verbose:     { short: "v", long: "verbose" }

    // Derived: character class for rm's short flags.
    #shortClass: "\(strings.Join([
        for _, v in #RmFlags if v.short != _|_ { v.short }
    ], ""))"

    // Template: generates `.when` for each flag entry.
    [Name=_]: {
        name: Name

        // long forms:
        //   --force, --force=..., -force, -force=...
        // short forms:
        //   -f, -rf, -vrf (but only if token is made of rm's known short letters)
        re: *(
            // long (double dash)
            "^--\(long)(=|$)" +
            // OR long (single dash)
            "|^-\(long)(=|$)" +
            // OR short/short-combo (safe)
            "|^-[\(#shortClass)]+$"
        ) | string

        // For a particular flag, require the token both:
        //  - be a valid rm short-combo token, AND
        //  - contain that specific letter somewhere.
        //
        // We do this by specializing `re` further per-flag below.
    }

    // Specialize per flag to "contain the specific short letter"
    force: {
        re: "^--\(long)(=|$)|^-\(long)(=|$)|^-[\(#shortClass)]*f[\(#shortClass)]*$"
    }
    recursive: {
        re: "^--\(long)(=|$)|^-\(long)(=|$)|^-[\(#shortClass)]*r[\(#shortClass)]*$"
    }
    interactive: {
        re: "^--\(long)(=|$)|^-\(long)(=|$)|^-[\(#shortClass)]*i[\(#shortClass)]*$"
    }
    verbose: {
        re: "^--\(long)(=|$)|^-\(long)(=|$)|^-[\(#shortClass)]*v[\(#shortClass)]*$"
    }

    // Export the actual composable constraints
    force:     { when: #HasFlagMatching & { #re: re } }
    recursive: { when: #HasFlagMatching & { #re: re } }
    interactive: { when: #HasFlagMatching & { #re: re } }
    verbose:     { when: #HasFlagMatching & { #re: re } }
}

// Convenience aliases (optional)
#HasForce:     #RmFlags.force.when
#HasRecursive: #RmFlags.recursive.when
```

### AND / OR Composition

Because these are **pure constraints** (no generated fields), they compose cleanly:

```cue
// AND: require both
when: #isPreToolUse & #isBash & (#HasForce & #HasRecursive)

// OR: require either
when: #isPreToolUse & #isBash & (#HasForce | #HasRecursive)
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
  7. ~/.config/quae/modules/<n>.wasm|.jq        (verified artifacts)

Schemas:
  8. builtin schema.cue                         (#Input, #Parsed, #Decision)
```

---

## Invariants

1. Parsers normalize to `#Parsed` (actions, targets, flags, attributes). No tool-specific shapes leak to the policy layer.
2. Parsers and signals write only to fixed namespaces: `tool_input.parsed`, `signals.*`.
3. Rules are pure constraints. No side effects, no I/O in CUE.
4. Gate priority is fixed: halt > deny/block > ask > allow. Exactly one gate wins.
5. Modify: at most one wins (highest priority, tie by `rule_id`). **Dropped if gate is Blocking.**
6. Inject: many accumulate, sorted and size-truncated.
7. Adapters are compiled Go; no user-configurable transforms on stdin→eval or eval→stdout.
8. Global before project; blocking gates short-circuit phase 2, but effects aggregate across phases.
9. Signals are static-demand-driven: `union(meta.requires)` across all loaded rules.
10. Pure Go build: `CGO_ENABLED=0`; deps are pure Go or Wasm.
