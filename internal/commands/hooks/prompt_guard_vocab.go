package hooks

import "regexp"

// defaultVocab is the curated list of libraries, frameworks, and services
// whose mentions in a user prompt should trigger a Browzer search advisory.
// Generic language names (typescript, node, python) are intentionally
// omitted — they would fire on every prompt and drown the signal. Mirrors
// the JS guard's DEFAULT_VOCAB list and MUST stay in sync with it.
var defaultVocab = []string{
	// web frameworks
	"fastify",
	"express",
	"hono",
	"koa",
	"nestjs",
	"nest.js",
	"next",
	"next.js",
	"nextjs",
	"react",
	"vue",
	"svelte",
	"sveltekit",
	"remix",
	"astro",
	"solid.js",
	"solidjs",
	// orm / db
	"drizzle",
	"prisma",
	"kysely",
	"typeorm",
	"sequelize",
	"mongoose",
	"postgres",
	"postgresql",
	"mysql",
	"sqlite",
	"mongodb",
	"redis",
	"neo4j",
	"cypher",
	// queues / bg jobs
	"bullmq",
	"bull",
	"celery",
	"sidekiq",
	"agenda",
	"inngest",
	"trigger.dev",
	// auth
	"better-auth",
	"betterauth",
	"clerk",
	"auth0",
	"nextauth",
	"next-auth",
	"lucia",
	"supabase",
	// ui
	"tailwind",
	"tailwindcss",
	"shadcn",
	"shadcn/ui",
	"radix",
	"radix-ui",
	"chakra",
	"mantine",
	"mui",
	"material-ui",
	// testing
	"vitest",
	"jest",
	"mocha",
	"chai",
	"playwright",
	"cypress",
	"testing-library",
	// build / tooling
	"turborepo",
	"turbo",
	"lerna",
	"nx",
	"rush",
	"vite",
	"esbuild",
	"swc",
	"webpack",
	"rollup",
	"tsup",
	"biome",
	"eslint",
	"prettier",
	// validation
	"zod",
	"yup",
	"joi",
	"valibot",
	"ajv",
	// llm / ai
	"langchain",
	"langfuse",
	"langgraph",
	"llamaindex",
	"ai sdk",
	"openai sdk",
	"anthropic sdk",
	"mcp",
	"model context protocol",
	// payments
	"stripe",
	"paddle",
	"lemon squeezy",
	// infra / deploy
	"vercel",
	"railway",
	"netlify",
	"cloudflare workers",
	"cloudflare",
	"fly.io",
	// "render" moved to CooccurrenceVocab — too ambiguous on its own
	// (deploy provider vs. React verb vs. plain English); only fires
	// when paired with a React/JSX/component or deploy-provider cue.
	"aws lambda",
	// observability
	"grafana",
	"prometheus",
	"datadog",
	"sentry",
	"opentelemetry",
	"otel",
	// state / data
	"react-query",
	"tanstack query",
	"tanstack",
	"swr",
	"zustand",
	"jotai",
	"redux",
	"mobx",
	"recoil",
	// pkg mgrs (specific, not "npm" alone)
	"pnpm workspaces",
	"yarn workspaces",
}

// CooccurrenceRule pairs an ambiguous vocab term with a context-cue regex.
// A hit is only recorded when both the term and the cue match the prompt.
// Disambiguates tokens like "render" (React verb vs. deploy host vs. plain
// English) without dropping signal when the context is genuinely present.
type CooccurrenceRule struct {
	Term       string
	RequiresRE *regexp.Regexp
}

// cooccurrenceVocab carries ambiguous-term rules. Currently scopes the
// React-verb / deploy-provider uses of "render" so plain-English usage
// ("render the report", "render as PDF") is suppressed.
var cooccurrenceVocab = []CooccurrenceRule{
	{
		Term:       "render",
		RequiresRE: regexp.MustCompile(`(?i)\b(react|jsx|tsx|component|hook|use[A-Z]|<[A-Z][A-Za-z0-9]*[\s/>]|next\.?js|render\s+(?:function|prop|tree)|re-?render|deploy|hosting|paas|saas|production|staging|provider|build\s+pipeline)\b`),
	},
}

