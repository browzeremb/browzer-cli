<p align="center">
  <strong>Browzer CLI — Hybrid vector + Graph RAG for your codebase, from the terminal</strong>
</p>

<p align="center">
  <a href="https://opensource.org/licenses/MIT"><img src="https://img.shields.io/badge/License-MIT-yellow.svg" alt="License: MIT"></a>
  <a href="https://github.com/browzeremb/browzer-cli/releases"><img src="https://img.shields.io/github/v/release/browzeremb/browzer-cli" alt="Release"></a>
  <a href="https://github.com/browzeremb/browzer-cli/actions"><img src="https://github.com/browzeremb/browzer-cli/workflows/CI/badge.svg" alt="CI"></a>
  <img src="https://img.shields.io/badge/Go-1.25%2B-00ADD8?logo=go" alt="Go 1.25+">
  <img src="https://img.shields.io/badge/platforms-macOS%20%7C%20Linux%20%7C%20Windows-informational" alt="Platforms">
</p>

<p align="center">
  <a href="https://browzeremb.com">Website</a> &bull;
  <a href="#installation">Install</a> &bull;
  <a href="#quick-start">Quick Start</a> &bull;
  <a href="#commands">Commands</a> &bull;
  <a href="#claude-code-integration-plugin">Plugin</a> &bull;
  <a href="https://github.com/browzeremb/browzer-cli/issues">Issues</a>
</p>

---

A single Go binary that talks to the Browzer server: log in, register a git repository as a workspace, sync code + markdown, run hybrid semantic + graph search, and — when paired with the Browzer Claude Code plugin — feed filtered file reads, gitignore-aware globs, and token telemetry back into your AI agent's context window.

Designed to be agent-friendly: every read command supports `--json` and `--save <file>` for clean SKILL / slash-command parsing.

> [!IMPORTANT]
> **The [Browzer Claude Code plugin](https://github.com/browzeremb/skills) is HIGHLY RECOMMENDED.** Without it the CLI works, but you lose the integrations that make it shine: Read/Glob/Grep auto-rewrite through the token-saving daemon, the SessionStart hook that boots `browzer-daemon` and registers the active model, the full workflow skills (`prd → task → execute → commit → sync`), and the pre-flight context probe on every session. Install it inside Claude Code:
>
> ```
> /plugin marketplace add browzeremb/skills
> /plugin install browzer@browzer-marketplace
> ```
>
> Or run `browzer plugin` at any time to reprint these instructions.

## Token Economy (v2.0.0 — measured / estimated / counterfactual)

When paired with the Claude Code plugin, the daemon and the plugin's hook guards observe Claude Code's tool calls and record the realized token savings into a local SQLite tracker. As of v2.0.0 the tracker classifies every event by **estimation method** (`measured | estimated | counterfactual | unknown`) so `browzer gain` can report adoption as a meaningful ratio rather than a single ghost total.

> Note: the v1 (2026-04-15) Read-rewrite path was reverted in 2026-04-16 because rewriting `tool_input.file_path` broke `Edit` round-trips. The current Read hook is **PostToolUse, advisory-only** — it observes the filtered output bytes after the tool ran, never substitutes its input.

**Sources observed**:

| Source | What it tracks | Method |
|---|---|---|
| `hook-read` | Claude Code `Read` calls | measured (filtered output bytes) for ≥40 KB files; estimated otherwise |
| `hook-grep` | Claude Code `Grep` calls | measured (actual tool output bytes) |
| `hook-glob` | Claude Code `Glob` calls | measured in soft-mode; counterfactual (manifest-derived) in block-mode |
| `hook-cli-explore` | `browzer explore` invoked by the agent via Bash | measured |
| `hook-cli-search` | `browzer search` invoked by the agent via Bash | measured |
| `hook-cli-deps` | `browzer deps` invoked by the agent via Bash | measured |
| `hook-cli-ask` | `browzer ask` invoked by the agent via Bash | measured |
| `hook-run` | `browzer run <cmd>` proxy — compresses stdout from git / vitest / turbo / go test / cargo test / biome / tsc before the LLM sees it | measured |
| `wasted-grep` | A `Grep` that the plugin would have rewritten to `browzer explore` | counterfactual |
| `wasted-find` | A `Bash(find ...)` that the plugin would have rewritten to `browzer explore` / `browzer deps` | counterfactual |

Savings reported by `browzer gain` on medium TypeScript/Go repos (numbers below use the calibrated per-language tokenizer shipped in v1.0.3 — see "How `savedTokens` is calculated" below):

| Operation                | Standard | browzer `read` (`--filter auto`) | Savings |
| ------------------------ | -------: | -------------------------------: | ------: |
| `Read` a 2k-line `.ts`   |  ~33,000 |                           ~5,000 |    -85% |
| `Read` a 500-line `.go`  |   ~9,300 |                           ~1,700 |    -82% |
| `Glob` into a large repo |  ~14,000 |                           ~1,400 |    -90% |
| `Grep` wide codebase     |  ~25,000 |                           ~4,200 |    -83% |

> Actual savings vary by project. Run `browzer gain --since 7d` for your own numbers.
>
> Pre-v1.0.3 the daemon used a flat `bytes/4` heuristic that under-reported Claude tokens by ~40%. Historic `browzer gain` rows written before the upgrade reflect the old (lower) numbers; events tracked after v1.0.3 use the calibrated per-language coefficients and match Anthropic billing within ~14% mean absolute error.

### How `savedTokens` is calculated

The daemon does not ship Claude's tokenizer (Anthropic doesn't publish it publicly for Claude 3/4). Instead, `savedTokens` is estimated per-language from the byte delta using coefficients calibrated against Anthropic's `count_tokens` API:

