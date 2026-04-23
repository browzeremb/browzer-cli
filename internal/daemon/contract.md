# browzer daemon — JSON-RPC contract

> **Status**: frozen contract (Phase 0). The Go daemon implements this; the Node hooks (`packages/skills/hooks/guards/*.mjs`) consume it. Breaking changes require a coordinated update to BOTH sides.

## Transport

- Unix socket at `/tmp/browzer-daemon.<uid>.sock` (mode `0600`, owner = current uid).
- Newline-delimited JSON-RPC 2.0 (`{"jsonrpc":"2.0","id":N,"method":"...","params":{...}}` per line).
- One request per line; one response per line.
- Connection is short-lived: clients open, send 1 request, read 1 response, close.

## Methods

### `Read`

Filter and return a file. Used by `browzer-rewrite-read.mjs` and the `browzer read` CLI command.

**Params:**
```json
{
  "path": "/abs/path/to/foo.ts",
  "filterLevel": "auto",
  "offset": null,
  "limit": null,
  "workspaceId": "3414426e-2657-4939-a68c-9acc14988fd8",
  "sessionId": "claude-session-abc",
  "model": "claude-opus-4-6"
}
```

| Field | Type | Notes |
|---|---|---|
| `path` | string (abs) | Required. Canonical (resolve via `RealPath` first). |
| `filterLevel` | `"auto"\|"none"\|"minimal"\|"aggressive"` | Required. Auto = daemon picks per heuristic in spec §4.2. |
| `offset` | int \| null | Optional. When non-null, filter is forced to `none`. |
| `limit` | int \| null | Optional. Same rule as `offset`. |
| `workspaceId` | string \| null | Optional. Read by the caller from the nearest `.browzer/config.json`. When present, daemon resolves the per-workspace manifest and uses per-file symbol/import/export entries to drive `filterLevel: "aggressive"`. When omitted or manifest missing, aggressive downgrades to minimal. |
| `sessionId` | string \| null | Optional. Used to attribute tracking event to a Claude session. |
| `model` | string \| null | Optional. Model name extracted from session by `browzer-session-start.mjs`. |

**Result:**
```json
{
  "tempPath": "/tmp/brz-read-abc12345.ts",
  "savedTokens": 1234,
  "filter": "aggressive",
  "filterFailed": false
}
```

| Field | Type | Notes |
|---|---|---|
| `tempPath` | string (abs) | Path to the filtered output. Caller reads this, then daemon will GC after 60s. |
| `savedTokens` | int | `(rawBytes - filteredBytes) / charsPerToken[language]`. Calibrated per-language (v1.0.3, 2026-04-17, commit `c628063`) against the Anthropic Claude `count_tokens` API; median divisor `2.36` when language is unknown. Mean absolute error dropped from 35 % (pre-calibration `/ 4`) to 14 %. See `packages/cli/README.md` §"How `savedTokens` is calculated" for the per-language divisor table. Can be 0. |
| `filter` | string | Effective filter level used (resolves "auto" to a concrete level). |
| `filterFailed` | bool | When `true`, `tempPath` contains the raw file unchanged (passthrough fallback). |

**Errors:** `path_not_found`, `path_outside_workspace`, `manifest_unavailable` (returned as JSON-RPC error object). Caller falls back to passthrough on any error.

---

### `Track`

Record a tracking event. Used by `browzer read` CLI itself and by every hook guard.

**Params:**
```json
{
  "ts": "2026-04-15T10:23:00Z",
  "source": "hook-read",
  "command": "Read",
  "pathHash": "sha256_hex_or_null",
  "inputBytes": 24000,
  "outputBytes": 4800,
  "savedTokens": 4800,
  "savingsPct": 80.0,
  "filterLevel": "aggressive",
  "execMs": 12,
  "workspaceId": "ws_1",
  "sessionId": "claude-session-abc",
  "model": "claude-opus-4-6",
  "filterFailed": false
}
```

Field semantics match the SQLite schema in spec §5.1.

**Result:** `{"ok": true}`.

**Errors:** `invalid_event` (validation), `db_unavailable` (returned but caller proceeds — telemetry is best-effort).

---

### `SessionRegister`

Cache the model for a Claude Code session by reading the transcript. Used once per session by `browzer-session-start.mjs`.

**Params:**
```json
{
  "sessionId": "claude-session-abc",
  "transcriptPath": "/Users/x/.claude/projects/.../session-abc.jsonl"
}
```

**Result:**
```json
{
  "model": "claude-opus-4-6"
}
```

When `model` cannot be extracted, returns `{"model": null}`. Daemon caches in `~/.browzer/sessions/<sessionId>.json`; subsequent `Read` calls with the same `sessionId` can omit `model` and the daemon will fill it in.

**Errors:** `transcript_unreadable`.

---

### `Health`

**Params:** `{}`.

**Result:**
```json
{
  "uptimeSec": 1234,
  "queueLen": 0,
  "dbPath": "/Users/x/.local/share/browzer/history.db"
}
```

Used by `browzer daemon status`. No tracking, no side effects.

---

### `Shutdown`

**Params:** `{}`.

**Result:** `{"ok": true}`.

Daemon flushes telemetry, closes DB, removes socket and PID file, exits. `browzer daemon status` after Shutdown returns "not running".

---

## Lifecycle

- **Start**: a client (CLI or hook) attempts to connect. On `ENOENT`/`ECONNREFUSED`, it spawns `browzer daemon start --background` and retries with backoff (50ms, 100ms, 250ms, give up).
- **Auto-shutdown**: daemon exits after `daemon.idle_timeout_seconds` (config key, default 600) of zero requests. PID file at `~/.browzer/daemon.pid`.
- **Crash recovery**: stale PID file (process gone) is treated as "not running" by `browzer daemon status` and overwritten on next start.

## Versioning

- The `Health` result includes the daemon's binary version once it ships (post-Phase 0 — added in the Daemon plan).
- Methods are append-only. Removing a method or a required field is a breaking change. Adding optional fields is safe.
