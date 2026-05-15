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
  "dbPath": "/Users/x/.local/share/browzer/history.db",
  "capabilities": [
    "read.v1",
    "track.v1",
    "session-register.v1"
  ]
}
```

Used by `browzer daemon status`. No tracking, no side effects.

| Field | Type | Notes |
|---|---|---|
| `capabilities` | string[] \| undefined | Optional. Pre-2026-04-29 daemons omit this field; clients treat absence as "no advertised capabilities — fall back". |

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

## Handshake protocol (v3)

The `Daemon.Version` method answers the protocol-negotiation handshake. The
CLI calls it once per `daemon.Client` lifetime (cached for subsequent calls)
to detect protocol mismatch with the running daemon binary.

**`Daemon.Version` params:** `{}`.

**`Daemon.Version` result:**

```json
{
  "daemonVersion": "1.7.0",
  "protocolFeatures": ["estimationMethod"],
  "protocolVersion": 3
}
```

| Field | Type | Notes |
|---|---|---|
| `daemonVersion` | string | CLI binary version, ldflag-injected from `internal/version.Version`. Empty in dev/test builds; populated in goreleaser builds. |
| `protocolFeatures` | string[] | Sorted lexicographically. Capability flags the CLI may consult to gate optional surfaces. Stable across calls — the response is byte-deterministic. |
| `protocolVersion` | int | This is the gate. The CLI compares against its own `daemon.CurrentProtocolVersion` constant; a mismatch instructs the CLI to refuse to issue methods that may have changed shape across the version break. |

**Mismatch handling:**

When the preflight returns a `protocolVersion` different from the CLI's
compile-time constant, the CLI emits a single stderr warning and refuses to
issue any method whose wire contract depends on the missing version. Older
daemons predating the `Daemon.Version` method return JSON-RPC `-32601`
(`method_not_found: Daemon.Version`); the CLI treats any error from
`Daemon.Version` as "no handshake possible".

---

## EstimationMethod field (Token Economy v2.0.0, 2026-05-07)

`Track.params.estimationMethod` and the corresponding SQLite column
`events.estimation_method` classify how `savedTokens` was derived. Optional
on the wire; NULL on disk for legacy rows.

| Value | Meaning |
|---|---|
| `"measured"` | Token delta computed from the calibrated chars-per-token table against actual filtered output bytes. |
| `"estimated"` | Token delta inferred without filter execution (e.g. hook short-circuited). |
| `"counterfactual"` | Comparison against an alternative path that did not run (planning / what-if). |
| `"unknown"` | Caller couldn't classify — distinct from omitting the field, which means "legacy / pre-v2 caller". |

**Wire path** (`Track.params`):

```jsonc
{
  "ts": "...",
  "savedTokens": 4800,
  "estimationMethod": "measured"  // optional; omitted by older clients
}
```

**Storage**: `events.estimation_method TEXT NULL` — added by the daemon's
boot-time migration (`tracker.ensureEstimationMethodColumn`); idempotent
across restarts via `PRAGMA table_info` probe.

**Aggregation**: `tracker.UnsentBuckets` includes `estimation_method` in
the bucket key, so two rows with different methods never coalesce.
`tracker.QueryAggregated(since, "method")` groups the gain report by
method (FR-9; new `method` value joins the existing `source | command |
filter | model | session` set in `groupColumn`).

**Backward compatibility**:

- A pre-v2 CLI sending `Track` to a v2 daemon: omits the field → daemon
  records NULL → bucket has `estimationMethod: null`.
- A v2 CLI sending `Track` to a pre-v2 daemon: silently dropped via JSON
  unknown-field tolerance → no observable failure mode (daemon records
  NULL by virtue of never receiving the value).
- A v2 daemon opening a pre-v2 history.db: column is added at boot;
  pre-existing rows scan as NULL.

---

## Pending events durability (FR-13, 2026-05-07)

Events are appended to `pending-events.jsonl` ONLY when the daemon
`Track` JSON-RPC fails (catch branch in hook guards) — typically when
the daemon is mid-respawn or unreachable. Successful Track calls bypass
the JSONL queue. The drain on next daemon boot replays only the queued
failures.

Rationale: always-append-before-RPC was rejected because the per-event
`fs.appendFileSync` penalty on hot paths (every Bash, every Read ≥40KB)
exceeded the budget for the marginal durability gain.

**File format**:

- Path: `~/.browzer/pending-events.jsonl` (mode `0600`).
- One line per event: a JSON-encoded `daemon.TrackParams` payload.
- Bounded at 10 MB by hook-side rotation (oldest lines truncated when
  the file exceeds the cap). Rotation (stat+rename) is guarded by a
  sidecar lockfile (`pending-events.jsonl.lock`, `O_CREAT|O_EXCL`) to
  serialize concurrent rotations across processes; the append itself
  remains lock-free, relying on POSIX `O_APPEND` atomicity for writes
  ≤ PIPE_BUF.

**Drain semantics** (daemon side, on every `daemon start`):

1. Open the file (or skip if missing → log `drained 0 pending events`).
2. Acquire `flock(LOCK_EX)` for the duration of the drain.
3. Scan line-by-line; for each parsed event call `tracker.Record(...)`.
4. Corrupt lines are warn-logged and skipped without aborting the drain.
5. On all-success: `Truncate(0)` and log `drained N pending events`.
6. On a tracker `Record` error mid-drain: stop, leave the file as-is.
   The next boot retries.

**What happens when the daemon never boots**:

- Events accumulate in the JSONL file up to the 10 MB cap.
- The hooks operate normally: filter/track decisions still log savings;
  `browzer gain` simply lacks the in-flight events until the next drain.
- Operator recovery: `browzer daemon start --background` will drain the
  backlog at boot.

**Tracker unavailability**: when `tracker.Open` fails (`tr == nil`),
`drainPendingEvents` is a no-op — events stay queued for the next boot.

---

## Manifest cache single-flight (F-021, 2026-05-07)

The daemon coalesces concurrent manifest pulls per workspace via
`golang.org/x/sync/singleflight`. The single-flight **key is
`workspaceID` only** — the auth principal (CLI session, API key id) is
intentionally NOT part of the key.

**Why**: the daemon currently runs single-user (Unix socket at
`/tmp/browzer-daemon.<uid>.sock`, mode `0600`, owner-uid only). Every
in-flight manifest pull within a daemon instance is therefore already
scoped to one principal by socket ACL, so adding the principal to the
key would only fragment the coalescing without any isolation gain.

**When this changes**: if a future daemon ever serves multiple
principals over the same socket, sharing an in-flight pull's result
across principals would leak workspace contents across auth boundaries.
That release MUST extend the key to `(workspaceID, principalID)` and
update this note. Until then, callers can rely on at most one
in-flight pull per workspace, regardless of how many requests arrive
during the fetch.

**ENOENT recovery**: the on-disk cache file can disappear between key
acquisition and the file read (concurrent prune, stale TTL eviction).
The loader treats `ENOENT` on the cached path as a cold miss — it
re-pulls and re-populates rather than returning an error to the
caller.

---

## Changelog

### `WorkflowMutate` removed

The daemon previously exposed a `WorkflowMutate` JSON-RPC method for applying mutations to `workflow.json` files (with `workflow.v1` and `workflow.fsync.v1` capability advertisements, a per-path FIFO drainer, and a verb whitelist). The method, its drainer, and both capabilities have been removed; the daemon no longer accepts workflow-write traffic. Clients that target this method receive the standard JSON-RPC `method_not_found: WorkflowMutate` response.
