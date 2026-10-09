package store

// AttachmentStore filesystem backend (C4T2): filename sanitization,
// path-traversal containment, and save/open/delete round-trip. No
// database needed — the store package tests are pure filesystem.

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newTestAttachmentStore(t *testing.T) *FileAttachmentStore {
	t.Helper()
	st, err := NewFileAttachmentStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileAttachmentStore: %v", err)
	}
	return st
}

func TestSanitizeFilename(t *testing.T) {
	cases := []struct {
		in   string
		want string // exact match when non-empty; "" means "just assert safe"
	}{
		{"report.pdf", "report.pdf"},
		{"../../etc/passwd", "passwd"},
		{`..\..\windows\system32\drivers\etc\hosts`, "hosts"},
		{"/absolute/path/evil.txt", "evil.txt"},
		{"a/../../b.png", "b.png"},
		{"..", "file"},
		{"", "file"},
		{"   ", "file"},
		{"my photo (1).jpg", "my photo (1).jpg"},
		{"über-cool_ß.txt", "über-cool_ß.txt"},
		{"evil\"name;.html", "evilname.html"}, // disposition-safe
		{"noext", "noext"},
	}
	for _, tc := range cases {
		got := SanitizeFilename(tc.in)
		if tc.want != "" && got != tc.want {
			t.Errorf("SanitizeFilename(%q) = %q, want %q", tc.in, got, tc.want)
		}
		if strings.ContainsAny(got, `/\`) || strings.Contains(got, "..") ||
			strings.ContainsAny(got, "\";\x00") || strings.ContainsAny(got, "\x00\x01\x1f") {
			t.Errorf("SanitizeFilename(%q) = %q: not safe", tc.in, got)
		}
		if got == "" {
			t.Errorf("SanitizeFilename(%q) returned empty", tc.in)
		}
	}
}

// TestAttachmentStoreTraversal proves a hostile stored name can never
// escape the storage root, even if a caller bypasses SanitizeFilename.
func TestAttachmentStoreTraversal(t *testing.T) {
	root := t.TempDir()
	st, err := NewFileAttachmentStore(root)
	if err != nil {
		t.Fatalf("NewFileAttachmentStore: %v", err)
	}
	hostile := []string{
		"../../escape.txt",
		`..\..\escape.txt`,
		"/abs/escape.txt",
		"sub/../../escape.txt",
		"..",
	}
	for _, name := range hostile {
		if _, err := st.Save(name, strings.NewReader("x")); err == nil {
			t.Errorf("Save(%q): expected a traversal error, got nil", name)
		}
		if _, err := st.Open(name); err == nil {
			t.Errorf("Open(%q): expected a traversal error, got nil", name)
		}
		if err := st.Delete(name); err == nil {
			t.Errorf("Delete(%q): expected a traversal error, got nil", name)
		}
	}
	// Nothing escaped: the root holds no files and the parent is untouched.
	var left []string
	filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			left = append(left, p)
		}
		return nil
	})
	if len(left) != 0 {
		t.Errorf("traversal writes escaped the root: %v", left)
	}
	parent := filepath.Dir(root)
	if _, err := os.Stat(filepath.Join(parent, "escape.txt")); !os.IsNotExist(err) {
		t.Errorf("traversal file appeared in parent dir")
	}
}

func TestAttachmentStoreRoundTrip(t *testing.T) {
	st := newTestAttachmentStore(t)
	name := "9f2b1a3c-4d5e-6f70-81a2-b3c4d5e6f7089.png"
	want := []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0x00}

	n, err := st.Save(name, bytes.NewReader(want))
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if n != int64(len(want)) {
		t.Fatalf("Save wrote %d bytes, want %d", n, len(want))
	}

	rc, err := st.Open(name)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	got, err := io.ReadAll(rc)
	rc.Close()
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("round-trip mismatch: got %x, want %x", got, want)
	}

	if err := st.Delete(name); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := st.Open(name); err == nil {
		t.Fatal("Open after Delete: expected error, got nil")
	}
	// Deleting a missing name is a no-op, never an error.
	if err := st.Delete(name); err != nil {
		t.Fatalf("second Delete: %v", err)
	}
}

func TestStoredFilenameIsServerGenerated(t *testing.T) {
	// StoredName must be a uuid-shaped name with a sanitized extension —
	// never derived from attacker-controlled path segments.
	for _, in := range []string{"a.png", "../../b.PNG", `c\0d.JpG`, "e"} {
		got, err := StoredName(in)
		if err != nil {
			t.Fatalf("StoredName(%q): %v", in, err)
		}
		if strings.ContainsAny(got, `/\`) || strings.Contains(got, "..") {
			t.Fatalf("StoredName(%q) = %q: unsafe", in, got)
		}
	}
	// Uniqueness: 1000 names, no collisions.
	seen := map[string]bool{}
	for i := 0; i < 1000; i++ {
		n, err := StoredName("photo.png")
		if err != nil {
			t.Fatal(err)
		}
		if seen[n] {
			t.Fatalf("StoredName collision on %q", n)
		}
		seen[n] = true
	}
	got, err := StoredName("../../x.PNG")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(got, ".png") {
		t.Fatalf("extension not lowercased/sanitized: %q", got)
	}
}
