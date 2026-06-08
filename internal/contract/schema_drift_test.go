package contract

import (
	"reflect"
	"sort"
	"strings"
	"testing"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"

	fascue "github.com/srnnkls/fas/cue"
	"github.com/srnnkls/fas/internal/envelope"
	"github.com/srnnkls/fas/internal/parser"
)

func jsonTags(t *testing.T, typ reflect.Type) map[string]struct{} {
	t.Helper()
	tags := map[string]struct{}{}
	for f := range typ.Fields() {
		if !f.IsExported() {
			continue
		}
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name == "" || name == "-" {
			continue
		}
		tags[name] = struct{}{}
	}
	return tags
}

func defFields(t *testing.T, def string) map[string]struct{} {
	t.Helper()
	ctx := cuecontext.New()
	root := ctx.CompileBytes(fascue.SchemaSource())
	if err := root.Err(); err != nil {
		t.Fatalf("compile schema: %v", err)
	}
	v := root.LookupPath(cue.ParsePath(def))
	if err := v.Err(); err != nil {
		t.Fatalf("lookup %s: %v", def, err)
	}
	fields := map[string]struct{}{}
	it, err := v.Fields(cue.Optional(true), cue.Definitions(true))
	if err != nil {
		t.Fatalf("fields %s: %v", def, err)
	}
	for it.Next() {
		fields[it.Selector().Unquoted()] = struct{}{}
	}
	return fields
}

func sortedKeys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func TestParsedContractMatchesSchema(t *testing.T) {
	goTags := jsonTags(t, reflect.TypeFor[parser.Parsed]())
	cueFields := defFields(t, "#Parsed")

	for name := range goTags {
		if _, ok := cueFields[name]; !ok {
			t.Errorf("parser.Parsed json tag %q has no matching #Parsed field (schema fields: %v)", name, sortedKeys(cueFields))
		}
	}
	for name := range cueFields {
		if _, ok := goTags[name]; !ok {
			t.Errorf("#Parsed field %q has no matching parser.Parsed json tag (go tags: %v)", name, sortedKeys(goTags))
		}
	}
}

// Pins R5: #Parsed.commands/subcommands must be open-typed (`_`), not
// `[...string]`. A list type eagerly defaults to `[]`, so composing #Parsed
// with a downstream `list.MatchN(>0, ...)` constraint — without supplying a
// value, exactly what happens at rule-load — fails against that empty default.
// Open typing lets the constraint compose. This test fails now (fields absent)
// and would fail again if a future edit retyped them as `[...string]`.
func TestParsedSchema_NewFieldsAreOpenTyped(t *testing.T) {
	fields := defFields(t, "#Parsed")
	for _, name := range []string{"commands", "subcommands"} {
		if _, ok := fields[name]; !ok {
			t.Errorf("#Parsed is missing field %q (schema fields: %v)", name, sortedKeys(fields))
		}
	}

	ctx := cuecontext.New()
	root := ctx.CompileBytes(fascue.SchemaSource())
	if err := root.Err(); err != nil {
		t.Fatalf("compile schema: %v", err)
	}
	parsed := root.LookupPath(cue.ParsePath("#Parsed"))
	if err := parsed.Err(); err != nil {
		t.Fatalf("lookup #Parsed: %v", err)
	}

	for _, field := range []string{"commands", "subcommands"} {
		guard := ctx.CompileString(`import "list"
{#name: "rm", ` + field + `: list.MatchN(>0, #name), ...}`)
		if err := guard.Err(); err != nil {
			t.Fatalf("compile %s guard: %v", field, err)
		}

		composed := parsed.Unify(guard)
		if err := composed.Err(); err != nil {
			t.Errorf("#Parsed & list.MatchN(>0,...) guard on %s failed at compose: %v\n"+
				"this is the R5 eager-empty-default failure — %s must be typed `_`, not `[...string]`",
				field, err, field)
		}
		if err := composed.Validate(); err != nil {
			t.Errorf("#Parsed & list.MatchN(>0,...) guard on %s failed validation: %v\n"+
				"this is the R5 eager-empty-default failure — %s must be typed `_`, not `[...string]`",
				field, err, field)
		}
	}
}

// One-way: #Input is open (`...`) and event-specific fields (e.g. prompt) live
// in hook sub-package shapes, so envelope.Input legitimately carries tags #Input
// does not enumerate. Only the schema->Go direction is a contract violation.
func TestInputSchemaFieldsAreEmitted(t *testing.T) {
	goTags := jsonTags(t, reflect.TypeFor[envelope.Input]())
	cueFields := defFields(t, "#Input")

	for name := range cueFields {
		if _, ok := goTags[name]; !ok {
			t.Errorf("#Input field %q has no matching envelope.Input json tag (go tags: %v)", name, sortedKeys(goTags))
		}
	}
}
