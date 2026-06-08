package parser

import (
	"bytes"
	"fmt"
	"slices"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// ParseBash parses a Bash command string into a canonical Parsed struct.
//
// Malformed input is reported via Attributes.parse_error rather than a
// returned error, so callers can still consume the partial structural facts
// extracted from tokens that did parse.
func ParseBash(command string) Parsed {
	trimmed := strings.TrimSpace(command)
	if trimmed == "" {
		return Parsed{}
	}

	parser := syntax.NewParser()
	file, err := parser.Parse(strings.NewReader(command), "")
	if err != nil {
		return Parsed{
			Attributes: map[string]any{"parse_error": err.Error()},
		}
	}

	p := Parsed{Attributes: map[string]any{}}
	syntax.Walk(file, func(node syntax.Node) bool {
		if node == nil {
			return false
		}
		switch n := node.(type) {
		case *syntax.Stmt:
			for _, r := range n.Redirs {
				if text := redirectText(r); text != "" {
					appendAttrString(&p, "redirections", text)
				}
			}
		case *syntax.BinaryCmd:
			if n.Op == syntax.Pipe || n.Op == syntax.PipeAll {
				p.Attributes["pipeline"] = true
			}
		case *syntax.Subshell:
			p.Attributes["subshell"] = true
		case *syntax.CallExpr:
			extractCall(n, &p)
		}
		return true
	})
	return p
}

func extractCall(call *syntax.CallExpr, out *Parsed) {
	type token struct {
		text     string
		hasSubst bool
	}
	tokens := make([]token, 0, len(call.Args))
	for _, w := range call.Args {
		tokens = append(tokens, token{text: wordString(w), hasSubst: wordHasSubst(w)})
	}
	if len(tokens) == 0 {
		return
	}

	// Mark subshell when any word contains a command substitution.
	for _, t := range tokens {
		if t.hasSubst {
			out.Attributes["subshell"] = true
			break
		}
	}

	// Strip leading escalation prefixes (sudo/doas/su) without consuming
	// substituted words, which can never be literal command names.
	for len(tokens) > 0 && !tokens[0].hasSubst {
		if _, ok := escalationPrefixes[tokens[0].text]; !ok {
			break
		}
		appendAttrString(out, "prefix_commands", tokens[0].text)
		tokens = tokens[1:]
	}
	if len(tokens) == 0 {
		return
	}

	name := tokens[0].text
	rest := tokens[1:]
	out.Commands = append(out.Commands, name)

	// Identify the subcommand as the first non-flag, non-substitution token
	// that the subcommand tables actually recognize for this command. This
	// lets `git -C /foo branch -D feature` resolve `branch` even when global
	// flags precede it, while leaving non-subcommand commands like `rm /etc`
	// alone.
	subIdx := -1
	endOfFlags := false
	for i, t := range rest {
		if t.hasSubst {
			continue
		}
		if !endOfFlags && t.text == "--" {
			endOfFlags = true
			continue
		}
		if !endOfFlags && isFlag(t.text) {
			continue
		}
		if isKnownSubcommand(name, t.text) {
			subIdx = i
			break
		}
	}

	flags := []string{}
	positional := []string{}
	endOfFlags = false
	for i, t := range rest {
		// Words containing command substitutions (e.g. $(rm foo)) leak
		// implementation detail if appended verbatim — subshell attribute is
		// the authoritative signal.
		if t.hasSubst {
			continue
		}
		if !endOfFlags && t.text == "--" {
			endOfFlags = true
			continue
		}
		if !endOfFlags && isFlag(t.text) {
			flags = append(flags, debundleFlag(t.text)...)
			continue
		}
		// Deny-safe over-match: expose every registered-subcommand token so a
		// leaked flag value ("git -C commit") can't shadow the real subcommand.
		if isKnownSubcommand(name, t.text) {
			out.Subcommands = append(out.Subcommands, t.text)
		}
		// Drop the resolved subcommand from positional Targets so only real
		// refs remain.
		if i == subIdx {
			continue
		}
		positional = append(positional, t.text)
	}

	out.Flags = append(out.Flags, flags...)
	out.Targets = append(out.Targets, positional...)

	subcommand := ""
	if subIdx >= 0 {
		subcommand = rest[subIdx].text
	}
	if verb := resolveVerb(name, subcommand, flags); verb != "" {
		out.Actions = append(out.Actions, verb)
	}
}

// isKnownSubcommand reports whether `<name> <candidate>` is registered in any
// subcommand verb table. Used to disambiguate global-flag arguments (e.g.
// `git -C /foo`) from real subcommands (e.g. `git branch`).
func isKnownSubcommand(name, candidate string) bool {
	key := name + " " + candidate
	if _, ok := bashSubcommandVerbs[key]; ok {
		return true
	}
	if _, ok := destructiveFlagVerbs[key]; ok {
		return true
	}
	if _, ok := bashKnownSubcommands[key]; ok {
		return true
	}
	return false
}

func isFlag(token string) bool {
	return strings.HasPrefix(token, "-") && token != "-"
}

// debundleFlag splits short bundles (-rf -> -r -f) and long opts with values
// (--opt=v -> --opt). It is arity-blind, so attached short values (-mfoo) and
// single-dash long options (-name) over-split per char (documented R6 limit).
func debundleFlag(token string) []string {
	if strings.HasPrefix(token, "--") {
		if eq := strings.IndexByte(token, '='); eq > 2 {
			return []string{token[:eq]}
		}
		return []string{token}
	}
	chars := token[1:]
	if len(chars) < 2 {
		return []string{token}
	}
	out := make([]string, 0, len(chars))
	for _, c := range chars {
		out = append(out, "-"+string(c))
	}
	return out
}

func resolveVerb(name, subcommand string, flags []string) string {
	if subcommand != "" {
		key := name + " " + subcommand
		if override, ok := destructiveFlagVerbs[key]; ok {
			for _, f := range flags {
				if v, hit := override[f]; hit {
					return v
				}
			}
		}
		if v, ok := bashSubcommandVerbs[key]; ok {
			return v
		}
	}
	return bashVerbs[name]
}

func wordString(w *syntax.Word) string {
	if w == nil {
		return ""
	}
	if lit := w.Lit(); lit != "" {
		return lit
	}
	var buf bytes.Buffer
	if err := syntax.NewPrinter().Print(&buf, w); err != nil {
		return ""
	}
	return strings.TrimSpace(buf.String())
}

func wordHasSubst(w *syntax.Word) bool {
	if w == nil {
		return false
	}
	for _, part := range w.Parts {
		switch p := part.(type) {
		case *syntax.CmdSubst:
			return true
		case *syntax.DblQuoted:
			for _, inner := range p.Parts {
				if _, ok := inner.(*syntax.CmdSubst); ok {
					return true
				}
			}
		}
	}
	return false
}

func redirectText(r *syntax.Redirect) string {
	if r == nil {
		return ""
	}
	var n string
	if r.N != nil {
		n = r.N.Value
	}
	word := wordString(r.Word)
	return fmt.Sprintf("%s%s%s", n, r.Op.String(), word)
}

func appendAttrString(out *Parsed, key, value string) {
	existing, _ := out.Attributes[key].([]string)
	if slices.Contains(existing, value) {
		return
	}
	out.Attributes[key] = append(existing, value)
}
