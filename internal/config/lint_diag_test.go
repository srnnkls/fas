package config_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cuelang.org/go/cue/token"

	"github.com/srnnkls/fas/internal/config"
	"github.com/srnnkls/fas/internal/diag"
)

// lintFixture stages a single .cue file under a temp dir and returns its
// on-disk path. The stem drives the file name so each test owns its source.
func lintFixture(t *testing.T, stem, src string) string {
	t.Helper()
	dir := t.TempDir()
	return writeRuleFile(t, dir, stem+".cue", src)
}

// recoverDiag walks err's tree (including errors.Join aggregates) and returns
// the first *diag.DiagError reachable via errors.As, plus whether one was
// found. Tests use this to assert on structured diagnostic content rather than
// rendered error strings.
func recoverDiag(t *testing.T, err error) (*diag.DiagError, bool) {
	t.Helper()
	if err == nil {
		return nil, false
	}
	var de *diag.DiagError
	if errors.As(err, &de) {
		return de, de != nil
	}
	return nil, false
}

// collectDiags returns every *diag.DiagError reachable from err, including
// those joined with errors.Join. Order follows the tree walk used by the
// stdlib unwrap helpers.
func collectDiags(err error) []*diag.DiagError {
	var out []*diag.DiagError
	var visit func(error)
	visit = func(e error) {
		if e == nil {
			return
		}
		var de *diag.DiagError
		if errors.As(e, &de) {
			out = append(out, de)
		}
		// Descend through Join aggregates.
		if multi, ok := e.(interface{ Unwrap() []error }); ok {
			for _, child := range multi.Unwrap() {
				visit(child)
			}
			return
		}
		if wrapped := errors.Unwrap(e); wrapped != nil {
			visit(wrapped)
		}
	}
	visit(err)
	return out
}

// tokenAtPos reads the rule file from disk and returns up to `span` bytes
// starting at pos. Used to verify that a diagnostic position actually points
// at the expected source token rather than hard-coding line numbers.
func tokenAtPos(t *testing.T, pos token.Pos, span int) string {
	t.Helper()
	if !pos.IsValid() {
		t.Fatalf("position must be valid; got %v", pos)
	}
	data, err := os.ReadFile(pos.Filename())
	if err != nil {
		t.Fatalf("read source file %q: %v", pos.Filename(), err)
	}
	offset := pos.Offset()
	if offset < 0 || offset >= len(data) {
		t.Fatalf("pos offset %d out of bounds for source length %d", offset, len(data))
	}
	end := min(offset+span, len(data))
	return string(data[offset:end])
}

// TestLoadRules_LintDiag_CrossRuleRef_EmitsE0502 pins that a cross-rule
// selector (`other_rule.when.tool_name`) surfaces as E0502 with the primary
// span anchored at the selector's root ident.
func TestLoadRules_LintDiag_CrossRuleRef_EmitsE0502(t *testing.T) {
	const src = `package rules

base_rule: {
	when: {tool_name: "Bash"}
	then: deny: {
		rule_id: "base"
		reason:  "nope"
	}
}

consumer_rule: {
	when: {tool_name: base_rule.when.tool_name}
	then: deny: {
		rule_id: "consumer"
		reason:  "nope"
	}
}
`
	path := lintFixture(t, "xrule", src)
	dir := filepath.Dir(path)

	_, err := config.LoadRules(dir)
	if err == nil {
		t.Fatal("expected cross-rule ref to be rejected, got nil error")
	}

	de, ok := recoverDiag(t, err)
	if !ok {
		t.Fatalf("expected err to carry *diag.DiagError via errors.As; got: %v", err)
	}
	if de.D.Code != "E0502" {
		t.Errorf("diagnostic Code = %q, want %q", de.D.Code, "E0502")
	}

	// The primary span must anchor at the selector's ROOT ident
	// (`base_rule`), not at `.when` or `.tool_name`. Reading the source at
	// the reported offset must yield the literal token text.
	got := tokenAtPos(t, de.D.Primary.Pos, len("base_rule"))
	if got != "base_rule" {
		t.Errorf("Primary.Pos should point at the selector root ident `base_rule`; got %q", got)
	}
	if de.D.Help == "" {
		t.Errorf("E0502 diagnostic should carry a Help string suggesting remediation; got empty")
	}
}

