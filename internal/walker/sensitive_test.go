package walker

import (
	"path/filepath"
	"slices"
	"sort"
	"testing"
)

// TestIsSensitive mirrors the test cases from
// packages/shared/src/__tests__/sensitive-filter.test.ts (legacy).
func TestIsSensitive(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		// Path patterns
		{".env", true},
		{".env.local", true},
		{".env.production", true},
		{"app/.env", true},
		{"server.key", true},
		{"cert.pem", true},
		{"keystore.jks", true},
		{".ssh/id_rsa", true},
		{"home/.ssh/known_hosts", true},
		{".gnupg/private-keys.kbx", true},
		{".aws/credentials", true},
		{"id_rsa", true},
		{"id_ed25519.pub", true},
		{".npmrc", true},
		{".pypirc", true},
		{"local.sqlite", true},
		{"prod.db", true},

		// Name patterns (delimited keywords)
		{"credentials.json", true},
		{"my_credentials.txt", true},
		{"aws-secret.txt", true},
		{"api_token.json", true},
		{"refresh_tokens.txt", true},

		// False positives the original guarded against
		{"tokenizer.ts", false},
		{"secretary.ts", false},
		{"credentialing-service.ts", false},
		{"src/index.ts", false},
		{"README.md", false},
		{"package.json", false},

		// Windows path normalization
		{`src\config\.env`, true},
		{`src\index.ts`, false},
	}

	for _, c := range cases {
		got := IsSensitive(c.path)
		if got != c.want {
			t.Errorf("IsSensitive(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}

func TestIsDefaultIgnoredPath(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		// test/fixtures directory at various depths should be excluded
		{"test/fixtures", true},
		{"apps/api/test/fixtures", true},
		{"packages/core/test/fixtures", true},

		// test/ alone should NOT be excluded (only the fixtures subdirectory)
		{"test", false},
		{"apps/api/test", false},

		// Other directories should NOT be excluded
		{"apps/api", false},
		{"src/routes", false},
		{"node_modules", false}, // handled by DefaultIgnoreDirs, not path suffixes
		{"dist", false},         // handled by DefaultIgnoreDirs, not path suffixes

		// Windows path normalization (backslash → forward slash)
		{`apps\api\test\fixtures`, true},
		{`test\fixtures`, true},
		{`apps\api\test`, false},
	}

	for _, c := range cases {
		got := IsDefaultIgnoredPath(c.path)
		if got != c.want {
			t.Errorf("IsDefaultIgnoredPath(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}

func TestRerootGitignore(t *testing.T) {
	cases := []struct {
		text   string
		relDir string
		want   string
	}{
		{"node_modules\ndist\n", "apps/web", "apps/web/node_modules\napps/web/dist\n"},
		{"!important.txt\n", "src", "!src/important.txt\n"},
		{"# comment\n\nbuild/\n", "lib", "# comment\n\nlib/build/\n"},
		{"/absolute\n", "pkg", "pkg/absolute\n"},
		{"empty\n", "", "empty\n"}, // empty relDir → no-op
	}
	for _, c := range cases {
		got := RerootGitignore(c.text, c.relDir)
		if got != c.want {
			t.Errorf("RerootGitignore(%q,%q):\n got: %q\nwant: %q", c.text, c.relDir, got, c.want)
		}
	}
}

// TestWalkRepo_DefaultIgnoresClaudeMd verifies that CLAUDE.md files at the
// repo root and in nested directories are excluded from the docs walker by
// default (DOG-CLI-1 / dogfood F-15). Basename match — any directory's
// CLAUDE.md is ignored, not just the root.
func TestWalkRepo_DefaultIgnoresClaudeMd(t *testing.T) {
	root := t.TempDir()

	// CLAUDE.md at root level.
	mustWrite(t, filepath.Join(root, "CLAUDE.md"), "# root claude\n")
	// CLAUDE.md nested inside a subdirectory.
	mustWrite(t, filepath.Join(root, "docs", "CLAUDE.md"), "# nested claude\n")
	// A regular doc that should still be included.
	mustWrite(t, filepath.Join(root, "README.md"), "# readme\n")

	docs, err := WalkDocs(root)
	if err != nil {
		t.Fatal(err)
	}

	paths := make([]string, 0, len(docs))
	for _, d := range docs {
		paths = append(paths, d.RelativePath)
	}
	sort.Strings(paths)

	for _, p := range paths {
		if p == "CLAUDE.md" || p == "docs/CLAUDE.md" {
			t.Errorf("WalkDocs included %q — expected it to be default-ignored (DOG-CLI-1)", p)
		}
	}

	if !slices.Contains(paths, "README.md") {
		t.Errorf("WalkDocs did not include README.md — non-CLAUDE docs must still be walked")
	}
}

// TestWalkRepo_BrowzerignoreNegationRestoresClaudeMd verifies the escape hatch:
// a .browzerignore containing "!CLAUDE.md" re-includes CLAUDE.md so users who
// DO want to index their agent-context file can opt back in.
func TestWalkRepo_BrowzerignoreNegationRestoresClaudeMd(t *testing.T) {
	root := t.TempDir()

	// Write .browzerignore that negates the default CLAUDE.md exclusion.
	mustWrite(t, filepath.Join(root, ".browzerignore"), "!CLAUDE.md\n")
	mustWrite(t, filepath.Join(root, "CLAUDE.md"), "# root claude re-included\n")
	mustWrite(t, filepath.Join(root, "README.md"), "# readme\n")

	docs, err := WalkDocs(root)
	if err != nil {
		t.Fatal(err)
	}

	paths := make([]string, 0, len(docs))
	for _, d := range docs {
		paths = append(paths, d.RelativePath)
	}

	if !slices.Contains(paths, "CLAUDE.md") {
		t.Errorf("WalkDocs did not include CLAUDE.md after .browzerignore negation — escape hatch broken")
	}
}
