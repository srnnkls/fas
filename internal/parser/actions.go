package parser

// bashVerbs maps Bash command names to the semantic verbs the rule stdlib
// expects in Parsed.Actions. Command names themselves never appear as verbs;
// commands not listed here produce no action.
var bashVerbs = map[string]string{
	"rm":     "remove",
	"rmdir":  "remove",
	"ls":     "list",
	"echo":   "print",
	"printf": "print",
	"cat":    "read",
	"mv":     "move",
	"cp":     "copy",
	"touch":  "create",
	"mkdir":  "create",
}

// bashSubcommandVerbs resolves verbs that depend on a subcommand, such as
// "git branch" or "apt install". Looked up with "<cmd> <sub>".
var bashSubcommandVerbs = map[string]string{
	"git branch":   "branch",
	"git commit":   "commit",
	"git push":     "push",
	"git pull":     "pull",
	"git merge":    "merge",
	"git rebase":   "rebase",
	"git checkout": "checkout",
	"apt install":  "install",
	"apt remove":   "remove",
	"apt purge":    "remove",
}

// bashKnownSubcommands registers subcommands that must be detected without
// resolveVerb fabricating an action (e.g. "git add" has no verb). Keyed by
// "<cmd> <sub>".
var bashKnownSubcommands = map[string]struct{}{
	"git add": {},
}

// escalationPrefixes are commands that elevate privilege and wrap the real
// command. They appear in Attributes.prefix_commands, not in Actions.
var escalationPrefixes = map[string]struct{}{
	"sudo": {},
	"doas": {},
	"su":   {},
}

// destructiveFlagVerbs lets subcommand verbs be overridden by destructive
// flags, e.g. `git branch -D` means delete, not branch.
var destructiveFlagVerbs = map[string]map[string]string{
	"git branch": {
		"-D": "delete",
		"-d": "delete",
	},
}