// TestLoadRules_LintDiag_SelfRefIntoThen_EmitsE0503 pins that a `when`
// expression walking its own rule's `then` subtree surfaces as E0503 with the
// primary span on the selector.
func TestLoadRules_LintDiag_SelfRefIntoThen_EmitsE0503(t *testing.T) {
	const src = `package rules

self_then: {
	when: {tool_name: "Bash", marker: self_then.then.deny.rule_id}
	then: deny: {
		rule_id: "sr"
		reason:  "nope"
	}
}
`
	path := lintFixture(t, "sthen", src)
	dir := filepath.Dir(path)

	_, err := config.LoadRules(dir)
	if err == nil {
		t.Fatal("expected self-ref into then to be rejected, got nil error")
	}

	de, ok := recoverDiag(t, err)
	if !ok {
		t.Fatalf("expected err to carry *diag.DiagError via errors.As; got: %v", err)
	}
	if de.D.Code != "E0503" {
		t.Errorf("diagnostic Code = %q, want %q", de.D.Code, "E0503")
	}

	// The primary span anchors at the SelectorExpr root — the rule's own
	// name `self_then`. That is what makes the ref structurally self-directed.
	got := tokenAtPos(t, de.D.Primary.Pos, len("self_then"))
	if got != "self_then" {
		t.Errorf("Primary.Pos should point at the self-ref root ident `self_then`; got %q", got)
	}
}

// TestLoadRules_LintDiag_SelfRefIntoMeta_EmitsE0503 mirrors the then-ref case
// for a `when` that reaches into the rule's own `meta` subtree.
func TestLoadRules_LintDiag_SelfRefIntoMeta_EmitsE0503(t *testing.T) {
	const src = `package rules

self_meta: {
	when: {tool_name: "Bash", tag: self_meta.meta.requires[0]}
	then: deny: {
		rule_id: "sm"
		reason:  "nope"
	}
	meta: {requires: ["signal_foo"]}
}
`
	path := lintFixture(t, "smeta", src)
	dir := filepath.Dir(path)

	_, err := config.LoadRules(dir)
	if err == nil {
		t.Fatal("expected self-ref into meta to be rejected, got nil error")
	}

	de, ok := recoverDiag(t, err)
	if !ok {
		t.Fatalf("expected err to carry *diag.DiagError via errors.As; got: %v", err)
	}
	if de.D.Code != "E0503" {
		t.Errorf("diagnostic Code = %q, want %q", de.D.Code, "E0503")
	}

	got := tokenAtPos(t, de.D.Primary.Pos, len("self_meta"))
	if got != "self_meta" {
		t.Errorf("Primary.Pos should point at the self-ref root ident `self_meta`; got %q", got)
	}
}

// TestLoadRules_LintDiag_UnboundIdentifier_EmitsE0501 pins that a bare
// identifier in `when` with no visible binding surfaces as E0501 with the
// primary span anchored at the ident and a Help mentioning the available
// resolution scopes.
func TestLoadRules_LintDiag_UnboundIdentifier_EmitsE0501(t *testing.T) {
	const src = `package rules

uid_rule: {
	when: {tool_name: myUnknownVar}
	then: deny: {
		rule_id: "uid"
		reason:  "nope"
	}
}
`
	path := lintFixture(t, "uid", src)
	dir := filepath.Dir(path)

	_, err := config.LoadRules(dir)
	if err == nil {
		t.Fatal("expected unbound identifier to be rejected, got nil error")
	}

	de, ok := recoverDiag(t, err)
	if !ok {
		t.Fatalf("expected err to carry *diag.DiagError via errors.As; got: %v", err)
	}
	if de.D.Code != "E0501" {
		t.Errorf("diagnostic Code = %q, want %q", de.D.Code, "E0501")
	}

	got := tokenAtPos(t, de.D.Primary.Pos, len("myUnknownVar"))
	if got != "myUnknownVar" {
		t.Errorf("Primary.Pos should point at the unbound ident `myUnknownVar`; got %q", got)
	}

	// Help must steer the author toward the documented escape hatches:
	// hidden siblings and stdlib imports. We check both terms rather than
	// pinning an exact phrasing.
	if !strings.Contains(de.D.Help, "hidden") {
		t.Errorf("E0501 Help should mention hidden siblings; got: %q", de.D.Help)
	}
	if !strings.Contains(strings.ToLower(de.D.Help), "stdlib") &&
		!strings.Contains(strings.ToLower(de.D.Help), "import") {
		t.Errorf("E0501 Help should mention stdlib imports; got: %q", de.D.Help)
	}
}

