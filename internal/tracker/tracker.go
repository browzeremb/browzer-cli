// Package tracker is the SQLite-backed event tracker for the Browzer CLI.
// Spec: docs/CHANGELOG.md §2026-04-15 "CLI token economy" (original spec §5.1 archived in git history).
package tracker

import (
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schemaSQL string

// Event is one tracking row. Pointer fields are nullable.
type Event struct {
	TS           string
	Source       string
	Command      string
	PathHash     *string
	InputBytes   int
	OutputBytes  int
	SavedTokens  int
	SavingsPct   float64
	FilterLevel  *string
	ExecMs       int
	WorkspaceID  *string
	SessionID    *string
	Model        *string
	FilterFailed bool
	// EstimationMethod classifies how SavedTokens was derived. Nullable
	// for legacy rows. One of: 'measured' | 'estimated' |
	// 'counterfactual' | 'unknown'. Token Economy v2.0.0 (FR-7).
	EstimationMethod *string
}

// Tracker owns the SQLite connection. Single writer, multiple readers.
//
// Performance: insertStmt is prepared once in Open and reused across every
// Record call. SQLite's WAL allows concurrent readers but a single writer;
// SetMaxOpenConns(1) avoids the driver re-preparing the statement on a
// freshly-opened connection from the pool.
type Tracker struct {
	db         *sql.DB
	insertStmt *sql.Stmt
	mu         sync.Mutex
}

// insertSQL is the canonical event insert statement, prepared once per Tracker.
const insertSQL = `
INSERT INTO events (ts, source, command, path_hash, input_bytes, output_bytes,
                    saved_tokens, savings_pct, filter_level, exec_ms,
                    workspace_id, session_id, model, filter_failed,
                    estimation_method)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

// Open creates the parent directory, opens the SQLite database in WAL mode,
// and applies the schema. Idempotent.
func Open(path string) (*Tracker, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(1)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// SQLite is single-writer; mu already serializes Go-side. Pin one
	// connection so the prepared insert stmt stays cached on it instead
	// of re-preparing every time the pool hands out a fresh conn.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if _, err := db.Exec(schemaSQL); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	// Idempotent column add (NULL-safe for legacy rows).
	if err := ensureEstimationMethodColumn(db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate estimation_method: %w", err)
	}
	insertStmt, err := db.Prepare(insertSQL)
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("prepare insert: %w", err)
	}
	return &Tracker{db: db, insertStmt: insertStmt}, nil
}

// ensureEstimationMethodColumn adds the `estimation_method` column to the
// `events` table when absent. Idempotent across boots: SQLite does not
// support `ALTER TABLE ... ADD COLUMN IF NOT EXISTS`, so we probe via
// PRAGMA table_info and only run the ALTER on miss. Legacy rows scan as
// NULL through *string in the Event struct.
func ensureEstimationMethodColumn(db *sql.DB) error {
	rows, err := db.Query(`PRAGMA table_info(events)`)
	if err != nil {
		return fmt.Errorf("table_info: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var (
			cid       int
			name      string
			ctype     string
			notnull   int
			dfltValue sql.NullString
			pk        int
		)
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dfltValue, &pk); err != nil {
			return fmt.Errorf("scan table_info: %w", err)
		}
		if name == "estimation_method" {
			return nil // already present
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if _, err := db.Exec(`ALTER TABLE events ADD COLUMN estimation_method TEXT`); err != nil {
		return fmt.Errorf("add column: %w", err)
	}
	return nil
}

// Close releases the connection. Closes the cached insert statement first
// so the underlying conn is fully released back to the pool.
func (t *Tracker) Close() error {
	if t.insertStmt != nil {
		_ = t.insertStmt.Close()
	}
	return t.db.Close()
}

// Record inserts one event into SQLite. Retention cleanup is handled
// separately by Cleanup, which is called from a periodic goroutine in
// the daemon — not on every write — to avoid per-call DELETE latency in
// high-frequency hook-read loops.
func (t *Tracker) Record(e Event) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	_, err := t.insertStmt.Exec(
		e.TS, e.Source, e.Command, e.PathHash, e.InputBytes, e.OutputBytes,
		e.SavedTokens, e.SavingsPct, e.FilterLevel, e.ExecMs,
		e.WorkspaceID, e.SessionID, e.Model, boolToInt(e.FilterFailed),
		e.EstimationMethod,
	)
	return err
}

// Cleanup removes events older than 90 days. Intended to be called
// from a periodic goroutine (see daemon_cmd.go).
func (t *Tracker) Cleanup() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	_, err := t.db.Exec(`DELETE FROM events WHERE ts < datetime('now', '-90 days')`)
	return err
}

// AggregatedRow is one row of the gain report.
type AggregatedRow struct {
	Group       string
	N           int
	InputBytes  int64
	OutputBytes int64
	SavedTokens int64
}

// QueryAggregated returns gain summary rows. `since` is e.g. "7d", "24h".
// `groupBy` is one of: source, command, filter, model, session.
// When `includeInternal` is false, events whose source starts with
// "workflow-audit:" are excluded from the result set.
//
// This method is internal to packages/cli — it is not part of any exported
// public API contract. Call-sites are gain.go (two) and tracker_test.go.
func (t *Tracker) QueryAggregated(since, groupBy string, includeInternal bool) ([]AggregatedRow, error) {
	col, err := groupColumn(groupBy)
	if err != nil {
		return nil, err
	}
	cutoff, err := parseSince(since)
	if err != nil {
		return nil, err
	}

	// Build WHERE clauses in Go so the SQL predicate is structural, not
	// data-driven. Avoids passing boolean state as a SQL operand (which
	// forces SQLite to evaluate the LIKE on every row even when the filter
	// is disabled) and makes future optional filters easy to add.
	where := []string{"ts >= ?"}
	if !includeInternal {
		where = append(where, "source NOT LIKE 'workflow-audit:%'")
	}

	q := fmt.Sprintf(
		`SELECT COALESCE(%s, '<unknown>') AS g, COUNT(*) AS n,
		        SUM(input_bytes), SUM(output_bytes), SUM(saved_tokens)
		 FROM events WHERE %s
		 GROUP BY g ORDER BY n DESC`,
		col, strings.Join(where, " AND "),
	)
	rows, err := t.db.Query(q, cutoff.UTC().Format(time.RFC3339))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []AggregatedRow
	for rows.Next() {
		var r AggregatedRow
		if err := rows.Scan(&r.Group, &r.N, &r.InputBytes, &r.OutputBytes, &r.SavedTokens); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Bucket is one telemetry bucket ready for the wire.
type Bucket struct {
	Day         string
	Source      string
	FilterLevel *string
	Model       *string
	N           int
	InputBytes  int64
	OutputBytes int64
	SavedTokens int64
	// EstimationMethod is the per-bucket classification (FR-7 / FR-9).
	// Nullable when the contributing rows predate the column.
	EstimationMethod *string
}

// UnsentBuckets aggregates all rows where flushed_at IS NULL into per-(day,
// source, filter_level, model) buckets. Returns the buckets AND the row ids
// that contributed (to be passed to MarkFlushed after the POST succeeds).
func (t *Tracker) UnsentBuckets() ([]Bucket, []int64, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	rows, err := t.db.Query(`
		SELECT id, ts, source, filter_level, model, input_bytes, output_bytes, saved_tokens,
		       estimation_method
		FROM events WHERE flushed_at IS NULL
	`)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = rows.Close() }()

	type key struct {
		day, source, filter, model, estMethod string
		filterNull, modelNull, estMethodNull  bool
	}
	agg := map[key]*Bucket{}
	var ids []int64
	for rows.Next() {
		var (
			id                     int64
			ts, source             string
			filter, mdl, estMethod sql.NullString
			inB, outB, saved       int64
		)
		if err := rows.Scan(&id, &ts, &source, &filter, &mdl, &inB, &outB, &saved, &estMethod); err != nil {
			return nil, nil, err
		}
		ids = append(ids, id)
		day := ts[:10] // YYYY-MM-DD
		k := key{day, source, filter.String, mdl.String, estMethod.String, !filter.Valid, !mdl.Valid, !estMethod.Valid}
		b, ok := agg[k]
		if !ok {
			b = &Bucket{Day: day, Source: source, N: 0}
			if filter.Valid {
				v := filter.String
				b.FilterLevel = &v
			}
			if mdl.Valid {
				v := mdl.String
				b.Model = &v
			}
			if estMethod.Valid {
				v := estMethod.String
				b.EstimationMethod = &v
			}
			agg[k] = b
		}
		b.N++
		b.InputBytes += inB
		b.OutputBytes += outB
		b.SavedTokens += saved
	}
	out := make([]Bucket, 0, len(agg))
	for _, b := range agg {
		out = append(out, *b)
	}
	return out, ids, nil
}

// markFlushedChunk is the SQLite-friendly batch size for IN-clause updates.
// SQLite's default SQLITE_MAX_VARIABLE_NUMBER is 999 on older builds; 500
// is a comfortable margin that works on every modernc.org/sqlite version.
const markFlushedChunk = 500

// MarkFlushed sets flushed_at = now() for the given rows in chunked
// IN-clause batches. Replaces the per-id prepared-statement loop so N rows
// cost ceil(N/markFlushedChunk) round trips instead of N.
func (t *Tracker) MarkFlushed(ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	for start := 0; start < len(ids); start += markFlushedChunk {
		end := min(start+markFlushedChunk, len(ids))
		chunk := ids[start:end]
		placeholders := strings.TrimRight(strings.Repeat("?,", len(chunk)), ",")
		args := make([]any, len(chunk))
		for i, id := range chunk {
			args[i] = id
		}
		query := "UPDATE events SET flushed_at = datetime('now') WHERE id IN (" + placeholders + ")"
		if _, err := t.db.Exec(query, args...); err != nil {
			return err
		}
	}
	return nil
}

// HookSavedTokens is the event source written by the hook layer when it
// reports per-hook saved-token deltas back to the tracker.
const HookSavedTokens = "hook-saved-tokens"

// HookDeltaRow is one row of the per-hook delta report.
type HookDeltaRow struct {
	Hook        string
	N           int
	SavedTokens int64
}

// QueryHookDeltas returns per-hook saved-token deltas for events whose source
// is HookSavedTokens within the given lookback window. The `command` column
// identifies which hook produced the event.
func (t *Tracker) QueryHookDeltas(since string) ([]HookDeltaRow, error) {
	cutoff, err := parseSince(since)
	if err != nil {
		return nil, err
	}
	rows, err := t.db.Query(`
		SELECT COALESCE(command, '<unknown>') AS hook, COUNT(*) AS n, SUM(saved_tokens)
		FROM events
		WHERE source = ? AND ts >= ?
		GROUP BY hook ORDER BY n DESC
	`, HookSavedTokens, cutoff.UTC().Format(time.RFC3339))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []HookDeltaRow
	for rows.Next() {
		var r HookDeltaRow
		if err := rows.Scan(&r.Hook, &r.N, &r.SavedTokens); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func groupColumn(g string) (string, error) {
	switch g {
	case "source":
		return "source", nil
	case "command":
		return "command", nil
	case "filter":
		return "filter_level", nil
	case "model":
		return "model", nil
	case "session":
		return "session_id", nil
	case "method":
		return "estimation_method", nil
	default:
		return "", errors.New("unknown groupBy: " + g)
	}
}

func parseSince(s string) (time.Time, error) {
	if s == "" {
		s = "7d"
	}
	if len(s) < 2 {
		return time.Time{}, errors.New("bad since")
	}
	unit := s[len(s)-1]
	num := s[:len(s)-1]
	if unit == 'd' {
		dur, err := time.ParseDuration(num + "h")
		if err != nil {
			return time.Time{}, err
		}
		return time.Now().Add(-dur * 24), nil
	}
	d, err := time.ParseDuration(num + string(unit))
	if err != nil {
		return time.Time{}, err
	}
	return time.Now().Add(-d), nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
