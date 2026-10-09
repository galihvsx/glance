package store

// Issue attachment file storage (C4T2).
//
// The attachments table (migrations/000021) holds metadata; the bytes
// live on the local filesystem under <data-dir>/attachments. The
// storage root is a server-side secret in the same sense as any other
// path: client-supplied filenames are NEVER used as paths.
//
// Two layers of defense:
//  1. SanitizeFilename reduces a client filename to a display-safe base
//     name (no separators, no traversal, no quote/semicolon/control
//     chars so it is safe inside a Content-Disposition header).
//  2. StoredName generates the actual on-disk name: crypto/rand UUID +
//     a sanitized, lowercased extension. resolvePath then re-verifies
//     containment in the root, so even a caller that bypasses
//     SanitizeFilename cannot escape (Save/Open/Delete fail closed).

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// AttachmentStore persists attachment bytes keyed by name. name is a
// server-generated (StoredName) or sanitized (SanitizeFilename) name —
// never a raw client path. Implementations must fail closed on names
// that would escape the storage root.
type AttachmentStore interface {
	// Save writes r under name, returning the bytes written.
	Save(name string, r io.Reader) (int64, error)
	// Open returns a reader for the bytes stored under name.
	Open(name string) (io.ReadCloser, error)
	// Delete removes name; a missing name is a no-op.
	Delete(name string) error
}

// ErrPathTraversal is returned when a name would resolve outside the
// storage root. The operation is refused, nothing is touched.
var ErrPathTraversal = errors.New("store: attachment name escapes storage root")

// FileAttachmentStore is a filesystem AttachmentStore rooted at dir.
type FileAttachmentStore struct {
	root string
}

// NewFileAttachmentStore creates the storage root (0700 — attachment
// bytes are workspace-private) and returns a store for it.
func NewFileAttachmentStore(dir string) (*FileAttachmentStore, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("store: resolve attachment dir: %w", err)
	}
	if err := os.MkdirAll(abs, 0o700); err != nil {
		return nil, fmt.Errorf("store: create attachment dir: %w", err)
	}
	return &FileAttachmentStore{root: abs}, nil
}

// resolvePath joins name onto the root and refuses anything that does
// not stay strictly inside the root. The check runs on the cleaned
// absolute path, so "..", absolute names, and separator tricks all fail.
func (s *FileAttachmentStore) resolvePath(name string) (string, error) {
	if name == "" || name == "." || name == ".." {
		return "", ErrPathTraversal
	}
	p := filepath.Join(s.root, name)
	// Join cleans the path; a name that escapes must not start with the
	// root prefix (root itself + separator, so "root-evil" can't match).
	if p != s.root && !strings.HasPrefix(p, s.root+string(os.PathSeparator)) {
		return "", ErrPathTraversal
	}
	if p == s.root {
		return "", ErrPathTraversal
	}
	// Belt and suspenders: after cleaning, the final element must be the
	// name itself (no subdirectories — stored names are always flat).
	// Backslash is rejected on every platform: it is a separator on
	// Windows, so a name containing one is not portable-safe even when
	// it happens to be a legal flat name on Linux.
	if filepath.Base(p) != name || strings.ContainsAny(name, `/\`) {
		return "", ErrPathTraversal
	}
	return p, nil
}

// Save writes r to the file named name inside the root.
func (s *FileAttachmentStore) Save(name string, r io.Reader) (int64, error) {
	p, err := s.resolvePath(name)
	if err != nil {
		return 0, err
	}
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return 0, fmt.Errorf("store: save attachment: %w", err)
	}
	n, werr := io.Copy(f, r)
	cerr := f.Close()
	if werr != nil {
		os.Remove(p) // don't leave a truncated file behind
		return n, fmt.Errorf("store: save attachment: %w", werr)
	}
	if cerr != nil {
		return n, fmt.Errorf("store: save attachment: %w", cerr)
	}
	return n, nil
}

// Open returns a reader for the stored bytes. Callers close it.
func (s *FileAttachmentStore) Open(name string) (io.ReadCloser, error) {
	p, err := s.resolvePath(name)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(p)
	if err != nil {
		return nil, fmt.Errorf("store: open attachment: %w", err)
	}
	return f, nil
}

// Delete removes the stored file; a missing file is a no-op.
func (s *FileAttachmentStore) Delete(name string) error {
	p, err := s.resolvePath(name)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("store: delete attachment: %w", err)
	}
	return nil
}

// SanitizeFilename reduces a client-supplied filename to a display-safe
// base name: path separators (both flavors), traversal segments, NUL and
// other control characters, quotes and semicolons (Content-Disposition
// metacharacters) are stripped. The result is never empty and never
// contains a path separator. It is for DISPLAY and Content-Disposition
// only — never used as a storage path (see StoredName).
func SanitizeFilename(name string) string {
	// Drop everything up to the last separator of either flavor.
	name = name[strings.LastIndex(name, "/")+1:]
	name = name[strings.LastIndex(name, "\\")+1:]
	var b strings.Builder
	for _, r := range name {
		switch {
		case r == 0 || r < 0x20 || r == 0x7f:
			// control chars, NUL: drop
		case r == '"' || r == ';' || r == '`':
			// Content-Disposition / shell metacharacters: drop
		default:
			b.WriteRune(r)
		}
	}
	out := strings.Trim(b.String(), " .")
	if out == "" || out == "." || out == ".." {
		return "file"
	}
	return out
}

// StoredName returns the on-disk name for an upload: a crypto/rand UUID
// (8-4-4-4-12 hex, stdlib only — no new dependency) plus the
// sanitized, lowercased extension of the client filename. The uuid makes
// the name unguessable and collision-proof; the extension survives only
// as a lowercase [a-z0-9] suffix so tooling can still recognize the
// file kind.
func StoredName(clientFilename string) (string, error) {
	ext := sanitizedExt(clientFilename)
	var rnd [16]byte
	if _, err := rand.Read(rnd[:]); err != nil {
		// crypto/rand failing means the OS entropy source is broken;
		// refuse to mint a guessable name and surface the failure.
		return "", fmt.Errorf("store: generate attachment name: %w", err)
	}
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		rnd[0:4], rnd[4:6], rnd[6:8], rnd[8:10], rnd[10:16]) + ext, nil
}

// sanitizedExt extracts the extension of name, lowercased and restricted
// to [a-z0-9], capped at 10 chars. Anything else yields "".
func sanitizedExt(name string) string {
	ext := strings.ToLower(filepath.Ext(SanitizeFilename(name)))
	if ext == "" {
		return ""
	}
	var b strings.Builder
	b.WriteByte('.')
	for _, r := range ext[1:] {
		if b.Len() > 10 {
			break
		}
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	if b.Len() == 1 {
		return ""
	}
	return b.String()
}
