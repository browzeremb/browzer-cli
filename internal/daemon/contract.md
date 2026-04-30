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
    "session-register.v1",
    "workflow.v1",
    "workflow.fsync.v1"
  ]
}
```

Used by `browzer daemon status`. No tracking, no side effects.

| Field | Type | Notes |
|---|---|---|
| `capabilities` | string[] \| undefined | Optional. Pre-2026-04-29 daemons omit this field; clients treat absence as "no advertised capabilities — fall back". `workflow.v1` indicates the daemon accepts the `WorkflowMutate` method. `workflow.fsync.v1` indicates `awaitDurability=true` produces a fsync'd file + parent dir before responding. |

---

### `Shutdown`

**Params:** `{}`.

**Result:** `{"ok": true}`.

Daemon flushes telemetry, closes DB, removes socket and PID file, exits. `browzer daemon status` after Shutdown returns "not running".

---

### `WorkflowMutate`

Apply a single mutation to a `workflow.json` file. Used by the `browzer workflow <verb>` CLI when `--async`, `--await`, or `--sync` selects the daemon path. The daemon runs each verb's mutator from `internal/workflow/apply.go` (`Mutators` map) inside a per-path FIFO drainer goroutine that owns the advisory lock for the duration of the mutation.

**Params:**
```json
{
  "verb": "set-status",
  "path": "/abs/path/to/docs/browzer/feat-X/workflow.json",
  "payload": {},
  "args": ["step-1", "RUNNING"],
  "jqExpr": "",
  "jqVars": null,
  "noLock": false,
  "awaitDurability": false,
  "lockTimeoutMs": 5000,
  "writeId": "ulid-or-uuid"
}
```

| Field | Type | Notes |
|---|---|---|
| `verb` | string | Required. Must be one of `append-step`, `update-step`, `complete-step`, `set-status`, `set-config`, `append-review-history`, `set-current-step`, `patch`. Daemon rejects unknown verbs with `unknown_verb`. |
| `path` | string (abs) | Required. Must be absolute — relative paths rejected with `path_must_be_absolute`. |
| `payload` | object \| undefined | Optional JSON payload (used by `append-step` + `append-review-history`). Embedded as `json.RawMessage` so structure is verb-defined. |
| `args` | string[] \| undefined | Optional positional args after the verb (e.g. `["step-1", "RUNNING"]` for `set-status`). |
| `jqExpr` | string \| undefined | Optional. Required for `verb: "patch"`. |
| `jqVars` | map[string]any \| undefined | Optional. Used only by `verb: "patch"`. Bind variables for the jq expression — gojq equivalent of `jq --arg KEY VALUE` / `jq --argjson KEY <json>`. Keys are bare identifiers (no leading `$`); values are arbitrary JSON-decoded scalars/objects/arrays. **Additive contract:** older daemons (pre CLI 1.6.0) silently drop this field via standard `json.Unmarshal` (no `DisallowUnknownFields`); the operator-visible failure mode is then a runtime `gojq: undefined variable $<name>` from the patch verb's expression. Restart the daemon (`browzer daemon stop && browzer daemon start`) when upgrading the CLI. Tests pin both decode directions in `internal/daemon/workflow_mutate_test.go` (`TestWorkflowMutateParams_AdditiveJQVarsContract`, `TestWorkflowMutateParams_AbsentJQVarsDecodesAsNilMap`). |
| `noLock` | bool \| undefined | **REJECTED** in the daemon path. Setting `noLock: true` returns `noLock_unsupported_in_daemon_path` so the caller falls back to standalone where `--no-lock` is honored. |
| `awaitDurability` | bool \| undefined | When `true`, daemon returns only after the mutation has been written and `fsync`'d (file + parent dir). When `false`/omitted, daemon returns immediately after enqueue (`mode: "daemon-async"`). |
| `lockTimeoutMs` | int \| undefined | Advisory lock acquisition timeout in milliseconds. Default 5000. The daemon's response ceiling for `awaitDurability=true` is `lockTimeoutMs + 2s`. |
| `writeId` | string | Recommended. Echoed back in the response so callers correlate audit lines across processes. |

**Result:**
```json
{
  "writeId": "ulid-or-uuid",
  "mode": "daemon-async",
  "stepId": "step-1",
  "lockHeldMs": 7,
  "queueDepthAhead": 0,
  "validatedOk": true,
  "durable": false
}
```

| Field | Type | Notes |
|---|---|---|
| `writeId` | string | Echo of request `writeId`. |
| `mode` | string | `daemon-async` (returned immediately) or `daemon-sync` (blocked on durability). Mirrors the audit line's `mode=` field. |
| `stepId` | string | Set for step-scoped verbs; empty for `set-config` / `patch` that target the workflow document itself. |
| `lockHeldMs` | int | How long the advisory lock was held. 0 for `mode: "daemon-async"` because the response is returned before the drainer acquires the lock. |
| `queueDepthAhead` | int | Number of jobs that were buffered ahead of this one when it was enqueued. 0 means the drainer was idle and this job runs first. |
| `validatedOk` | bool | True iff `Validate()` returned no errors after the mutation. Always `true` for `mode: "daemon-async"` because the daemon returned before validation; the drainer's later validation failures are silently dropped (mirrors fire-and-forget semantics). |
| `durable` | bool | True iff `awaitDurability=true` AND fsync of file + parent dir succeeded. Always `false` for `mode: "daemon-async"`. |

**Errors:**

| Code (string) | When | Caller behavior |
|---|---|---|
| `unknown_verb` | Verb not in the whitelist | DO NOT retry; verb is bogus. Surface to user. |
| `invalid_params` | Malformed JSON request | Surface to user. |
| `path_must_be_absolute` | `path` is empty or relative | Caller fixes path; do not fall back. |
| `noLock_unsupported_in_daemon_path` | `noLock: true` was passed | Caller falls back to standalone where `--no-lock` is honored. |
| `queue_full` | Per-path FIFO at capacity (64) | Caller falls back to standalone — same write semantics, lower throughput. |
| `timeout` | `awaitDurability=true` and drainer didn't finish within `lockTimeoutMs+2s` | Caller falls back to standalone and re-applies idempotently. |
| `workflow_dispatcher_disabled` | Server constructed without dispatcher (test harness) | Caller falls back. |
| `method_not_found: WorkflowMutate` | Pre-`workflow.v1` daemon | Caller checks `HasCapability("workflow.v1")` first to avoid this; if missed, falls back. |
| (any `wf.ApplyAndPersist` error) | Mutator / validation / write error | Surface verbatim. |

**Lifecycle:**

1. Handler parses params + validates verb, path, noLock guard.
2. Pushes a `mutateJob` onto the per-path FIFO (`enqueue` in `workflow_queue.go`). Channel cap is 64 per path; overflow returns `queue_full`.
3. The lazy per-path drainer goroutine pulls FIFO, acquires `wf.NewLock(path)`, runs `wf.ApplyAndPersist(path, verb, args, awaitDurability)`, releases lock, signals completion via the job's `done` channel.
4. `mode: "daemon-async"`: handler returns BEFORE step 3 completes.
5. `mode: "daemon-sync"`: handler blocks on `done` (ceiling `lockTimeoutMs+2s`) and returns the drainer's recorded `validatedOk` + `durable` bits.
6. Drainer self-collects after 30 minutes (default; tunable via `daemon.workflow_keepalive_seconds`) of empty channel + zero in-flight sync waiters. The double-check under `dispatcher.mu` prevents loss of jobs that race in past the timer.

**fsync semantics (`awaitDurability=true`):**

- Tmp file: write → `f.Sync()` → close.
- Rename tmp → real path.
- Open dir (containing `workflow.json`) → `dir.Sync()` → close.

A crash anywhere before the dir.Sync() returns leaves the file in a recoverable state (either old contents or new contents on next mount, never partial). A crash after dir.Sync() returns guarantees the new contents survive a power loss.

**Capability negotiation:**

Pre-`workflow.v1` daemons return `method_not_found: WorkflowMutate`. Clients call `Health()` once per 60s and cache the capability set; `HasCapability("workflow.v1")` returns false → caller falls back without ever sending `WorkflowMutate`. A one-shot stderr warning fires the first time the cache misses `workflow.v1` so operators know to restart their daemon.

**Audit format additions** (vs the historic `verb=… stepId=… lockHeldMs=… validatedOk=true` line):

```
verb=set-status path=/abs/.../workflow.json mode=daemon-async writeId=01HXXX \
     stepId=step-1 lockHeldMs=0 validatedOk=true durable=false \
     queueDepthAhead=0 elapsedMs=2 reason=
```

`verb=` stays first; `validatedOk=` stays present; new fields are appended in stable order. Skill stderr parsers anchored on `/^verb=/` and `validatedOk=` keep working unchanged.

---

## Lifecycle

- **Start**: a client (CLI or hook) attempts to connect. On `ENOENT`/`ECONNREFUSED`, it spawns `browzer daemon start --background` and retries with backoff (50ms, 100ms, 250ms, give up).
- **Auto-shutdown**: daemon exits after `daemon.idle_timeout_seconds` (config key, default 600) of zero requests. PID file at `~/.browzer/daemon.pid`.
- **Crash recovery**: stale PID file (process gone) is treated as "not running" by `browzer daemon status` and overwritten on next start.

## Versioning

- The `Health` result includes the daemon's binary version once it ships (post-Phase 0 — added in the Daemon plan).
- Methods are append-only. Removing a method or a required field is a breaking change. Adding optional fields is safe.
