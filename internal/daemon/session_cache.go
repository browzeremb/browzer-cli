package daemon

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

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
	model, err := extractModelFromTranscript(transcriptPath)
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
	defer f.Close()
	scn := bufio.NewScanner(f)
	scn.Buffer(make([]byte, 64*1024), 1024*1024)
	for scn.Scan() {
		var row map[string]any
		if json.Unmarshal(scn.Bytes(), &row) != nil {
			continue
		}
		if m, ok := row["model"].(string); ok && m != "" {
			return &m, nil
		}
		// Some transcript shapes nest model under "message.model" or "session.model"
		if msg, ok := row["message"].(map[string]any); ok {
			if m, ok := msg["model"].(string); ok && m != "" {
				return &m, nil
			}
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
