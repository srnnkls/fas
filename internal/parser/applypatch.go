package parser

import "strings"

// ParseApplyPatch extracts file operations without applying the patch. Content
// lines retain their diff prefix, so text resembling a header is not a target.
func ParseApplyPatch(command string) Parsed {
	parsed := Parsed{}
	fail := func(message string) Parsed {
		parsed.Attributes = map[string]any{"parse_error": message}
		return parsed
	}
	lines := strings.Split(strings.TrimSpace(command), "\n")
	if len(lines) < 2 || strings.TrimSuffix(lines[0], "\r") != "*** Begin Patch" || lines[len(lines)-1] != "*** End Patch" {
		return fail("invalid patch envelope")
	}
	seen := map[string]bool{}
	operation := ""
	canMove := false
	for _, raw := range lines[1 : len(lines)-1] {
		line := strings.TrimSuffix(raw, "\r")
		action, path := "", ""
		for _, header := range []struct{ prefix, action string }{
			{"*** Add File: ", "add"}, {"*** Update File: ", "update"},
			{"*** Delete File: ", "delete"}, {"*** Move to: ", "move"},
		} {
			if value, ok := strings.CutPrefix(line, header.prefix); ok {
				action, path = header.action, value
				break
			}
		}
		if action != "" {
			if strings.TrimSpace(path) == "" {
				return fail("empty patch path")
			}
			if action == "move" && !canMove {
				return fail("move must follow update header")
			}
			if !seen[path] {
				parsed.Targets = append(parsed.Targets, path)
				seen[path] = true
			}
			parsed.Actions = append(parsed.Actions, action)
			if action != "move" {
				operation = action
			}
			canMove = action == "update"
			continue
		}
		canMove = false
		switch operation {
		case "add":
			if !strings.HasPrefix(line, "+") {
				return fail("invalid added line")
			}
		case "update":
			if line != "" && !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "-") && line != "@@" && !strings.HasPrefix(line, "@@ ") && line != "*** End of File" {
				return fail("invalid update line")
			}
		default:
			return fail("unexpected patch content")
		}
	}
	return parsed
}
