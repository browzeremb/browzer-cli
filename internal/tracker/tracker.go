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
}

// Tracker owns the SQLite connection. Single writer, multiple readers.
type Tracker struct {
	db *sql.DB
	mu sync.Mutex
}

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
	if _, err := db.Exec(schemaSQL); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	return &Tracker{db: db}, nil
}

// Close releases the connection.
func (t *Tracker) Close() error { return t.db.Close() }

// Record inserts one event into SQLite. Retention cleanup is handled
// separately by Cleanup, which is called from a periodic goroutine in
// the daemon — not on every write — to avoid per-call DELETE latency in
// high-frequency hook-read loops.
func (t *Tracker) Record(e Event) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	_, err := t.db.Exec(`
		INSERT INTO events (ts, source, command, path_hash, input_bytes, output_bytes,
		                    saved_tokens, savings_pct, filter_level, exec_ms,
		                    workspace_id, session_id, model, filter_failed)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		e.TS, e.Source, e.Command, e.PathHash, e.InputBytes, e.OutputBytes,
		e.SavedTokens, e.SavingsPct, e.FilterLevel, e.ExecMs,
		e.WorkspaceID, e.SessionID, e.Model, boolToInt(e.FilterFailed),
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
func (t *Tracker) QueryAggregated(since, groupBy string) ([]AggregatedRow, error) {
	col, err := groupColumn(groupBy)
	if err != nil {
		return nil, err
	}
	cutoff, err := parseSince(since)
	if err != nil {
		return nil, err
	}
	q := fmt.Sprintf(`
		SELECT COALESCE(%s, '<unknown>') AS g, COUNT(*) AS n,
		       SUM(input_bytes), SUM(output_bytes), SUM(saved_tokens)
		FROM events WHERE ts >= ? GROUP BY g ORDER BY n DESC
	`, col)
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
}

// UnsentBuckets aggregates all rows where flushed_at IS NULL into per-(day,
// source, filter_level, model) buckets. Returns the buckets AND the row ids
// that contributed (to be passed to MarkFlushed after the POST succeeds).
func (t *Tracker) UnsentBuckets() ([]Bucket, []int64, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	rows, err := t.db.Query(`
		SELECT id, ts, source, filter_level, model, input_bytes, output_bytes, saved_tokens
		FROM events WHERE flushed_at IS NULL
	`)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = rows.Close() }()

	type key struct {
		day, source, filter, model string
		filterNull, modelNull      bool
	}
	agg := map[key]*Bucket{}
	var ids []int64
	for rows.Next() {
		var (
			id              int64
			ts, source      string
			filter, mdl     sql.NullString
			inB, outB, saved int64
		)
		if err := rows.Scan(&id, &ts, &source, &filter, &mdl, &inB, &outB, &saved); err != nil {
			return nil, nil, err
		}
		ids = append(ids, id)
		day := ts[:10] // YYYY-MM-DD
		k := key{day, source, filter.String, mdl.String, !filter.Valid, !mdl.Valid}
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

// MarkFlushed sets flushed_at = now() for the given rows.
func (t *Tracker) MarkFlushed(ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	tx, err := t.db.Begin()
	if err != nil {
		return err
	}
	stmt, err := tx.Prepare(`UPDATE events SET flushed_at = datetime('now') WHERE id = ?`)
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	defer func() { _ = stmt.Close() }()
	for _, id := range ids {
		if _, err := stmt.Exec(id); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	return tx.Commit()
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
