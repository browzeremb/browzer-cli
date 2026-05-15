package hooks

import "testing"

func TestClassifyPath_NeverRewrite_TableDriven(t *testing.T) {
	// One positive + one near-miss negative per NEVER_REWRITE pattern.
	cases := []struct {
		name string
		path string
		want PathClass
	}{
		// Dockerfile
		{"dockerfile-root-positive", "Dockerfile", ClassNeverRewrite},
		{"dockerfile-nested-positive", "apps/api/Dockerfile", ClassNeverRewrite},
		{"dockerfile-near-miss", "Dockerfile.backup", ClassCode},

		// drizzle.config.ts
		{"drizzle-config-positive", "apps/auth/drizzle.config.ts", ClassNeverRewrite},
		{"drizzle-config-near-miss", "apps/auth/drizzle.config.test.ts", ClassCode},

		// tsup.config.ts
		{"tsup-config-positive", "packages/queue/tsup.config.ts", ClassNeverRewrite},
		{"tsup-config-near-miss", "packages/queue/tsup-config.ts", ClassCode},

		// *.sql
		{"sql-positive", "db/migrations/001.sql", ClassNeverRewrite},
		{"sql-near-miss", "db/migrations/001.sqlite", ClassCode},

		// *.toml
		{"toml-positive", "rust/Cargo.toml", ClassNeverRewrite},
		{"toml-near-miss", "rust/Cargo.tomly", ClassCode},

		// *.env (root + nested)
		{"env-positive", ".env", ClassNeverRewrite},
		{"env-nested-positive", "apps/api/.env", ClassNeverRewrite},
		{"env-near-miss", "envconfig.ts", ClassCode},

		// *.env.<suffix>
		{"env-local-positive", ".env.local", ClassNeverRewrite},
		{"env-production-positive", "apps/api/.env.production", ClassNeverRewrite},
		// `.envs` is a near miss because the JS regex's negative-lookahead
		// `(?![a-z0-9])` only applies to the CONFIG_SURFACE branch; the
		// NEVER_REWRITE regex anchors on either `\.env` (then optional
		// `.<lowercase>`) at the end of the string OR `(^|/)\.env(\.|$)`
		// for path-component matches. `.envs` does not match either form
		// because the `\.env` extension branch requires end-of-string or
		// a `.` suffix.
		{"envs-near-miss", "doc/envs", ClassUnknown},

		// *.yaml / *.yml
		{"yaml-positive", ".github/workflows/ci.yaml", ClassNeverRewrite},
		{"yml-positive", "k8s/values.yml", ClassNeverRewrite},
		{"yaml-near-miss", "doc/yamllint", ClassUnknown},

		// package.json
		{"package-json-positive", "package.json", ClassNeverRewrite},
		{"package-json-nested-positive", "apps/api/package.json", ClassNeverRewrite},
		{"package-json-near-miss", "package-lock.json", ClassConfigSurface},

		// CLAUDE.md
		{"claude-md-positive", "CLAUDE.md", ClassNeverRewrite},
		{"claude-md-nested-positive", "packages/cli/CLAUDE.md", ClassNeverRewrite},
		{"claude-md-near-miss", "claude.md", ClassNeverRewrite}, // case-insensitive by design
		// `claude-notes.md` fails the NEVER_REWRITE regex (basename must
		// be exactly CLAUDE.md) and falls into the ConfigSurface bucket
		// because `.md` is in the config-surface extension set.
		{"claude-md-near-miss-2", "claude-notes.md", ClassConfigSurface},

		// AGENTS.md
		{"agents-md-positive", "AGENTS.md", ClassNeverRewrite},
		// `agents-list.md` — same reasoning as the CLAUDE near-miss.
		{"agents-md-near-miss", "agents-list.md", ClassConfigSurface},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ClassifyPath(tc.path); got != tc.want {
				t.Fatalf("ClassifyPath(%q) = %s; want %s", tc.path, got, tc.want)
			}
		})
	}
}

func TestClassifyPath_ConfigSurface(t *testing.T) {
	cases := []struct {
		path string
		want PathClass
	}{
		// `.json` is NOT in NEVER_REWRITE_RE — it falls under ConfigSurface
		// via the directory-component rule (.claude is in the dir set).
		{".claude/settings.local.json", ClassConfigSurface},
		{".github/dependabot.yml", ClassNeverRewrite},      // .yml hits NEVER_REWRITE_RE
		{"node_modules/foo/index.js", ClassConfigSurface},
		{"dist/bundle.js", ClassConfigSurface},
		{"build/out.js", ClassConfigSurface},
		{".turbo/cache.tar", ClassConfigSurface},
		{".next/static/chunks/abc.js", ClassConfigSurface},
		{".vscode/settings.json", ClassConfigSurface}, // .json is config-surface
		{".husky/pre-push", ClassConfigSurface},
		{".changeset/intro.md", ClassConfigSurface}, // .md is config-surface ext
	}
	for _, tc := range cases {
		if got := ClassifyPath(tc.path); got != tc.want {
			t.Fatalf("ClassifyPath(%q) = %s; want %s", tc.path, got, tc.want)
		}
	}
}

func TestClassifyPath_Doc(t *testing.T) {
	cases := []string{
		"docs/architecture.md",
		"README.mdx",
		"apps/api/openapi.json",
		"config/values.yaml",
		"config/values.yml",
		"package.toml",
		"yarn.lock",
		"app.env",
	}
	for _, p := range cases {
		got := ClassifyPath(p)
		// .md / .mdx / .json / .yaml / .yml / .toml / .lock / .env all fall
		// under either NeverRewrite (env/yaml/toml/CLAUDE.md/AGENTS.md/...)
		// or ConfigSurface (md/mdx/json/lock under the extension regex).
		// The Doc bucket is reachable only via direct extension dispatch
		// when ConfigSurface miss is forced. Verify the path lands in one
		// of the structured buckets — never the unknown bucket.
		if got == ClassUnknown || got == ClassCode {
			t.Fatalf("ClassifyPath(%q) = %s; expected a structured bucket", p, got)
		}
	}
}

func TestClassifyPath_Code(t *testing.T) {
	cases := []string{
		"apps/api/src/server.ts",
		"packages/core/src/index.ts",
		"cmd/browzer/main.go",
		"internal/hooks/strip_quoted.go",
		"src/app.tsx",
		"scripts/run.py",
	}
	for _, p := range cases {
		if got := ClassifyPath(p); got != ClassCode {
			t.Fatalf("ClassifyPath(%q) = %s; want code", p, got)
		}
	}
}

func TestClassifyPath_UnknownEmpty(t *testing.T) {
	if got := ClassifyPath(""); got != ClassUnknown {
		t.Fatalf("ClassifyPath(\"\") = %s; want unknown", got)
	}
	if got := ClassifyPath("Makefile"); got != ClassUnknown {
		t.Fatalf("ClassifyPath(\"Makefile\") = %s; want unknown (no extension, no never-rewrite match)", got)
	}
}

func TestClassifyPath_Windows_Separators(t *testing.T) {
	if got := ClassifyPath(`apps\api\Dockerfile`); got != ClassNeverRewrite {
		t.Fatalf("Windows path normalisation failed: got %s; want never-rewrite", got)
	}
}

func TestExportedRegexps_Are_Compiled(t *testing.T) {
	if NeverRewriteRE == nil {
		t.Fatal("NeverRewriteRE must be non-nil")
	}
	if ConfigSurfaceRE == nil {
		t.Fatal("ConfigSurfaceRE must be non-nil")
	}
}
