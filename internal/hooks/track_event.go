package hooks

// TrackEvent is the fire-and-forget convenience wrapper that hook
// subcommands (`track-cli`, `track-wasted`, `subagent-stop`,
// `incremental-sync`) call when they want to emit a telemetry event.
//
// Wraps DaemonCall("Track", payload) and intentionally swallows the
// error: telemetry is best-effort and must NEVER fail a tool call.
// The fallback path inside DaemonCall already handles the
// "daemon unreachable" case by appending the event to
// $HOME/.browzer/pending-events.jsonl and triggering a detached
// respawn, so a caller that ignored the error still leaves the
// system in a state where the event will be replayed later.
//
// `payload` is loosely typed — typical callers pass a
// daemon.TrackParams or a map[string]any whose keys mirror the
// TrackParams JSON tags. DaemonCall normalizes either shape into
// the typed call via coerceTrackParams.
func TrackEvent(payload any) {
	_, _ = DaemonCall("Track", payload)
}
