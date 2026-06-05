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
