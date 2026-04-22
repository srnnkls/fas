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
	Help: `The ` + "`then.kind`" + ` value names an action that quae does not recognize.

Action kinds are a closed set registered at compile time. A typo
(` + "`blok`" + ` instead of ` + "`block`" + `) or an action from a newer quae release
will both produce this code. Check the spelling against the action
catalog and the version of quae you are running.`,
}

var E0103 = CodeInfo{
	Code: "E0103",
	Help: `A top-level field in the rule file uses a name reserved by quae.

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
diagnostic shows the expected pattern (` + "`want`" + `) and the concrete input
(` + "`got`" + `); compare the two to decide whether to loosen the pattern or
fix the producer of the input.`,
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
Under quae's closed-world pattern matching the notion of a "default"
arm is ill-defined; remove the mark and let the arms stand on their
own merits.`,
}

// E05xx — scope / binding.

var E0501 = CodeInfo{
	Code: "E0501",
	Help: `An identifier in the rule does not resolve to any visible binding.

Rules may only reference fields declared within the rule itself or
values exported from the quae standard library. A typo or a
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
	Help: `A ` + "`then`" + ` or ` + "`meta`" + ` block references the rule's own ` + "`when`" + ` fields.

The action and metadata phases run after matching and must not
depend on the pattern structure; such self-references conflate the
match phase with the reaction phase. Rewrite the action to use
constants or values from the resolved input rather than ` + "`when`" + `.`,
}

// codeRegistry maps each stable code string to its CodeInfo.
// Built at package init so that duplicate codes fail loudly rather
// than silently overwriting each other.
var codeRegistry = buildCodeRegistry(
	E0101, E0102, E0103,
	E0201, E0202, E0203,
	E0301, E0302, E0303, E0304,
	E0401, E0402,
	E0501, E0502, E0503,
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
