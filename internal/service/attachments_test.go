package service

// Issue attachments (C4T2): upload/list/download/delete round-trip,
// per-file size limit, traversal containment, and tenancy (attachment
// rows are always scoped to the issue they were uploaded to).

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"glance/internal/store"

	"github.com/jackc/pgx/v5/pgxpool"
)

func setupAttachmentTest(t *testing.T) (context.Context, *pgxpool.Pool, *store.FileAttachmentStore, string, string, string, string) {
	t.Helper()
	ctx := context.Background()
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	st, err := store.NewFileAttachmentStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileAttachmentStore: %v", err)
	}
	actor := createTestUser(t, pool, uniqueTestEmail("att"))
	slug := uniqueTestSlug("att-ws")
	createTestWorkspace(t, pool, "Attachment Co", slug, actor)
	ident := uniqueTestIdentifier()
	createTestProject(t, pool, slug, actor, "Eng", ident)
	iss := createTestIssue(t, pool, slug, ident, actor, "With files")
	return ctx, pool, st, slug, ident, actor, iss.ID
}

func uploadBytes(t *testing.T, ctx context.Context, pool *pgxpool.Pool, st *store.FileAttachmentStore,
	slug, ident, issueID, actor, filename, contentType string, data []byte, maxBytes int64) *Attachment {
	t.Helper()
	a, err := UploadAttachment(ctx, pool, st, slug, ident, issueID, actor, UploadInput{
		Filename:    filename,
		ContentType: contentType,
		Data:        bytes.NewReader(data),
		MaxBytes:    maxBytes,
	})
	if err != nil {
		t.Fatalf("UploadAttachment(%q): %v", filename, err)
	}
	return a
}