```
savedTokens = (rawBytes - filteredBytes) / charsPerToken[language]
```

| Language   | chars/token | Source                                            |
| ---------- | ----------: | ------------------------------------------------- |
| typescript |        2.39 | median over N=28 files in the calibration sample  |
| javascript |        2.22 | N=8                                               |
| go         |        2.15 | N=12                                              |
| python     |        2.79 | N=2 (thin sample — may refine with more data)     |
| markdown   |        2.56 | N=12                                              |
| json       |        1.97 | N=4                                               |
| yaml       |        2.36 | N=4                                               |
| *default*  |        2.36 | overall median when language is unknown           |

Calibration methodology: 70 files × `count_tokens` (claude-opus-4-7), corrected for the 11-token chat wrapper overhead, fit by language. Mean absolute error on the savings delta: **14%** (vs **35%** for the previous flat `÷4` heuristic). Family-4 models (Opus / Sonnet / Haiku) share the same tokenizer, so one model suffices.

The absolute number still diverges from the Anthropic billing figure by single-digit percent — for exact per-request audits, use `count_tokens` directly or inspect the `usage` block the Anthropic API returns on every response.

## Installation

Pick whichever channel matches your environment.

### Quick install (Linux / macOS)

```sh
curl -fsSL https://browzeremb.com/install.sh | sh
```

Detects OS/arch, downloads the matching tarball from the latest GitHub release, verifies the SHA-256 checksum, and drops the binary into `~/.local/bin/browzer`. Pin a specific release with `BROWZER_VERSION=v1.5.0` (or any tag from `git tag -l 'cli-v*'`); omit the variable to install the latest tag.

### Homebrew (macOS / Linux)

```sh
brew install browzeremb/tap/browzer
```

### Scoop (Windows)

```powershell
scoop bucket add browzeremb https://github.com/browzeremb/scoop-bucket
scoop install browzer
```

### `go install` (any Go-enabled host)

```sh
go install github.com/browzeremb/browzer-cli/cmd/browzer@latest
```

Requires **Go 1.25+**. Binary lands in `$(go env GOPATH)/bin`.

### Upgrading

```sh
browzer upgrade            # print the channel-appropriate upgrade command
browzer upgrade --check    # exit 0 if up-to-date, 10 if a newer release exists
```

### Verify installation

```sh
browzer --version
browzer status --json
```

## Quick Start

```sh
browzer login                        # device-flow OAuth
cd /path/to/your/repo
browzer init --name my-project       # create + index workspace
browzer search "fastify graph store" # vector over docs
browzer explore "auth middleware"    # hybrid graph + vector over code
browzer status

# HIGHLY RECOMMENDED — install the Claude Code plugin (run inside Claude Code):
#   /plugin marketplace add browzeremb/skills
#   /plugin install browzer@browzer-marketplace
# Reprint instructions anytime with:
browzer plugin
```

## How It Works

```
  Without browzer:                                      With browzer + plugin:

  Claude --Read huge.ts--> shell                        Claude --Read huge.ts--> daemon --> filter (auto)
    ^                        |                            ^                         |          |
    |   ~20,000 tokens (raw) |                            |   ~3,000 tokens         | AST trim |
    +------------------------+                            +--- (signatures only) ---+----------+
```

