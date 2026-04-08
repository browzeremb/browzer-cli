package walker

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// DocFile is a single Markdown document found by WalkDocs.
type DocFile struct {
	// RelativePath is forward-slash relative to repo root.
	RelativePath string
	// AbsolutePath is the local filesystem path.
	AbsolutePath string
	// SHA256 is the lowercase hex digest of the raw bytes.
	SHA256 string
	// Size is the file size in bytes.
	Size int64
}

var docExtensions = map[string]struct{}{
	".md":  {},
	".mdx": {},
}

// WalkDocs walks rootPath and returns every non-ignored, non-sensitive
// Markdown document with its content hash. SHA-256 is computed during
// the read so we don't pay double I/O.
//
// Same hardening invariants as WalkRepo (symlink skip, MaxDepth, binary
// detection, sensitive-before-stat).
func WalkDocs(rootPath string) ([]DocFile, error) {
	matcher, err := loadRootIgnore(rootPath)
	if err != nil {
		return nil, err
	}
	var out []DocFile
	if err := walkDocsRec(rootPath, "", matcher, &out, 0); err != nil {
		return nil, err
	}
	return out, nil
}

func walkDocsRec(absDir, relDir string, matcher *ignoreMatcher, out *[]DocFile, depth int) error {
	if depth > MaxDepth {
		fmt.Fprintf(os.Stderr, "Warning: max directory depth %d exceeded at %q — stopping recursion.\n", MaxDepth, relDir)
		return nil
	}

	entries, err := os.ReadDir(absDir)
	if err != nil {
		return nil
	}

	if relDir != "" {
		if data, err := os.ReadFile(filepath.Join(absDir, ".gitignore")); err == nil {
			matcher.add(RerootGitignore(string(data), relDir))
		}
	}

	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		mode := entry.Type()
		if mode&os.ModeSymlink != 0 {
			continue
		}

		var relPath string
		if relDir == "" {
			relPath = name
		} else {
			relPath = relDir + "/" + name
		}

		if entry.IsDir() {
			if _, skip := DefaultIgnoreDirs[name]; skip {
				continue
			}
			if matcher.matches(relPath + "/") {
				continue
			}
			if err := walkDocsRec(filepath.Join(absDir, name), relPath, matcher, out, depth+1); err != nil {
				return err
			}
			continue
		}

		if !mode.IsRegular() {
			continue
		}
		if IsSensitive(relPath) {
			continue
		}
		ext := strings.ToLower(filepath.Ext(name))
		if _, ok := docExtensions[ext]; !ok {
			continue
		}
		if matcher.matches(relPath) {
			continue
		}

		absPath := filepath.Join(absDir, name)
		// I-11: paranoia — `.md` extension on a binary blob is either
		// a corrupt file or a poison attempt; either way, skip.
		if IsBinaryFile(absPath) {
			continue
		}

		hash, size, ok := hashFile(absPath)
		if !ok {
			continue
		}

		*out = append(*out, DocFile{
			RelativePath: relPath,
			AbsolutePath: absPath,
			SHA256:       hash,
			Size:         size,
		})
	}
	return nil
}

// hashFile streams absPath through sha256 and returns the hex digest +
// the byte count. Returns false on read error.
func hashFile(absPath string) (string, int64, bool) {
	f, err := os.Open(absPath)
	if err != nil {
		return "", 0, false
	}
	defer func() { _ = f.Close() }()

	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, false
	}
	return hex.EncodeToString(h.Sum(nil)), n, true
}
