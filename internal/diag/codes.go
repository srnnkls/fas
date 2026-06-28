package diag

import "fmt"

// CodeInfo pairs a stable error code with an explanatory help string.
// Codes are stable across versions; changing one is a breaking change
// for anyone filtering or grepping diagnostic output.
type CodeInfo struct {
	Code string
	Help string
}

// E01xx — rule load.

var E0101 = CodeInfo{
	Code: "E0101",
	Help: `The ` + "`then`" + ` block does not conform to the action schema.

Every rule must declare a ` + "`then`" + ` whose shape matches one of the known
action kinds (e.g. block, allow, rewrite). A missing or misnamed field,
a wrong type, or an unknown action kind will surface here. Check the
action's required fields against the rule definition.`,
}

var E0102 = CodeInfo{
	Code: "E0102",
	Help: `The ` + "`then.kind`" + ` value names an action that fas does not recognize.

Action kinds are a closed set registered at compile time. A typo
(` + "`blok`" + ` instead of ` + "`block`" + `) or an action from a newer fas release
will both produce this code. Check the spelling against the action
catalog and the version of fas you are running.`,
}

var E0103 = CodeInfo{
	Code: "E0103",
	Help: `A top-level field in the rule file uses a name reserved by fas.

Names like ` + "`then`" + `, ` + "`when`" + `, ` + "`meta`" + ` and a handful of others are part of
the rule schema and cannot be redefined as user rules. Rename the
offending field to something unambiguous, or move it under ` + "`meta`" + ` if
it is documentation.`,
}

// E02xx — path resolution.

var E0201 = CodeInfo{
	Code: "E0201",
	Help: `A path segment referenced in the rule does not exist in the input.

Under closed-world pattern-match semantics, every path referenced in
` + "`when`" + ` must exist in the input for the rule to match. Absent paths
cause the rule to silently not fire; the diagnostic shows which
segment broke the chain.`,
}

var E0202 = CodeInfo{
	Code: "E0202",
	Help: `A path segment indexes into a value that is not a list.

The rule uses a numeric index like ` + "`items[0]`" + ` but the corresponding
input value is a scalar or an object. Either the rule should address
the value by key, or the input shape is not what the rule expected.`,
}

var E0203 = CodeInfo{
	Code: "E0203",
	Help: `A list index in the rule exceeds the bounds of the input list.

The rule references ` + "`items[N]`" + ` but the input list has fewer than N+1
elements. Either shrink the index, guard the path with a length
check, or verify the producer of the input is emitting the expected
number of entries.`,
}

// E03xx — leaf constraint.

var E0301 = CodeInfo{
	Code: "E0301",
	Help: `A string leaf does not satisfy the regex declared in the rule.

Regex constraints in ` + "`when`" + ` must match the full input string. The
primary caret row underlines the pattern; the ` + "`got:`" + ` label echoes the
concrete input. Compare the two to decide whether to loosen the
pattern or fix the producer of the input. References that resolve to
a regex (e.g. ` + "`#DangerousCmds`" + `) additionally surface the expanded
pattern via a ` + "`want:`" + ` label.`,
}

var E0302 = CodeInfo{
	Code: "E0302",
	Help: `A numeric leaf falls outside the range declared in the rule.

Range constraints (` + "`>= 0 & < 100`" + ` and friends) are closed bounds.
The diagnostic shows the expected range and the concrete value;
decide whether the rule's bounds are wrong or the input is out of
spec for the policy.`,
}

var E0303 = CodeInfo{
	Code: "E0303",
	Help: `A leaf value has a different type than the rule constraint requires.

The rule expects, say, a string and the input carries a number, or
vice versa. CUE subsumption is strict about types; coerce the input
at the producer or rewrite the rule to accept the actual shape.`,
}

var E0304 = CodeInfo{
	Code: "E0304",
	Help: `A leaf value is not a member of the allowed set declared in the rule.

Enum-style constraints (` + "`\"a\" | \"b\" | \"c\"`" + `) accept only the listed
values. The diagnostic shows the allowed set and the concrete value;
extend the set or fix the input to match one of the accepted
alternatives.`,
}

// E04xx — disjunction.

var E0401 = CodeInfo{
	Code: "E0401",
	Help: `All arms of a disjunction failed to subsume the input.

A disjunction ` + "`A | B | C`" + ` matches when any arm matches; this code
fires when every arm rejected the input. The diagnostic highlights
each arm's span with the specific reason it failed, so you can tell
which arm was closest to matching.`,
}

var E0402 = CodeInfo{
	Code: "E0402",
	Help: `A disjunction uses a CUE default mark under pattern-match semantics.

Default marks (` + "`*`" + `) change which arm wins when an input is ambiguous.
Under fas's closed-world pattern matching the notion of a "default"
arm is ill-defined; remove the mark and let the arms stand on their
own merits.`,
}