Three search surfaces, one index:

1. **`browzer explore`** — hybrid graph + vector search over indexed code. Returns symbols, imports, exports, blast radius.
2. **`browzer search`** — pure vector search over indexed markdown (ADRs, runbooks, specs).
3. **`browzer deps`** — per-file dependency graph (forward + reverse).

## Commands

The canonical form is **noun-grouped** under `workspace`. Top-level aliases (`browzer init`, `browzer sync`, ...) still work for backward compat.

### Auth & workspace

| Command                             | Purpose                                                                |
| ----------------------------------- | ---------------------------------------------------------------------- |
| `browzer login [--key K]`           | Device-flow OAuth or non-interactive API-key login                     |
| `browzer logout`                    | Revoke + forget `~/.browzer/credentials`                               |
| `browzer status`                    | Show login + workspace state                                           |
| `browzer workspace init [--name N]` | Create a fresh workspace and index this repo                           |
| `browzer workspace sync`            | Re-index code + delta-upload docs (code first, then docs)              |
| `browzer workspace index`           | Re-parse code only (no doc upload)                                     |
| `browzer workspace docs`            | (Re-)index documents (interactive by default; `--yes` non-interactive) |
| `browzer workspace list`            | List workspaces in your org                                            |
| `browzer workspace get <id>`        | Fetch a single workspace JSON                                          |
| `browzer workspace show [id]`       | Full workspace detail including docs + files                           |
| `browzer workspace files-list`      | List files indexed in a workspace                                      |
| `browzer workspace docs-list`       | List documents indexed in a workspace                                  |
| `browzer workspace relink`          | Point current directory at an existing workspace                       |
| `browzer workspace unlink`          | Remove `.browzer/config.json` local link                               |
| `browzer workspace delete <id>`     | Delete a workspace and all its data                                    |
| `browzer workspace manifest`        | Print the cached graph-fingerprint manifest                            |

### Retrieval & ask

| Command                  | Purpose                                                                                |
| ------------------------ | -------------------------------------------------------------------------------------- |
| `browzer explore <q>`    | Hybrid graph + vector search over indexed code                                         |
| `browzer search <q>`     | Vector search over indexed markdown docs                                               |
| `browzer deps <path>`    | Per-file dependency graph (forward + reverse). Flags: `--reverse`, `--limit`, `--json` |
| `browzer ask <question>` | End-to-end ask (search + LLM). Resolves `workspaceId` via flag → config → first-in-org |
| `browzer job get <id>`   | Inspect async ingestion batches returned by `sync --no-wait`                           |

### Token economy (Claude Code integration)

