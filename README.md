# Browzer CLI

Hybrid vector + Graph RAG for your codebase, from the terminal.

A small Go binary that talks to the Browzer server: log in, register a
git repository as a workspace, sync code + markdown, and run semantic
search from the shell. Designed to be agent-friendly — every read
command supports `--json` and `--save <file>` for clean SKILL parsing.

## Install

Pick whichever channel matches your environment.

### 1. Quick install (Linux / macOS)

```sh
curl -fsSL https://browzeremb.com/install.sh | sh
```

Detects OS/arch, downloads the matching tarball from the latest GitHub
release, verifies the SHA-256 checksum, and drops the binary into
`~/.local/bin/browzer`. Set `BROWZER_VERSION=v0.1.0` to pin a tag.

### 2. Homebrew (macOS / Linux)

```sh
brew install browzeremb/tap/browzer
```

### 3. Scoop (Windows)

```powershell
scoop bucket add browzeremb https://github.com/browzeremb/scoop-bucket
scoop install browzer
```

### 4. `go install` (any Go-enabled host)

```sh
go install github.com/browzeremb/browzer-cli/cmd/browzer@latest
```

Compiles from source. Requires Go 1.21+. The binary lands in
`$(go env GOPATH)/bin`.

## Quick start

```sh
browzer login                       # device-flow OAuth
cd /path/to/your/repo
browzer init --name my-project      # create + index workspace
browzer search "fastify graph store"
browzer explore "auth middleware"
browzer status
```

## Commands

The canonical form is **noun-grouped** under `workspace`. Top-level
aliases (`browzer init`, `browzer sync`, ...) still work for backward
compat — both forms call the same handlers.

| Command                                  | What it does                                                          |
| ---------------------------------------- | --------------------------------------------------------------------- |
| `browzer login [--key]`                  | Device-flow OAuth or non-interactive API-key login                    |
| `browzer logout`                         | Revoke and forget local credentials                                   |
| `browzer workspace init [--name N]`      | Create a fresh workspace and index this repo                          |
| `browzer workspace sync [flags]`         | Re-index code + delta-upload docs in one step (code first, then docs) |
| `browzer workspace status`               | Show login + workspace state                                          |
| `browzer workspace explore <q>`          | Hybrid graph + vector search                                          |
| `browzer workspace search <q>`           | Vector search over markdown docs                                      |
| `browzer workspace list`                 | List workspaces in your org                                           |
| `browzer workspace get <id>`             | Fetch a single workspace JSON (schema-discovery helper)               |
| `browzer workspace delete <id>`          | Delete a workspace and all its data                                   |
| `browzer job get <batchId>`              | Inspect an async ingestion batch returned by `sync --no-wait`         |

### `workspace sync` flags

| Flag | Default | Meaning |
|---|---|---|
| `--skip-code` | false | Skip the code re-index step |
| `--skip-docs` | false | Skip the document delta-upload step |
| `--dry-run` | false | Print what would be done without making changes |
| `--no-wait` | false | Return immediately after enqueueing (poll with `browzer job get`) |
| `--json` | false | Output as JSON |
| `--save <file>` | — | Write JSON output to file (implies `--json`) |

Code index always runs before document upload when both are enabled — Package nodes must exist
in the graph before entity extraction can create `RELEVANT_TO` edges.

## Agent-friendly flags

Every read/search command supports:

- `--json` — compact JSON to stdout (no banners)
- `--save <file>` — write JSON to a file (implies `--json`, stdout silent)
- `--limit <n>` — bound on results (1–200) for `explore`/`search`

Plus:

- `browzer explore --schema` — print the response JSON Schema without
  hitting the server
- `browzer workspace get <id> --save ws.json` — discover workspace shape
- `browzer sync --no-wait --json` + `browzer job get <id> --json` — async
  loop dourado for SKILLs that want to poll on their own cadence

## Exit codes

| Code | Meaning                                |
| ---: | -------------------------------------- |
|  `0` | Success                                |
|  `1` | Generic error                          |
|  `2` | Not authenticated (run `browzer login`)|
|  `3` | No Browzer project here                |
|  `4` | Resource not found                     |
| `130`| Interrupted (SIGINT)                   |
| `143`| Terminated (SIGTERM)                   |

## Environment variables

| Var                       | Purpose                                                                |
| ------------------------- | ---------------------------------------------------------------------- |
| `BROWZER_HOME`            | Override `~/.browzer/` (useful for tests / shared hosts)               |
| `BROWZER_SERVER`          | Default `--server` for `login` (e.g. `http://localhost:8080`)          |
| `BROWZER_API_KEY`         | Fallback for `login --key ''`                                          |
| `BROWZER_ALLOW_INSECURE`  | Set to `1` to allow plain HTTP to non-loopback hosts                   |

## Known limitations

- Symlinks are skipped at every walker level (defense against escape via
  symlink-to-secret).
- Recursion depth capped at 32 directories.
- Files larger than 5 MiB are excluded from doc upload.
- Binary files (null byte / >30% non-printable) are dropped before
  embedding.
- Sensitive files (`.env*`, `*.key`, `id_rsa`, `credentials.*`, etc.) are
  hardcoded blocklisted and never read from disk.

## License

MIT — see [LICENSE](./LICENSE).