// E05xx — scope / binding.

var E0501 = CodeInfo{
	Code: "E0501",
	Help: `An identifier in the rule does not resolve to any visible binding.

Rules may only reference fields declared within the rule itself or
values exported from the fas standard library. A typo or a
reference to something not in scope surfaces here; check the name
against the local declarations and the available stdlib imports.`,
}

var E0502 = CodeInfo{
	Code: "E0502",
	Help: `A rule references an identifier declared inside a different rule.

Rules are isolated units; cross-rule references would couple their
evaluation order and break composability. If the shared value is
genuinely common, extract it as a hidden sibling (leading
underscore) at the file top level where both rules can see it.`,
}

var E0503 = CodeInfo{
	Code: "E0503",
	Help: `A ` + "`when`" + ` block references the rule's own ` + "`then`" + ` or ` + "`meta`" + ` fields.

The match phase sees only the input; fields declared in ` + "`then`" + ` or
` + "`meta`" + ` are not yet in scope when ` + "`when`" + ` evaluates. Rewrite the
pattern so it depends only on the input and, if a constant is
needed, lift it to a hidden sibling (leading underscore).`,
}

var E0504 = CodeInfo{
	Code: "E0504",
	Help: `Two rule files in a directory declare the same top-level rule name.

A rules directory is loaded as one merged CUE package, so a plain
top-level rule label must be unique across every ` + "`.cue`" + ` file in it.
Two files defining the same name would silently collapse into one
rule (or collide), losing one author's intent. Rename one of the
rules, or move the divergent file into its own directory. Hidden
helpers (` + "`_x`" + `) and definitions (` + "`#X`" + `) are exempt: those are meant
to be shared across files.`,
}

var E0505 = CodeInfo{
	Code: "E0505",
	Help: `Two or more distinct explicit ` + "`package`" + ` names appear in one directory.

A rules directory is loaded as one merged CUE package, so its files may
declare at most one explicit ` + "`package`" + ` name. Files that omit the clause (or
use ` + "`package _`" + `) adopt that name automatically. When two files name
different packages the merge is ambiguous; rename them to one shared package,
or split the divergent files into their own directory.`,
}

var E0508 = CodeInfo{
	Code: "E0508",
	Help: "`len` inside `" + `when` + "` computes over the pattern's materialised value, not the input's." + `

` + "`_n: len(flags)`" + ` where ` + "`flags: [...string]`" + ` always yields ` + "`0`" + ` because the
pattern materialises the open list as ` + "`[]`" + `. A downstream constraint like
` + "`_n: >=2`" + ` either conflicts statically or is vacuously true — either way,
it cannot react to the input's actual list length. Use ` + "`list.MatchN`" + `
instead: ` + "`flags: list.MatchN(>=2, string)`" + `.`,
}

// E06xx — lattice binding.

var E0601 = CodeInfo{
	Code: "E0601",
	Help: `Fields annotated with the same @bind variable resolved to different values.

Two or more fields in ` + "`when`" + ` carry ` + "`@bind(X)`" + ` with the same variable
name. At match time, the concrete input values at those paths must be
equal (they must unify to the same point in the lattice). This diagnostic
fires when the input structurally matched the pattern but the bound values
diverged — e.g. ` + "`command`" + ` was ` + "`\"cat\"`" + ` while ` + "`targets[0]`" + ` was ` + "`\"dog\"`" + `.`,
}

// CodesInScopeV1 freezes the code count for this scope; bumping it requires
// a deliberate design review to justify adding a new code.
const CodesInScopeV1 = 19

// codeRegistry maps each stable code string to its CodeInfo.
// Built at package init so that duplicate codes fail loudly rather
// than silently overwriting each other.
var codeRegistry = buildCodeRegistry(
	E0101, E0102, E0103,
	E0201, E0202, E0203,
	E0301, E0302, E0303, E0304,
	E0401, E0402,
	E0501, E0502, E0503, E0504, E0505, E0508,
	E0601,
)

func buildCodeRegistry(entries ...CodeInfo) map[string]CodeInfo {
	m := make(map[string]CodeInfo, len(entries))
	for _, e := range entries {
		if _, dup := m[e.Code]; dup {
			panic(fmt.Sprintf("diag: duplicate code registration %q", e.Code))
		}
		m[e.Code] = e
	}
	return m
}

// LookupCode returns the CodeInfo registered for code and true, or the
// zero CodeInfo and false if no such code is declared. Lookup is
// case-sensitive; "E0201" and "e0201" are distinct keys.
func LookupCode(code string) (CodeInfo, bool) {
	info, ok := codeRegistry[code]
	return info, ok
}
