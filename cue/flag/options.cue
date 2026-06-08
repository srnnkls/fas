package flag

// opt is the flat, lowercase spelling library: canonical option name -> the
// concrete token spellings a policy may see. Compose `#hasOption & opt.<name>`.
opt: {
	recursive:      {#spellings: ["-r", "-R", "--recursive"]}
	force:          {#spellings: ["-f", "--force"]}
	interactive:    {#spellings: ["-i", "-I", "--interactive"]}
	verbose:        {#spellings: ["-v", "--verbose"]}
	noVerify:       {#spellings: ["--no-verify"]}        // push: long-form ONLY (push -n is dry-run) — R4
	noVerifyCommit: {#spellings: ["--no-verify", "-n"]} // commit/merge: -n is a no-verify alias — R4
}
