package diag

import (
	"bytes"
	"encoding/json"

	"cuelang.org/go/cue/token"
)

// sarifSchemaURI is the canonical SARIF 2.1.0 JSON schema URL. SARIF
// consumers key schema selection off this value; keep it pinned.
const sarifSchemaURI = "https://json.schemastore.org/sarif-2.1.0.json"

// sarifVersion is the spec version string embedded in every emitted document.
const sarifVersion = "2.1.0"

// sarifToolVersion advertises the emitter version. The scope (F10) accepts a
// placeholder when the binary does not yet surface a build version.
const sarifToolVersion = "0.0.0"

// Hand-rolled SARIF 2.1.0 struct types. Field order in these declarations
// determines JSON key order, which fixes determinism (NF2) without needing a
// sort pass over a map.

type sarifDoc struct {
	Schema  string     `json:"$schema"`
	Version string     `json:"version"`
	Runs    []sarifRun `json:"runs"`
}

type sarifRun struct {
	Tool    sarifTool     `json:"tool"`
	Results []sarifResult `json:"results"`
}

type sarifTool struct {
	Driver sarifDriver `json:"driver"`
}

type sarifDriver struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type sarifResult struct {
	RuleID           string          `json:"ruleId"`
	Level            string          `json:"level"`
	Message          sarifMessage    `json:"message"`
	Locations        []sarifLocation `json:"locations,omitempty"`
	RelatedLocations []sarifLocation `json:"relatedLocations,omitempty"`
}

type sarifMessage struct {
	Text string `json:"text"`
}

type sarifLocation struct {
	PhysicalLocation *sarifPhysicalLocation `json:"physicalLocation,omitempty"`
	Message          *sarifMessage          `json:"message,omitempty"`
	Properties       map[string]any         `json:"properties,omitempty"`
}

type sarifPhysicalLocation struct {
	ArtifactLocation sarifArtifactLocation `json:"artifactLocation"`
	Region           sarifRegion           `json:"region"`
}

type sarifArtifactLocation struct {
	URI string `json:"uri"`
}

type sarifRegion struct {
	StartLine   int `json:"startLine"`
	StartColumn int `json:"startColumn"`
	CharLength  int `json:"charLength"`
}

// RenderSARIF emits a single SARIF 2.1.0 document containing every Diagnostic
// in diags as one entry under runs[0].results. Invalid positions omit their
// physicalLocation cleanly (NF3) and the output is byte-deterministic (NF2).
func RenderSARIF(diags []Diagnostic) []byte {
	results := make([]sarifResult, 0, len(diags))
	for i := range diags {
		results = append(results, buildSARIFResult(diags[i]))
	}

	doc := sarifDoc{
		Schema:  sarifSchemaURI,
		Version: sarifVersion,
		Runs: []sarifRun{
			{
				Tool: sarifTool{
					Driver: sarifDriver{
						Name:    "quae",
						Version: sarifToolVersion,
					},
				},
				Results: results,
			},
		},
	}

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(doc); err != nil {
		// encoding/json on plain structs with string/int/slice/map[string]any
		// leaves cannot legitimately fail. A failure here is a programmer
		// bug; surface an empty byte slice rather than panic (NF3).
		return nil
	}
	return buf.Bytes()
}

func buildSARIFResult(d Diagnostic) sarifResult {
	r := sarifResult{
		RuleID:  d.Code,
		Level:   sarifLevel(d.Severity),
		Message: sarifMessage{Text: sarifMessageText(d)},
	}

	if loc, ok := locationFromPos(d.Primary.Pos, d.Primary.Len); ok {
		r.Locations = []sarifLocation{loc}
	}

	var related []sarifLocation
	for _, n := range d.Notes {
		noteLoc, ok := locationFromPos(n.Pos, n.Len)
		if !ok {
			// Preserve the note's message even when its position is
			// unknown so consumers still see it.
			if n.Msg != "" {
				related = append(related, sarifLocation{
					Message: &sarifMessage{Text: n.Msg},
				})
			}
			continue
		}
		if n.Msg != "" {
			noteLoc.Message = &sarifMessage{Text: n.Msg}
		}
		related = append(related, noteLoc)
	}

	// Provenance entries live on the primary Label's Reasons tree. Walk the
	// tree so nested ConjunctFailed → Provenance also surfaces.
	for _, reason := range d.Primary.Reasons {
		related = append(related, provenanceLocations(reason)...)
	}
	for _, n := range d.Notes {
		for _, reason := range n.Reasons {
			related = append(related, provenanceLocations(reason)...)
		}
	}

	if len(related) > 0 {
		r.RelatedLocations = related
	}
	return r
}

func sarifLevel(s Severity) string {
	switch s {
	case SeverityWarning:
		return "warning"
	case SeverityNote:
		return "note"
	default:
		return "error"
	}
}

func sarifMessageText(d Diagnostic) string {
	if d.Primary.Msg == "" {
		return d.Title
	}
	return d.Title + ": " + d.Primary.Msg
}

// locationFromPos builds a SARIF location from a token.Pos + length. Returns
// ok=false for invalid positions so callers can omit the entry entirely.
func locationFromPos(p token.Pos, length int) (sarifLocation, bool) {
	if !p.IsValid() {
		return sarifLocation{}, false
	}
	return sarifLocation{
		PhysicalLocation: &sarifPhysicalLocation{
			ArtifactLocation: sarifArtifactLocation{URI: p.Filename()},
			Region: sarifRegion{
				StartLine:   p.Line(),
				StartColumn: p.Column(),
				CharLength:  length,
			},
		},
	}, true
}

// locationFromSpan builds a SARIF location from a Span DTO (used for
// Provenance whose origin cannot be replayed through a token.Pos).
func locationFromSpan(s Span) (sarifLocation, bool) {
	if s.File == "" || s.Line == 0 {
		return sarifLocation{}, false
	}
	return sarifLocation{
		PhysicalLocation: &sarifPhysicalLocation{
			ArtifactLocation: sarifArtifactLocation{URI: s.File},
			Region: sarifRegion{
				StartLine:   s.Line,
				StartColumn: s.Col,
				CharLength:  s.Length,
			},
		},
	}, true
}

// provenanceLocations walks a Reason tree and returns a relatedLocation for
// every Provenance node, tagged properties.role="definition". ConjunctFailed
// and DisjunctionFailed are descended; other Reasons contribute nothing.
func provenanceLocations(r Reason) []sarifLocation {
	if r == nil {
		return nil
	}
	switch v := r.(type) {
	case Provenance:
		loc, ok := locationFromSpan(v.Span)
		if !ok {
			return nil
		}
		loc.Properties = map[string]any{"role": "definition"}
		if v.Snippet != "" {
			loc.Message = &sarifMessage{Text: v.Snippet}
		}
		return []sarifLocation{loc}
	case ConjunctFailed:
		return provenanceLocations(v.Sub)
	case DisjunctionFailed:
		var out []sarifLocation
		for _, a := range v.Arms {
			out = append(out, provenanceLocations(a.Inner)...)
		}
		return out
	}
	return nil
}