// planTriggers redirects planning/decomposition prompts to the structured
// generate-prd / generate-task workflow skills instead of Claude Code's
// native plan mode. Covers EN + PT-BR phrasings.
var planTriggers = []*regexp.Regexp{
	// English
	regexp.MustCompile(`(?i)\bbreak (?:this|it|the(?:m|se))? (?:into|down) (?:tasks?|prs?|chunks?)\b`),
	regexp.MustCompile(`(?i)\bdecompose (?:this|the) (?:spec|prd|requirements?|feature)\b`),
	regexp.MustCompile(`(?i)\bsplit (?:this|the)? ?(?:into|by) prs?\b`),
	regexp.MustCompile(`(?i)\bwrite (?:a|the) prd\b`),
	regexp.MustCompile(`(?i)\bdraft (?:a|the) (?:prd|requirements? doc|spec)\b`),
	regexp.MustCompile(`(?i)\bplan (?:mode|the implementation|out the work)\b`),
	regexp.MustCompile(`(?i)\b(?:create|build|generate) a roadmap\b`),
	// Portuguese
	regexp.MustCompile(`(?i)\b(?:vamos? )?planejar?\b`),
	regexp.MustCompile(`(?i)\b(?:fazer|escrever|criar) um (?:planejamento|plano|prd|roadmap)\b`),
	regexp.MustCompile(`(?i)\bplan(?:o|ejamento) para (?:matar|resolver|corrigir)\b`),
	regexp.MustCompile(`(?i)\bquebrar (?:em|por|nas?) tarefas?\b`),
	regexp.MustCompile(`(?i)\bdividir (?:em|nas?) prs?\b`),
	regexp.MustCompile(`(?i)\b(?:transformar|converter) (?:em|num?) (?:prd|tarefas?)\b`),
}

// DefaultVocab returns a defensive copy of the curated vocab slice.
// Callers may freely append to the returned slice without affecting the
// package-level state — safe for concurrent use.
func DefaultVocab() []string {
	cp := make([]string, len(defaultVocab))
	copy(cp, defaultVocab)
	return cp
}

// CooccurrenceVocab returns a defensive copy of the co-occurrence rules
// slice. Callers may freely append without affecting the package-level
// state — safe for concurrent use.
func CooccurrenceVocab() []CooccurrenceRule {
	cp := make([]CooccurrenceRule, len(cooccurrenceVocab))
	copy(cp, cooccurrenceVocab)
	return cp
}

// PlanTriggers returns a defensive copy of the plan-trigger regexp slice.
// Callers may freely append without affecting the package-level state —
// safe for concurrent use. The *regexp.Regexp values themselves are
// immutable (regexp package guarantees goroutine safety).
func PlanTriggers() []*regexp.Regexp {
	cp := make([]*regexp.Regexp, len(planTriggers))
	copy(cp, planTriggers)
	return cp
}

// ActionVerbRE requires the prompt to also signal implementation /
// configuration / debug work — conversational mentions of "redis" or
// "react" alone no longer trigger noise. EN + PT-BR.
// *regexp.Regexp is safe for concurrent use (regexp package guarantee).
var ActionVerbRE = regexp.MustCompile(`(?i)\b(implement|implementing|build|building|create|creating|write|writing|fix|fixing|debug|debugging|configure|configuring|setup|set up|integrate|integrating|migrate|migrating|refactor|refactoring|optimi[sz]e|optimi[sz]ing|test|testing|deploy|deploying|add|adding|use|using|connect|connecting|wire|wiring|enable|enabling|disable|disabling|upgrade|upgrading|update|updating|implementar|construir|criar|escrever|corrigir|depurar|configurar|integrar|migrar|refatorar|testar|adicionar|usar|conectar|habilitar|desabilitar|atualizar)\b`)

// SlashCommandRE matches a leading slash command (whitespace + `/word`)
// which should bypass the guard — slash commands are explicit skill
// invocations and never need search advisories.
var SlashCommandRE = regexp.MustCompile(`^\s*/\w`)

// ScopedPackageRE matches scoped npm package paths like @scope/name.
var ScopedPackageRE = regexp.MustCompile(`@[a-z0-9][a-z0-9._-]*\/[a-z0-9][a-z0-9._-]*`)

// InstallCommandRE matches install commands and captures the package
// argument that follows. Group 1 is the captured package name.
var InstallCommandRE = regexp.MustCompile(`\b(?:npm i(?:nstall)?|pnpm add|yarn add|bun add)\s+([@\w\-/.]+)`)

// ImportSourceRE matches `from '...'` and `require('...')` import sources.
// Group 1 (or group 2 fallback) carries the source string.
var ImportSourceRE = regexp.MustCompile(`\bfrom\s+['"]([^'"]+)['"]|\brequire\(\s*['"]([^'"]+)['"]\s*\)`)

// MinPromptLen is the minimum prompt length below which the guard skips
// — shorter prompts almost never carry enough context to seed a search.
const MinPromptLen = 10

// PlanModeAdvisory is the additionalContext emitted when a plan-mode
// trigger fires.
const PlanModeAdvisory = "This prompt asks for planning/decomposition. The Browzer plugin ships two " +
	"workflow skills that do this with structured output:\n" +
	"  • `Skill(skill: \"browzer:generate-prd\")` — emits a Product Requirements Document inline " +
	"(problem, scope, requirements, acceptance criteria, repo invariants).\n" +
	"  • `Skill(skill: \"browzer:generate-task\")` — decomposes a PRD into ordered, " +
	"mergeable, PR-sized engineering tasks with per-task verification plans.\n" +
	"Prefer these over Claude Code's native plan mode — they ground in " +
	"`browzer explore`/`search`/`deps`, surface repo invariants from CLAUDE.md, " +
	"and emit artifacts the next phase (`execute`) consumes directly."
