package walker

import "testing"

func TestClassifyFile(t *testing.T) {
	cases := []struct {
		path string
		want FileClass
	}{
		{"src/main.go", ClassCode},
		{"README.md", ClassDoc},
		{"docs/intro.mdx", ClassDoc},
		{"paper.pdf", ClassDoc},
		{"notes.txt", ClassDoc},
		{"manual.rst", ClassDoc},
		{"UPPER.MD", ClassDoc},              // case-insensitive
		{"path/to/thing.tsx", ClassCode},
		{"no-extension", ClassCode},
		{"archive.tar.gz", ClassCode},       // .gz not a doc ext
	}
	for _, tc := range cases {
		if got := ClassifyFile(tc.path); got != tc.want {
			t.Errorf("ClassifyFile(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}

func TestIsDocExtension(t *testing.T) {
	for _, ext := range []string{".md", "md", ".MD", ".pdf", "PDF"} {
		if !IsDocExtension(ext) {
			t.Errorf("IsDocExtension(%q) = false, want true", ext)
		}
	}
	for _, ext := range []string{".go", "tsx", "", ".tar"} {
		if IsDocExtension(ext) {
			t.Errorf("IsDocExtension(%q) = true, want false", ext)
		}
	}
}

func TestDocExtensionsSorted(t *testing.T) {
	exts := DocExtensions()
	if len(exts) == 0 {
		t.Fatal("DocExtensions() empty")
	}
	for i := 1; i < len(exts); i++ {
		if exts[i-1] >= exts[i] {
			t.Errorf("DocExtensions() not sorted: %v", exts)
			break
		}
	}
}