// TestLoadRules_LintDiag_CleanRule_NoE05xx is the regression guard: a rule
// that uses only legal refs (a local hidden sibling) must load without
// producing any E05xx diagnostic.
func TestLoadRules_LintDiag_CleanRule_NoE05xx(t *testing.T) {
	const src = `package rules

clean_rule: {
	_local_tool: "Bash"
	when: {tool_name: _local_tool}
	then: deny: {
		rule_id: "clean"
		reason:  "nope"
	}
}
`
	path := lintFixture(t, "clean", src)
	dir := filepath.Dir(path)

	_, err := config.LoadRules(dir)
	if err != nil {
		// If an error slipped through, it must not be an E05xx lint
		// diagnostic. Non-lint engine errors would indicate an unrelated
		// regression and should still fail this test loudly.
		for _, de := range collectDiags(err) {
			if strings.HasPrefix(de.D.Code, "E05") {
				t.Fatalf("clean rule produced unexpected lint diagnostic %s: %v",
					de.D.Code, err)
			}
		}
		t.Fatalf("clean rule should load without error; got: %v", err)
	}
}

// TestLoadRules_LintDiag_PermittedUniverseBuiltins_NoE05xx pins that the
// curated universe builtins (and, or, matchN, matchIf) may be used bare in
// `when` without tripping E0501. Each fixture exercises the builtin's real
// validator arity in the srnnkls/cue fork.
func TestLoadRules_LintDiag_PermittedUniverseBuiltins_NoE05xx(t *testing.T) {
	cases := []struct {
		name string
		when string
	}{
		{
			name: "or_over_list",
			when: `when: {tool_name: or(["Bash", "Edit"])}`,
		},
		{
			name: "and_over_list",
			when: `when: {tool_name: and([=~"^B", =~"sh$"])}`,
		},
		{
			name: "matchN_constraint_count",
			when: `when: {tool_name: matchN(1, [=~"^B", =~"^E"])}`,
		},
		{
			name: "matchIf_conditional",
			when: `when: {tool_name: matchIf({}, {}, {})}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := "package rules\n\nbuiltin_rule: {\n\t" + tc.when + `
	then: deny: {
		rule_id: "b"
		reason:  "nope"
	}
}
`
			path := lintFixture(t, "builtin_"+tc.name, src)
			dir := filepath.Dir(path)

			_, err := config.LoadRules(dir)
			if err != nil {
				for _, de := range collectDiags(err) {
					if strings.HasPrefix(de.D.Code, "E05") {
						t.Fatalf("permitted builtin %q produced lint diagnostic %s: %v",
							tc.name, de.D.Code, err)
					}
				}
				t.Fatalf("permitted builtin %q should load without error; got: %v", tc.name, err)
			}
		})
	}
}

// TestLoadRules_LintDiag_CloseInWhen_EmitsE0501 pins that `close` is excluded
// from the permitted set: using it bare in `when` still surfaces as E0501. It
// flips a struct pattern from open to closed, silently breaking matches on
// extensible hook payloads, so it must remain rejected.
func TestLoadRules_LintDiag_CloseInWhen_EmitsE0501(t *testing.T) {
	const src = `package rules

close_rule: {
	when: close({tool_name: "Bash"})
	then: deny: {
		rule_id: "c"
		reason:  "nope"
	}
}
`
	path := lintFixture(t, "closewhen", src)
	dir := filepath.Dir(path)

	_, err := config.LoadRules(dir)
	if err == nil {
		t.Fatal("expected close in when to be rejected, got nil error")
	}

	de, ok := recoverDiag(t, err)
	if !ok {
		t.Fatalf("expected err to carry *diag.DiagError via errors.As; got: %v", err)
	}
	if de.D.Code != "E0501" {
		t.Errorf("diagnostic Code = %q, want %q", de.D.Code, "E0501")
	}
}

func TestLoadRules_LintDiag_ExcludedUniverseBuiltins_EmitsE0501(t *testing.T) {
	cases := []struct {
		name string
		when string
	}{
		{
			name: "div_arithmetic",
			when: `when: {tool_name: "Bash", _n: div(6, 2)}`,
		},
		{
			name: "mod_arithmetic",
			when: `when: {tool_name: "Bash", _n: mod(6, 4)}`,
		},
		{
			name: "quo_arithmetic",
			when: `when: {tool_name: "Bash", _n: quo(6, 2)}`,
		},
		{
			name: "rem_arithmetic",
			when: `when: {tool_name: "Bash", _n: rem(6, 4)}`,
		},
		{
			name: "error_bare_ident",
			when: `when: {tool_name: "Bash", _e: error("boom")}`,
		},
		{
			name: "self_bare_ident",
			when: `when: {tool_name: "Bash", _s: self}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := "package rules\n\nexcluded_rule: {\n\t" + tc.when + `
	then: deny: {
		rule_id: "x"
		reason:  "nope"
	}
}
`
			path := lintFixture(t, "excluded_"+tc.name, src)
			dir := filepath.Dir(path)

			_, err := config.LoadRules(dir)
			if err == nil {
				t.Fatalf("excluded builtin %q should be rejected in when, got nil error", tc.name)
			}

			de, ok := recoverDiag(t, err)
			if !ok {
				t.Fatalf("excluded builtin %q: expected err to carry *diag.DiagError; got: %v", tc.name, err)
			}
			if de.D.Code != "E0501" {
				t.Errorf("excluded builtin %q: diagnostic Code = %q, want %q", tc.name, de.D.Code, "E0501")
			}
		})
	}
}

