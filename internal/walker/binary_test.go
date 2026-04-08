package walker

import (
	"os"
	"path/filepath"
	"testing"
)

// TestIsBinaryFile_TinyFiles asserts that small files don't get
// misclassified as binary by the non-printable ratio heuristic. Before
// the binaryRatioMinSample short-circuit, a 1-byte file containing a
// single high-bit byte would trip the 0.3 ratio (1.0 > 0.3) and be
// flagged as binary; tiny UTF-8 source files would suffer the same.
func TestIsBinaryFile_TinyFiles(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		name, content string
		wantBinary    bool
	}{
		{"single-byte-text.txt", "a", false},
		{"few-bytes-utf8.txt", "olá", false},
		{"empty.txt", "", false},
		// Null byte should always flag as binary regardless of size.
		{"tiny-with-null.bin", "a\x00b", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			path := filepath.Join(dir, c.name)
			if err := os.WriteFile(path, []byte(c.content), 0o644); err != nil {
				t.Fatal(err)
			}
			if got := IsBinaryFile(path); got != c.wantBinary {
				t.Fatalf("IsBinaryFile(%q) = %v, want %v", c.name, got, c.wantBinary)
			}
		})
	}
}

// TestIsBinaryFile_LargeBinarySample makes sure the ratio heuristic
// still flags a clearly-binary blob once it crosses the minimum
// sample size — guard against the short-circuit being too lenient.
func TestIsBinaryFile_LargeBinarySample(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "blob.bin")
	// 64 bytes of high-bit garbage, no nulls — well past the
	// binaryRatioMinSample threshold, well above the 0.3 ratio.
	buf := make([]byte, 64)
	for i := range buf {
		buf[i] = 0xFE
	}
	if err := os.WriteFile(path, buf, 0o644); err != nil {
		t.Fatal(err)
	}
	if !IsBinaryFile(path) {
		t.Fatalf("expected blob to be flagged as binary")
	}
}
