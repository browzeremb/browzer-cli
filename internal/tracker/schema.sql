CREATE TABLE IF NOT EXISTS events (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  ts TEXT NOT NULL,
  source TEXT NOT NULL,
  command TEXT,
  path_hash TEXT,
  input_bytes INTEGER NOT NULL,
  output_bytes INTEGER NOT NULL,
  saved_tokens INTEGER NOT NULL,
  savings_pct REAL NOT NULL,
  filter_level TEXT,
  exec_ms INTEGER,
  workspace_id TEXT,
  session_id TEXT,
  model TEXT,
  filter_failed INTEGER NOT NULL DEFAULT 0,
  flushed_at TEXT
);
CREATE INDEX IF NOT EXISTS events_ts ON events(ts);
CREATE INDEX IF NOT EXISTS events_session ON events(session_id);
CREATE INDEX IF NOT EXISTS events_model ON events(model);
CREATE INDEX IF NOT EXISTS events_unflushed ON events(flushed_at) WHERE flushed_at IS NULL;
-- Token Economy v2.0.0 (FR-7): classifies how SavedTokens was derived.
-- 'measured' | 'estimated' | 'counterfactual' | 'unknown'. NULL for legacy
-- rows recorded before this column existed. The actual ALTER TABLE is
-- driven by ensureEstimationMethodColumn() in tracker.go because SQLite
-- does not support `ADD COLUMN IF NOT EXISTS`.