// TestLoadRules_LintDiag_MultipleViolations_JoinedAndRecoverable pins that a
// file with several independent lint failures surfaces all of them through
// errors.Join; errors.As recovers each as its own *diag.DiagError.
//
// This rides on the T4 plumbing: the loader aggregates per-rule failures into
// a joined error so callers see the full picture in one pass.
func TestLoadRules_LintDiag_MultipleViolations_JoinedAndRecoverable(t *testing.T) {
	const src = `package rules

donor_rule: {
	when: {tool_name: "Bash"}
	then: deny: {
		rule_id: "donor"
		reason:  "nope"
	}
}

consumer_rule: {
	when: {tool_name: donor_rule.when.tool_name}
	then: deny: {
		rule_id: "consumer"
		reason:  "nope"
	}
}

typo_rule: {
	when: {tool_name: not_a_real_binding}
	then: deny: {
		rule_id: "typo"
		reason:  "nope"
	}
}
`
	path := lintFixture(t, "multi_bad", src)
	dir := filepath.Dir(path)

	_, err := config.LoadRules(dir)
	if err == nil {
		t.Fatal("expected multi-violation file to be rejected, got nil error")
	}

	diags := collectDiags(err)
	if len(diags) < 2 {
		t.Fatalf("expected at least 2 diagnostics from errors.Join recovery, got %d (err=%v)",
			len(diags), err)
	}

	codes := make(map[string]bool, len(diags))
	for _, de := range diags {
		codes[de.D.Code] = true
	}
	if !codes["E0502"] {
		t.Errorf("expected E0502 among recovered diagnostics; got codes %v", codes)
	}
	if !codes["E0501"] {
		t.Errorf("expected E0501 among recovered diagnostics; got codes %v", codes)
	}
}

// TestLoadRules_LintDiag_LenInWhen_EmitsE0508 pins that a `len()` call inside
// `when` surfaces as E0508 with the primary span anchored at `len`.
func TestLoadRules_LintDiag_LenInWhen_EmitsE0508(t *testing.T) {
	const src = `package rules

len_rule: {
	when: {
		tool_input: parsed: {
			flags: [...]
			_n: len(flags)
			_n: >=2
		}
	}
	then: deny: {
		rule_id: "n"
		reason:  "nope"
	}
}
`
	path := lintFixture(t, "len_when", src)
	dir := filepath.Dir(path)

	_, err := config.LoadRules(dir)
	if err == nil {
		t.Fatal("expected len in when to be rejected, got nil error")
	}

	de, ok := recoverDiag(t, err)
	if !ok {
		t.Fatalf("expected err to carry *diag.DiagError via errors.As; got: %v", err)
	}
	if de.D.Code != "E0508" {
		t.Errorf("diagnostic Code = %q, want %q", de.D.Code, "E0508")
	}
	if got := tokenAtPos(t, de.D.Primary.Pos, 3); got != "len" {
		t.Errorf("primary span should anchor at `len` keyword; got %q", got)
	}
}

