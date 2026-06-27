package config

import "testing"

func TestSuggest(t *testing.T) {
	idx := map[string][]string{
		"agent": {"#Explore", "#Plan", "#GeneralPurpose"},
		"hook":  {"#PreToolUse", "#PostToolUse", "#SubagentStart"},
		"tool":  {"#Bash", "#Edit", "#Read"},
		"twin":  {"#abc", "#abd"},
	}

	cases := []struct {
		name    string
		refPkg  string
		missing string
		local   []string
		want    string
	}{
		{
			name:    "qualified same-package typo",
			refPkg:  "agent",
			missing: "#Explor",
			want:    "did you mean `agent.#Explore`?",
		},
		{
			name:    "qualified cross-package fallback",
			refPkg:  "hook",
			missing: "#Bsh",
			want:    "did you mean `tool.#Bash`?",
		},
		{
			name:    "qualified no match",
			refPkg:  "agent",
			missing: "#Zzzzzzz",
			want:    "",
		},
		{
			name:    "bare stdlib member typo qualifies the suggestion",
			refPkg:  "",
			missing: "#PreToolUze",
			want:    "did you mean `hook.#PreToolUse`?",
		},
		{
			name:    "bare local binding typo",
			refPkg:  "",
			missing: "_helpr",
			local:   []string{"_helper", "_other"},
			want:    "did you mean `_helper`?",
		},
		{
			name:    "two candidates use one-of phrasing without oxford comma",
			refPkg:  "twin",
			missing: "#abe",
			want:    "did you mean one of: `twin.#abc`, `twin.#abd`?",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := suggest(c.refPkg, c.missing, c.local, idx)
			if got != c.want {
				t.Errorf("suggest(%q,%q,%v) = %q, want %q", c.refPkg, c.missing, c.local, got, c.want)
			}
		})
	}
}
