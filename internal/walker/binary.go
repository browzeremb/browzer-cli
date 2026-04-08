package walker

import "os"

// binaryProbeBytes is the number of leading bytes inspected.
const binaryProbeBytes = 512

// binaryNonprintRatio: above this ratio of non-printable bytes the file
// is treated as binary.
const binaryNonprintRatio = 0.3

// IsBinaryFile probes the first 512 bytes of absPath and returns true
// if the file looks binary.
//
// Heuristic (mirrors src/lib/walker.ts:isBinaryFile):
//   - any null byte → binary
//   - non-printable byte ratio > binaryNonprintRatio → binary
//
// Returns true on read errors so corrupt files don't crash the walker.
func IsBinaryFile(absPath string) bool {
	f, err := os.Open(absPath)
	if err != nil {
		return true
	}
	defer f.Close()

	buf := make([]byte, binaryProbeBytes)
	n, err := f.Read(buf)
	if err != nil && n == 0 {
		// Empty or unreadable — treat empty as text (matches Node).
		return false
	}
	if n == 0 {
		return false
	}

	nonPrint := 0
	for i := 0; i < n; i++ {
		b := buf[i]
		if b == 0 {
			return true
		}
		// Printable ASCII (0x20-0x7E) plus common whitespace
		// (\t, \n, \v, \f, \r).
		printable := (b >= 0x20 && b <= 0x7e) ||
			b == 0x09 || b == 0x0a || b == 0x0b ||
			b == 0x0c || b == 0x0d
		if !printable {
			nonPrint++
		}
	}
	return float64(nonPrint)/float64(n) > binaryNonprintRatio
}
