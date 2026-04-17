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

## Token Economy (v0.8.0)

When paired with the Claude Code plugin, `browzer read` replaces built-in `Read`/`Glob`/`Grep` tool calls with daemon-filtered equivalents. Savings reported by `browzer gain` on medium TypeScript/Go repos (numbers below use the calibrated per-language tokenizer shipped in v1.0.3 — see "How `savedTokens` is calculated" below):

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

Detects OS/arch, downloads the matching tarball from the latest GitHub release, verifies the SHA-256 checksum, and drops the binary into `~/.local/bin/browzer`. Pin a tag with `BROWZER_VERSION=v1.0.3`.

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
| `browzer read <path>`                 | Read a file with AST filter (`none\|minimal\|aggressive\|auto`). `auto` uses the manifest      |
| `browzer daemon start [--background]` | Start the Unix-socket JSON-RPC daemon (hot path for `read`, tracking, session cache)           |
| `browzer daemon status`               | Health: uptime, queue length, tracker DB path                                                  |
| `browzer daemon stop`                 | Graceful shutdown                                                                              |
| `browzer gain [--since 7d]`           | Tabular token-savings report. `--ultra` gives a one-line summary                               |
| `browzer config <key> [value]`        | Get/set persisted config. Keys: `tracking`, `hook`, `telemetry`, `daemon.idle_timeout_seconds` |
| `browzer plugin`                      | Print marketplace install instructions (the plugin is installed from **inside** Claude Code)   |

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

- **Hooks** (`hooks/guards/`) — `PreToolUse` for `Read` (rewrites to a filtered daemon path), `Glob` (blocks sensitive patterns), `Grep` (suggests `browzer explore`), and `Bash` (rewrites `find`/`grep` invocations). Plus a `SessionStart` hook that auto-starts the daemon and registers the session's model with the tracker.
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