Introduced in v0.8.0 to reduce token burn when Claude Code reads files, globs, or greps. Requires `browzer-daemon` running (auto-started by the plugin's `SessionStart` hook).

| Command                               | Purpose                                                                                        |
| ------------------------------------- | ---------------------------------------------------------------------------------------------- |
| `browzer run <cmd> [--no-filter]`     | Run any shell command and compress its stdout through a family-specific filter before returning it to the LLM. Supports git (status/log/diff/push/pull), vitest, pnpm turbo test, go test, cargo test, biome, and tsc. Non-zero exits return raw output unchanged. `--no-filter` disables compression. The plugin's `PreToolUse(Bash)` hook auto-rewrites these command families to `browzer run <cmd>`. |
| `browzer read <path>`                 | Read a file with AST filter (`none\|minimal\|aggressive\|auto`). `auto` uses the manifest      |
| `browzer daemon start [--background]` | Start the Unix-socket JSON-RPC daemon (hot path for `read`, tracking, session cache)           |
| `browzer daemon status`               | Health: uptime, queue length, tracker DB path                                                  |
| `browzer daemon stop`                 | Graceful shutdown                                                                              |
| `browzer gain [--since 7d]`           | Tabular token-savings report. `--ultra` gives a one-line summary. New: `--by method` groups by `measured\|estimated\|counterfactual\|unknown`; `--adoption` prints the adoption ratio `SUM(saved \| measured+estimated+counterfactual) / ABS(SUM(wasted))` |
| `browzer config <key> [value]`        | Get/set persisted config. Keys: `tracking`, `hook`, `telemetry`, `daemon.idle_timeout_seconds` |
| `browzer plugin`                      | Print marketplace install instructions (the plugin is installed from **inside** Claude Code)   |

#### Staleness signals on `browzer status --json`

`browzer status --json` exposes a `staleness` block with the daemon's view of its caches. Notable fields:

- `staleness.manifestCachePresent` (bool) — `true` iff the workspace manifest is currently cached on disk under `~/.browzer/manifests/`. `false` means the next Glob block-mode call will fall back to soft-mode and the next aggressive `Read` will downgrade to minimal until the manifest is re-pulled. Surfaces what was previously a silent failure mode.

#### `~/.browzer/pending-events.jsonl` (durability)

When the daemon is unreachable (mid-respawn, post-crash, or never started), hook guards append failed `Track` payloads — one JSON line per event, mode `0600` — to `~/.browzer/pending-events.jsonl`. On the next `browzer daemon start`, the daemon **renames** the file to `pending-events.jsonl.draining` (under a sidecar `pending-events.jsonl.lock` acquired with `O_CREAT|O_EXCL`), drops the lock, and replays the renamed file. Hooks landing during the drain append to a fresh `pending-events.jsonl` rather than blocking. Bounded at 10 MB by hook-side rotation. Successful `Track` calls bypass the JSONL entirely — the queue is the failure path only.

### Workflow state (`docs/browzer/<feat>/workflow.json`)

The workflow surface is intentionally narrow in v3.0.0: read with `get-step`, persist with `save-step`, plus a small set of structured helpers. Every persist call acquires an advisory flock, validates the payload against the CUE schema (`packages/cli/schemas/workflow-v1.cue`), and writes atomically (tmp+rename + optional fsync). Skills no longer roll their own `jq | mv` blocks.

| Command                                                              | Purpose                                                                                  |
| -------------------------------------------------------------------- | ---------------------------------------------------------------------------------------- |
| `browzer get-step <ID> --id <feat>`                                  | Read a step. Default emits a rich markdown view formatted for LLM context (role + scope + skills + PRD slice + deps + invariants + done-when); `--json` emits the `#StepView` payload. Accepted IDs: `PRD`, `TASKS`, `TASK_NN`, `CODE_REVIEW`, `RECEIVING_CODE_REVIEW`, `WRITE_TESTS`, `UPDATE_DOCS`, `FEATURE_ACCEPTANCE`, `COMMIT`, `BRAINSTORM`, plus two virtual phases — `ORIGINAL_REQUEST` (verbatim operator ask captured at `workflow init`) and `CONFIG` (carries `executionStrategy`, `mode`, `setAt`). Also exposed under `browzer workflow get-step`. New flags: `--field <a,b,c>` projects a sub-tree of the `#StepView` JSON (e.g. `--field invariants,doneWhen` returns only those keys); `--invariants=auto\|full\|hash\|skip` controls invariant verbosity (`auto` = include when non-empty, `hash` = SHA-256 digest only, `skip` = omit entirely). Example: `browzer get-step CODE_REVIEW --id feat-123 --json --field findings --invariants=hash`. |
| `browzer save-step <PHASE> --id <feat> [--from <file>] [--stdin]`    | Validate a phase payload against CUE and persist it into `workflow.json`. Accepts a markdown PRD via the markdown parser or a JSON payload. Flags: `--validate-only`, `--quiet`, `--hint-fixes` (emit `expected one of [...]; example: "<key>": "<value>"` on enum / unknown-field violations). `--async` is **deprecated** — it exits `2` (not-authenticated sentinel). Use `--await` (daemon + fsync, recommended) or `--sync` (in-process standalone). Exits silent on success. |
| `browzer save-step-batch --from PHASE=PATH ...`                      | Persist multiple phase payloads atomically. All `--from` entries are CUE-validated against the in-memory mutated document under a single advisory flock; on any individual failure the batch rolls back without writing. Duplicate phase names exit `2`. Flags: `--validate-only`, `--quiet`, `--hint-fixes`. |
| `browzer workflow append-step --workflow <p>`                        | Append a new step JSON (stdin or `--payload`); recomputes counters                       |
| `browzer workflow append-steps --workflow <p>`                       | Bulk append (single lock + write)                                                         |
| `browzer workflow set-finding-statuses --batch '<json>' --workflow <p>` | Bulk-update finding statuses inside one lock + write                                  |
| `browzer workflow describe-step-type <NAME> --json [--include-base] [--field <jq>] [--inline-enums]` | Print the live CUE-derived payload shape — skills' sole route to discover the shape of a phase before staging output (cache to `/tmp/<feat>/.schema-cache/<NAME>.json`) |
| `browzer workflow validate --workflow <p>`                           | Structural integrity check; exits non-zero on schema violations                          |
| `browzer workflow backfill-elapsed --workflow <p>`                   | Backfill `elapsedMin` on every step where both `startedAt` and `completedAt` are set (post-hoc elapsed-time correction) |
| `browzer workflow schema [--field <jq>]`                             | Print the workflow JSON Schema (Draft 2020-12)                                           |
| `browzer workflow init [--execution-strategy <serial\|parallel\|parallel-worktrees\|agent-teams>] [--mode <autonomous\|review>]` | Scaffold an empty `workflow.json` for a new feature. `--execution-strategy` and `--mode` seed the CONFIG virtual phase consumed by `generate-task` / orchestrator skills. Defaults: strategy `serial`, mode `autonomous`. |
| `browzer codereview aggregate --id <feat-id>`                        | Reads all per-lane `staging/CODE_REVIEW.<member>.json` files and produces a consolidated `staging/CODE_REVIEW.json` with preserve-all (`mergedFrom[]`) semantics — every finding from every lane is retained and tagged with its origin. `--feat <feat-id>` is accepted as an alias for `--id`. Flags: `--dry-run` (print merged output without writing), `--json` (emit consolidated payload to stdout), `--strict` (exit non-zero if any lane file is missing or malformed). Exit codes: `0` success, `2` deprecated/auth sentinel (do not use for this command), `3` no Browzer project, `4` one or more lane files not found. |

**State-mutation contract (v3.0.0)**: a phase skill writes its output to `docs/browzer/<feat>/staging/<PHASE>.{md,json}` via the `Write` tool; the Browzer Claude Code plugin's `PostToolUse(Write)` autosave hook (`hooks/_auto-save-step.mjs`, matched on `docs/browzer/*/staging/**`) then calls `browzer save-step <PHASE> --id <feat> --from <staged-file>`, which CUE-validates and atomically rewrites `workflow.json`. Skills never call retired mutator verbs (`workflow patch`, `workflow save`, `workflow complete-step`, `workflow append-dispatch{,es}`, `workflow append-review-history`, `workflow audit-model-override`, `workflow reapply-additional-context`, `workflow get-config`, `workflow query`) and must not edit `workflow.json` directly. Operators rarely invoke `save-step` themselves — the hook handles it.

**Write modes**: persisting verbs honor `--sync` (in-process standalone) and `--await` (daemon + fsync, recommended default). `--async` is **deprecated** — callers that pass it receive exit code `2`; migrate to `--await` or `--sync`. The env-var `BROWZER_WORKFLOW_MODE=sync|await` overrides config defaults — useful in CI / tests to force standalone path when a long-running daemon binary may be stale.

### Organization / RBAC

| Command               | Purpose                                          |
| --------------------- | ------------------------------------------------ |
| `browzer org show`    | Show the current organization                    |
| `browzer org members` | Manage organization members (list/invite/remove) |
| `browzer org docs`    | List + inspect org-scoped documents              |

### `workspace sync` flags

| Flag            | Default | Meaning                                                      |
| --------------- | ------- | ------------------------------------------------------------ |
| `--skip-code`   | false   | Skip the code re-index step                                  |
| `--skip-docs`   | false   | Skip the document delta-upload step                          |
| `--dry-run`     | false   | Print what would be done without making changes              |
| `--no-wait`     | false   | Return immediately after enqueueing (poll `browzer job get`) |
| `--json`        | false   | Output as JSON                                               |
| `--save <file>` | —       | Write JSON output to file (implies `--json`)                 |

Code index always runs before document upload when both are enabled — Package nodes must exist in the graph before entity extraction can create `RELEVANT_TO` edges.

## Agent-friendly flags

Every read/search command supports:

- `--json` — compact JSON to stdout (no banners)
- `--save <file>` — write JSON to a file (implies `--json`, stdout silent)
- `--limit <n>` — bound on results (1–200) for `explore`/`search`

Global flags:

- `--ultra` — ultra-compact output (smaller payloads, fewer fields — ideal for agent context windows)
- `--llm` — LLM mode: suppresses banner, disables colors + spinners
- `--quiet` — suppress decorative output (cross-cutting on `status`, `explore`, `search`, `deps`, `mentions`, `workspace sync`, and every workflow mutator; default-on under `BROWZER_LLM=1`)
- `-v`/`-vv`/`-vvv` — increase verbosity (decisions / subprocess / raw I/O)

Schema discovery:

- `browzer explore --schema` / `browzer deps --schema` — print the response JSON Schema without hitting the server
- `browzer workspace get <id> --save ws.json` — discover workspace shape
- `browzer sync --no-wait --json` + `browzer job get <id> --json` — async loop for SKILLs that poll on their own cadence

## Claude Code Integration (plugin)

> **Highly recommended.** The plugin is the only way to get Read/Glob/Grep auto-rewrite, the daemon boot hook, and the workflow skills. Without it the CLI is still useful, but you're leaving most of the token savings and agent UX on the table.

### Install (recommended — marketplace)

Run these **inside Claude Code**:

```
/plugin marketplace add browzeremb/skills
/plugin install browzer@browzer-marketplace
```

### Install (local dev — uncommitted changes)

From a monorepo clone:

```sh
claude --plugin-dir ./packages/skills
```

Run `/reload-plugins` inside Claude Code after editing a SKILL.

### What the plugin wires up

- **Hooks** (`hooks/guards/`) — `PreToolUse` for `Read` (rewrites to a filtered daemon path), `Glob` (blocks sensitive patterns), `Grep` (suggests `browzer explore`), and `Bash` (rewrites `find`/`grep` invocations and auto-rewrites git / vitest / turbo / go test / cargo test / biome / tsc to `browzer run <cmd>` for stdout compression). Plus a `PostToolUse(Bash)` hook (`browzer-postuse-run.mjs`) that injects top-5 `additionalContext` entries when `browzer explore`/`search`/`deps`/`ask` returns more than 10 JSON entries. Plus a `SessionStart` hook that auto-starts the daemon and registers the session's model with the tracker.
- **Skills** (`skills/`) — installable slash-commands for RAG workflows, ops, and tooling.
- **Agents** (`agents/`) — long-running specialist agents (the Browzer monorepo ships a `browzer` agent that uses the CLI for deep search).

Previous versions shipped a `browzer plugin install` command that copied files into `.claude/plugins/browzer/`. Claude Code does **not** auto-discover plugins from that path — it only loads them via the marketplace flow or `--plugin-dir`. The old command has been replaced by `browzer plugin`, which just reprints the instructions above.

## Exit codes

|  Code | Meaning                                 |
| ----: | --------------------------------------- |
|   `0` | Success                                 |
|   `1` | Generic error                           |
|   `2` | Not authenticated (run `browzer login`) |
|   `3` | No Browzer project here                 |
|   `4` | Resource not found                      |
|  `10` | `upgrade --check` found a newer release |
| `130` | Interrupted (SIGINT)                    |
| `143` | Terminated (SIGTERM)                    |

## Environment variables

| Var                      | Purpose                                                       |
| ------------------------ | ------------------------------------------------------------- |
| `BROWZER_HOME`           | Override `~/.browzer/` (useful for tests / shared hosts)      |
| `BROWZER_SERVER`         | Default `--server` for `login` (e.g. `http://localhost:8080`) |
| `BROWZER_API_KEY`        | Fallback for `login --key ''`                                 |
| `BROWZER_ALLOW_INSECURE` | Set to `1` to allow plain HTTP to non-loopback hosts          |
| `BROWZER_VERSION`        | Pin a tag in the `install.sh` quick-install channel           |

## Known limitations

- Symlinks are skipped at every walker level (defense against escape via symlink-to-secret).
- Recursion depth capped at 32 directories.
- Files larger than 5 MiB are excluded from doc upload.
- Binary files (null byte / >30% non-printable) are dropped before embedding.
- Sensitive files (`.env*`, `*.key`, `id_rsa`, `credentials.*`, etc.) are hard-coded blocklisted and never read from disk.
- The background daemon is a supported binary on Windows but a **no-op in practice** — Unix domain sockets + `os.Getuid()`-derived paths mean `daemon start` produces a running process that no client can reach. macOS + Linux are the supported daemon hosts.

## Documentation

- [Website](https://browzeremb.com)
- [Public mirror (source + releases)](https://github.com/browzeremb/browzer-cli)
- [Releases](https://github.com/browzeremb/browzer-cli/releases)
- [Issues](https://github.com/browzeremb/browzer-cli/issues)
- [Claude Code SKILLs package](https://github.com/browzeremb/skills)

## License

MIT — see [LICENSE](./LICENSE).