// TestLoadRules_LintDiag_LetInWhen_EmitsE0506 pins that a `let` clause inside
// `when` surfaces as E0506 with the primary span anchored at the `let` keyword.
func TestLoadRules_LintDiag_LetInWhen_EmitsE0506(t *testing.T) {
	const src = `package rules

let_rule: {
	when: {
		let cmd = tool_input.command
		tool_input: command: string
		_check: cmd & =~"^git"
	}
	then: deny: {
		rule_id: "l"
		reason:  "nope"
	}
}
`
	path := lintFixture(t, "let_when", src)
	dir := filepath.Dir(path)

	_, err := config.LoadRules(dir)
	if err == nil {
		t.Fatal("expected let in when to be rejected, got nil error")
	}

	de, ok := recoverDiag(t, err)
	if !ok {
		t.Fatalf("expected err to carry *diag.DiagError via errors.As; got: %v", err)
	}
	if de.D.Code != "E0506" {
		t.Errorf("diagnostic Code = %q, want %q", de.D.Code, "E0506")
	}
	if got := tokenAtPos(t, de.D.Primary.Pos, 3); got != "let" {
		t.Errorf("primary span should anchor at `let` keyword; got %q", got)
	}
	if de.D.Help == "" {
		t.Errorf("E0506 diagnostic should carry a Help string; got empty")
	}
}

// TestLoadRules_LintDiag_IfInWhen_EmitsE0507 pins that an `if` comprehension
// inside `when` surfaces as E0507 with the primary span anchored at the `if`
// keyword.
func TestLoadRules_LintDiag_IfInWhen_EmitsE0507(t *testing.T) {
	const src = `package rules

import "list"

if_rule: {
	when: {
		tool_input: parsed: {
			flags: [...string]
			if list.Contains(flags, "--force") {
				commands: [...=~"^git$"]
			}
		}
	}
	then: deny: {
		rule_id: "i"
		reason:  "nope"
	}
}
`
	path := lintFixture(t, "if_when", src)
	dir := filepath.Dir(path)

	_, err := config.LoadRules(dir)
	if err == nil {
		t.Fatal("expected if comprehension in when to be rejected, got nil error")
	}

	de, ok := recoverDiag(t, err)
	if !ok {
		t.Fatalf("expected err to carry *diag.DiagError via errors.As; got: %v", err)
	}
	if de.D.Code != "E0507" {
		t.Errorf("diagnostic Code = %q, want %q", de.D.Code, "E0507")
	}
	if got := tokenAtPos(t, de.D.Primary.Pos, 2); got != "if" {
		t.Errorf("primary span should anchor at `if` keyword; got %q", got)
	}
	if !strings.Contains(de.D.Title, "`if` guard") {
		t.Errorf("E0507 title should mention `if` guard; got %q", de.D.Title)
	}
}

// TestLoadRules_LintDiag_ForInWhen_EmitsE0507 pins that a `for` comprehension
// inside `when` surfaces as E0507.
func TestLoadRules_LintDiag_ForInWhen_EmitsE0507(t *testing.T) {
	const src = `package rules

for_rule: {
	when: {
		tool_input: parsed: {
			flags: [...string]
			for f in flags {
				(f): true
			}
		}
	}
	then: deny: {
		rule_id: "f"
		reason:  "nope"
	}
}
`
	path := lintFixture(t, "for_when", src)
	dir := filepath.Dir(path)

	_, err := config.LoadRules(dir)
	if err == nil {
		t.Fatal("expected for comprehension in when to be rejected, got nil error")
	}

	de, ok := recoverDiag(t, err)
	if !ok {
		t.Fatalf("expected err to carry *diag.DiagError via errors.As; got: %v", err)
	}
	if de.D.Code != "E0507" {
		t.Errorf("diagnostic Code = %q, want %q", de.D.Code, "E0507")
	}
	if !strings.Contains(de.D.Title, "`for` loop") {
		t.Errorf("E0507 title should mention `for` loop; got %q", de.D.Title)
	}
}
