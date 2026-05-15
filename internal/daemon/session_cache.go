package daemon

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// transcriptRow is the typed shape extractModelFromTranscript scans
// each JSONL line into. Only the `model` and `message.model` fields are
// needed; everything else round-trips via the json.Decoder's silent
// drop of unknown keys. Replaces the `map[string]any` decode path,
// which on transcript-sized payloads (hundreds of mixed-shape rows)
// was the highest-allocation surface in the daemon's session bootstrap.
type transcriptRow struct {
	Model   string `json:"model"`
	Message struct {
		Model string `json:"model"`
	} `json:"message"`
}

// transcriptModelToken is the substring every JSONL line MUST carry for
// extractModelFromTranscript to bother running json.Unmarshal — lines
// without it definitionally have no model field at top-level or under
// message, so the typed decode would just return zero values.
var transcriptModelToken = []byte(`"model"`)

// SessionCache maps a Claude Code session id to the model in use, by
// scanning the transcript JSONL once on Register.
type SessionCache struct {
	pathFor func(sessionID string) string
	mu      sync.RWMutex
	cache   map[string]*string // sessionID → model (nil = scanned, no model found)
}

type sessionCacheFile struct {
	SessionID  string  `json:"sessionId"`
	Model      *string `json:"model"`
	CapturedAt string  `json:"capturedAt"`
}

func NewSessionCache(pathFor func(string) string) *SessionCache {
	return &SessionCache{pathFor: pathFor, cache: make(map[string]*string)}
}

// Register reads the transcript at transcriptPath, extracts the model,
// caches it in memory + on disk, and returns it.
func (c *SessionCache) Register(sessionID, transcriptPath string) (*string, error) {
	// I11: Validate that transcriptPath is within an expected directory to
	// prevent a malicious process from making the daemon open arbitrary files.
	home, _ := os.UserHomeDir()
	allowedPrefixes := []string{
		filepath.Join(home, ".claude"),
		filepath.Clean(os.TempDir()),
	}
	clean := filepath.Clean(transcriptPath)
	allowed := false
	for _, prefix := range allowedPrefixes {
		if strings.HasPrefix(clean, prefix+string(filepath.Separator)) {
			allowed = true
			break
		}
	}
	if !allowed {
		return nil, fmt.Errorf("transcript_path_outside_allowed: %s", clean)
	}

	model, err := extractModelFromTranscript(clean)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	c.mu.Lock()
	c.cache[sessionID] = model
	c.mu.Unlock()

	// Best-effort persistence (cache survives daemon restart).
	_ = persistSession(c.pathFor(sessionID), sessionID, model)
	return model, nil
}

// Get returns the cached model for a session id. ok=false means the
// session was never registered.
func (c *SessionCache) Get(sessionID string) (*string, bool) {
	c.mu.RLock()
	m, ok := c.cache[sessionID]
	c.mu.RUnlock()
	if ok {
		return m, true
	}
	// Try disk.
	body, err := os.ReadFile(c.pathFor(sessionID))
	if err != nil {
		return nil, false
	}
	var f sessionCacheFile
	if err := json.Unmarshal(body, &f); err != nil {
		return nil, false
	}
	c.mu.Lock()
	c.cache[sessionID] = f.Model
	c.mu.Unlock()
	return f.Model, true
}

func extractModelFromTranscript(path string) (*string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	scn := bufio.NewScanner(f)
	scn.Buffer(make([]byte, 64*1024), 1024*1024)
	for scn.Scan() {
		line := scn.Bytes()
		// Skip-fast: lines without the "model" substring cannot carry
		// either top-level or nested model values; json.Unmarshal would
		// just return zero values. The substring check is O(n) byte-
		// comparison vs the full JSON parse + map[string]any allocation
		// that previously ran on every line.
		if !bytes.Contains(line, transcriptModelToken) {
			continue
		}
		var row transcriptRow
		if json.Unmarshal(line, &row) != nil {
			continue
		}
		if row.Model != "" {
			m := row.Model
			return &m, nil
		}
		// Some transcript shapes nest model under message.model.
		if row.Message.Model != "" {
			m := row.Message.Model
			return &m, nil
		}
	}
	return nil, scn.Err()
}

func persistSession(path, sessionID string, model *string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	body, err := json.Marshal(sessionCacheFile{
		SessionID:  sessionID,
		Model:      model,
		CapturedAt: time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		return err
	}
	return os.WriteFile(path, body, 0o600)
}