func TestAttachmentRoundTrip(t *testing.T) {
	ctx, pool, st, slug, ident, actor, issueID := setupAttachmentTest(t)
	data := []byte("%PDF-1.4 fake bytes")

	a := uploadBytes(t, ctx, pool, st, slug, ident, issueID, actor, "report.pdf", "application/pdf", data, 25<<20)
	if a.Filename != "report.pdf" {
		t.Errorf("Filename = %q, want original client name", a.Filename)
	}
	if a.SizeBytes != int64(len(data)) {
		t.Errorf("SizeBytes = %d, want %d", a.SizeBytes, len(data))
	}
	if a.UploadedBy.ID != actor {
		t.Errorf("UploadedBy.ID = %s, want %s", a.UploadedBy.ID, actor)
	}
	if strings.ContainsAny(a.StoredPath, `/\`) || strings.Contains(a.StoredPath, "..") {
		t.Errorf("StoredPath %q is not a flat server-generated name", a.StoredPath)
	}

	// List shows it.
	list, err := ListAttachments(ctx, pool, slug, ident, issueID, actor)
	if err != nil {
		t.Fatalf("ListAttachments: %v", err)
	}
	if len(list) != 1 || list[0].ID != a.ID {
		t.Fatalf("ListAttachments = %d items, want the one upload", len(list))
	}

	// Get by id (download path metadata).
	got, err := GetAttachment(ctx, pool, slug, ident, issueID, a.ID, actor)
	if err != nil {
		t.Fatalf("GetAttachment: %v", err)
	}
	if got.StoredPath != a.StoredPath {
		t.Fatalf("StoredPath mismatch after re-read")
	}

	// The bytes on disk match.
	rc, err := st.Open(got.StoredPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	back, err := io.ReadAll(rc)
	rc.Close()
	if err != nil || !bytes.Equal(back, data) {
		t.Fatalf("stored bytes mismatch: err=%v", err)
	}

	// Delete removes the row AND the bytes.
	if err := DeleteAttachment(ctx, pool, st, slug, ident, issueID, a.ID, actor); err != nil {
		t.Fatalf("DeleteAttachment: %v", err)
	}
	if _, err := GetAttachment(ctx, pool, slug, ident, issueID, a.ID, actor); !errors.Is(err, ErrAttachmentNotFound) {
		t.Fatalf("GetAttachment after delete = %v, want ErrAttachmentNotFound", err)
	}
	if _, err := st.Open(a.StoredPath); err == nil {
		t.Fatal("attachment bytes still on disk after delete")
	}
	list, err = ListAttachments(ctx, pool, slug, ident, issueID, actor)
	if err != nil {
		t.Fatalf("ListAttachments: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("ListAttachments after delete = %d items, want 0", len(list))
	}
}

func TestAttachmentSizeLimit(t *testing.T) {
	ctx, pool, st, slug, ident, actor, issueID := setupAttachmentTest(t)

	// Exactly at the limit passes.
	atLimit := uploadBytes(t, ctx, pool, st, slug, ident, issueID, actor,
		"limit.bin", "application/octet-stream", bytes.Repeat([]byte("a"), 64), 64)
	if atLimit.SizeBytes != 64 {
		t.Fatalf("SizeBytes = %d, want 64", atLimit.SizeBytes)
	}

	// One byte over fails — and leaves no row and no orphaned bytes.
	_, err := UploadAttachment(ctx, pool, st, slug, ident, issueID, actor, UploadInput{
		Filename:    "too-big.bin",
		ContentType: "application/octet-stream",
		Data:        bytes.NewReader(bytes.Repeat([]byte("a"), 65)),
		MaxBytes:    64,
	})
	if !errors.Is(err, ErrAttachmentTooLarge) {
		t.Fatalf("oversize upload = %v, want ErrAttachmentTooLarge", err)
	}
	list, err := ListAttachments(ctx, pool, slug, ident, issueID, actor)
	if err != nil {
		t.Fatalf("ListAttachments: %v", err)
	}
	for _, a := range list {
		if a.Filename == "too-big.bin" {
			t.Fatal("oversize upload left a row behind")
		}
	}
	// Streamed (unknown-size) uploads are capped too, not just
	// pre-sized ones.
	_, err = UploadAttachment(ctx, pool, st, slug, ident, issueID, actor, UploadInput{
		Filename:    "stream.bin",
		ContentType: "application/octet-stream",
		Data:        io.MultiReader(bytes.NewReader(bytes.Repeat([]byte("a"), 65))),
		MaxBytes:    64,
	})
	if !errors.Is(err, ErrAttachmentTooLarge) {
		t.Fatalf("oversize streamed upload = %v, want ErrAttachmentTooLarge", err)
	}
}

func TestAttachmentTraversalFilename(t *testing.T) {
	ctx, pool, st, slug, ident, actor, issueID := setupAttachmentTest(t)

	// A hostile client filename must never influence the stored path,
	// and the display name must be separator-free.
	a := uploadBytes(t, ctx, pool, st, slug, ident, issueID, actor,
		"../../etc/passwd", "text/plain", []byte("x"), 25<<20)
	if strings.ContainsAny(a.Filename, `/\`) || strings.Contains(a.Filename, "..") {
		t.Fatalf("display filename not sanitized: %q", a.Filename)
	}
	if strings.ContainsAny(a.StoredPath, `/\`) || strings.Contains(a.StoredPath, "..") {
		t.Fatalf("stored path not server-generated: %q", a.StoredPath)
	}
	if err := DeleteAttachment(ctx, pool, st, slug, ident, issueID, a.ID, actor); err != nil {
		t.Fatalf("DeleteAttachment: %v", err)
	}
}

func TestAttachmentTenancy(t *testing.T) {
	ctx, pool, st, slug, ident, actor, issueID := setupAttachmentTest(t)
	other := createTestIssue(t, pool, slug, ident, actor, "Other issue")

	a := uploadBytes(t, ctx, pool, st, slug, ident, issueID, actor,
		"a.txt", "text/plain", []byte("x"), 25<<20)

	// The attachment is invisible through a different issue's scope.
	if _, err := GetAttachment(ctx, pool, slug, ident, other.ID, a.ID, actor); !errors.Is(err, ErrAttachmentNotFound) {
		t.Fatalf("cross-issue Get = %v, want ErrAttachmentNotFound", err)
	}
	if err := DeleteAttachment(ctx, pool, st, slug, ident, other.ID, a.ID, actor); !errors.Is(err, ErrAttachmentNotFound) {
		t.Fatalf("cross-issue Delete = %v, want ErrAttachmentNotFound", err)
	}
	// And a missing attachment 404s rather than 500ing.
	if _, err := GetAttachment(ctx, pool, slug, ident, issueID, "00000000-0000-0000-0000-000000000000", actor); !errors.Is(err, ErrAttachmentNotFound) {
		t.Fatalf("missing Get = %v, want ErrAttachmentNotFound", err)
	}
}

func TestAttachmentValidation(t *testing.T) {
	ctx, pool, st, slug, ident, actor, issueID := setupAttachmentTest(t)

	// Empty filename is a 400-class error, not a stored row.
	_, err := UploadAttachment(ctx, pool, st, slug, ident, issueID, actor, UploadInput{
		Filename:    "",
		ContentType: "text/plain",
		Data:        bytes.NewReader([]byte("x")),
		MaxBytes:    25 << 20,
	})
	if !errors.Is(err, ErrInvalidAttachment) {
		t.Fatalf("empty filename upload = %v, want ErrInvalidAttachment", err)
	}

	// Nil data is rejected before touching the store.
	_, err = UploadAttachment(ctx, pool, st, slug, ident, issueID, actor, UploadInput{
		Filename:    "x.txt",
		ContentType: "text/plain",
		Data:        nil,
		MaxBytes:    25 << 20,
	})
	if !errors.Is(err, ErrInvalidAttachment) {
		t.Fatalf("nil data upload = %v, want ErrInvalidAttachment", err)
	}
}
