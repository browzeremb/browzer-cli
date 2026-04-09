package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// browzerSection is the block injected into CLAUDE.md by `browzer init`.
// The sentinel comment on the first line is what InjectBrowzerSection uses
// to detect an existing injection — do NOT change it without also updating
// the idempotency check below.
const browzerSection = `
## This repository is powered by Browzer with a Semantic Knowledge Base (hybrid vector + Graph RAG)

**SINGLE SOURCE OF TRUTH**: The Browzer workspace index stores all code, patterns, and documentation for this repo.

**Two search surfaces**:

- ` + "`browzer explore`" + ` — hybrid graph + vector search over **indexed code** (files, symbols, snippets)
- ` + "`browzer search`" + ` — pure vector search over **indexed markdown documents** (architecture docs, ADRs, runbooks)

### How to Update the Index

| What changed                     | Command to re-index       |
| -------------------------------- | ------------------------- |
| Source code (` + "`.ts`, `.go`" + `, etc.) | ` + "`browzer workspace index`" + ` |
| Markdown docs / ADRs             | ` + "`browzer workspace docs`" + `  |

### When to Query KB vs Read Files

| Question Type                             | FIRST Action                                                | SECOND Action (if needed)                              |
| ----------------------------------------- | ----------------------------------------------------------- | ------------------------------------------------------ |
| "What does this code do / where is Foo?"  | ` + "`browzer explore \"<query>\" --json --save /tmp/explore.json`" + ` | ` + "`Read`" + ` specific file + line range from explore results |
| "How does X library/framework work here?" | ` + "`browzer search \"<topic>\" --json --save /tmp/search.json`" + `   | Maybe: Read file only if search points to it           |
| "Explain architecture / design decisions" | ` + "`browzer search \"<topic>\" --json --save /tmp/search.json`" + `   | Never needed — KB has it                               |
| "Where is function Foo?"                  | ` + "`browzer explore \"Foo\" --json --save /tmp/explore.json`" + `     | ` + "`Read`" + ` exact line range returned                       |
| "Fix error: TypeError..."                 | ` + "`browzer search \"<error>\" --json --save /tmp/search.json`" + `   | Maybe: Read file if new error                          |

**Rule of thumb**: codebase questions → ` + "`explore`" + ` FIRST. Documentation / library / pattern questions → ` + "`search`" + ` FIRST. Glob/Grep on source files only when browzer returns no useful results.

### Useful flags (both commands)

` + "```" + `bash
browzer explore "<query>" --json --save /tmp/explore.json   # hybrid graph+vector, code index
browzer search  "<query>" --json --save /tmp/search.json    # vector, markdown docs index
browzer explore --schema                                     # discover response JSON shape
browzer workspace status                                     # check index staleness
browzer workspace index                                      # re-index source code
browzer workspace docs                                       # re-index markdown documents
` + "```" + `
`

// browzerSectionSentinel is the unique string used to detect whether the
// Browzer section has already been injected. Checked via strings.Contains so
// the check is robust to trailing whitespace differences.
const browzerSectionSentinel = "## This repository is powered by Browzer with a Semantic Knowledge Base"

// InjectBrowzerSection appends the Browzer KB section to <gitRoot>/CLAUDE.md,
// creating the file if it does not exist. The operation is idempotent: if the
// sentinel heading is already present the file is left unchanged.
func InjectBrowzerSection(gitRoot string) error {
	path := filepath.Join(gitRoot, "CLAUDE.md")

	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}

	// Idempotency check — skip if already injected.
	if strings.Contains(string(data), browzerSectionSentinel) {
		return nil
	}

	// Ensure the file ends with a newline before appending.
	prefix := ""
	if len(data) > 0 && data[len(data)-1] != '\n' {
		prefix = "\n"
	}

	out := append(data, []byte(prefix+browzerSection)...)
	return os.WriteFile(path, out, 0o644)
}
