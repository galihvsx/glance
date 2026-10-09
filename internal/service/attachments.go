package service

// Issue attachments (C4T2): metadata in Postgres (migrations/000021),
// bytes in an AttachmentStore (filesystem, <data-dir>/attachments).
//
// Conventions (mirror the issue satellites):
//   - Tenancy: every op resolves workspace membership + project, then the
//     live issue, via resolveSatelliteIssue. A soft-deleted issue 404s.
//   - Roles: any member (guest 5+) may read; mutations need member (15)+
//     via requireSatelliteWriter.
//   - The stored filename is server-generated (store.StoredName: uuid +
//     sanitized extension) — the client filename survives only as a
//     sanitized DISPLAY name (store.SanitizeFilename), never as a path.
//   - Content type: sniff the first 512 bytes (http.DetectContentType);
//     when the sniff is inconclusive (application/octet-stream) fall back
//     to the client-declared type (parsed, normalized); empty/invalid →
//     application/octet-stream. The download handler — not this layer —
//     decides inline vs. attachment serving from the stored type.
//   - Size is capped per upload: the stream is wrapped in an
//     io.LimitedReader(maxBytes+1), so unknown-size streams cannot dodge
//     the cap. Over-limit uploads are rejected BEFORE any row is written
//     and the partial file is removed, so a rejected upload leaves
//     neither a row nor orphaned bytes.
//   - Delete removes the row first, then the bytes (best effort): a
//     failed byte-delete leaves an orphaned file, which is the safer
//     failure mode versus a row pointing at missing bytes.

import (
	"bytes"
	"context"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"
	"time"

	"glance/internal/store"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	// ErrAttachmentNotFound is returned for a missing attachment id, or
	// one that belongs to a different issue. The handler maps it to 404.
	ErrAttachmentNotFound = errors.New("service: attachment not found")
	// ErrAttachmentTooLarge is returned when an upload exceeds the
	// per-file cap. The handler maps it to 413.
	ErrAttachmentTooLarge = errors.New("service: attachment exceeds size limit")
	// ErrInvalidAttachment is returned for a malformed upload (empty
	// filename, nil data, non-positive cap). The handler maps it to 400.
	ErrInvalidAttachment = errors.New("service: invalid attachment")
)

// Attachment is one uploaded file's metadata. StoredPath is the
// server-generated on-disk name; it is never serialized to clients
// (the download endpoint resolves bytes server-side).
type Attachment struct {
	ID          string    `json:"id"`
	IssueID     string    `json:"issue_id"`
	Filename    string    `json:"filename"`
	ContentType string    `json:"content_type"`
	SizeBytes   int64     `json:"size_bytes"`
	StoredPath  string    `json:"-"`
	UploadedBy  ActorRef  `json:"uploaded_by"`
	CreatedAt   time.Time `json:"created_at"`
}

const attachmentColumns = `a.id::text, a.issue_id::text, a.filename,
	a.content_type, a.size_bytes, a.stored_path,
	a.uploaded_by::text, u.name, u.email, a.created_at`

func scanAttachment(row pgx.Row) (*Attachment, error) {
	var a Attachment
	err := row.Scan(&a.ID, &a.IssueID, &a.Filename,
		&a.ContentType, &a.SizeBytes, &a.StoredPath,
		&a.UploadedBy.ID, &a.UploadedBy.Name, &a.UploadedBy.Email,
		&a.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &a, nil
}

// UploadInput is one file upload. Data may be an unknown-length stream;
// MaxBytes is the per-file cap enforced by UploadAttachment.
type UploadInput struct {
	Filename    string // client filename (display only, sanitized)
	ContentType string // client-declared media type (fallback only)
	Data        io.Reader
	MaxBytes    int64
}

// UploadAttachment stores one file on the issue. Member (15)+. Returns
// the metadata row; the bytes are in the store under a server-generated
// name.
func UploadAttachment(ctx context.Context, pool *pgxpool.Pool, st store.AttachmentStore,
	wsSlug, identifier, issueID, actorID string, in UploadInput) (*Attachment, error) {
	if _, _, role, err := resolveSatelliteIssue(ctx, pool, wsSlug, identifier, issueID, actorID); err != nil {
		return nil, err
	} else if err := requireSatelliteWriter(role); err != nil {
		return nil, err
	}
	if strings.TrimSpace(in.Filename) == "" || in.Data == nil || in.MaxBytes < 1 {
		return nil, ErrInvalidAttachment
	}
	displayName := store.SanitizeFilename(in.Filename)
	storedName, err := store.StoredName(displayName)
	if err != nil {
		return nil, err
	}

	// Sniff the head for the content type, then stream the whole body
	// (head + rest) through the size-capped limiter into the store.
	head := make([]byte, 512)
	n, rerr := io.ReadFull(in.Data, head)
	if rerr != nil && rerr != io.EOF && rerr != io.ErrUnexpectedEOF {
		return nil, rerr
	}
	head = head[:n]
	body := io.MultiReader(bytes.NewReader(head), in.Data)
	contentType := sniffContentType(head, in.ContentType)

	limited := &io.LimitedReader{R: body, N: in.MaxBytes + 1}
	written, err := st.Save(storedName, limited)
	if err != nil {
		return nil, err
	}
	if limited.N == 0 {
		// The stream had more than MaxBytes bytes: reject and remove the
		// partial file so no orphaned bytes (and no row) remain.
		_ = st.Delete(storedName)
		return nil, ErrAttachmentTooLarge
	}

	var a Attachment
	err = pool.QueryRow(ctx,
		`INSERT INTO attachments
		 (issue_id, filename, content_type, size_bytes, stored_path, uploaded_by)
		 VALUES ($1::uuid, $2, $3, $4, $5, $6::uuid)
		 RETURNING id::text, issue_id::text, filename, content_type,
		           size_bytes, stored_path, uploaded_by::text, created_at`,
		normalizeIssueID(issueID), displayName, contentType, written,
		storedName, actorID).Scan(
		&a.ID, &a.IssueID, &a.Filename, &a.ContentType,
		&a.SizeBytes, &a.StoredPath, &a.UploadedBy.ID, &a.CreatedAt)
	if err != nil {
		// Row write failed: remove the bytes so the upload leaves
		// nothing behind.
		_ = st.Delete(storedName)
		return nil, err
	}
	// Best-effort uploader label (the row is already committed; a missing
	// user row must not fail the upload response).
	_ = pool.QueryRow(ctx, `SELECT name, email FROM users WHERE id = $1::uuid`,
		actorID).Scan(&a.UploadedBy.Name, &a.UploadedBy.Email)
	return &a, nil
}

// sniffContentType returns the media type to store: the sniffed type
// when conclusive, else the client-declared type (parsed/normalized),
// else application/octet-stream.
func sniffContentType(head []byte, clientDeclared string) string {
	if ct := http.DetectContentType(head); ct != "application/octet-stream" {
		return ct
	}
	if mt, _, err := mime.ParseMediaType(strings.TrimSpace(clientDeclared)); err == nil && mt != "" {
		return mt
	}
	return "application/octet-stream"
}

// ListAttachments returns the issue's attachments, newest first. Any
// workspace member (guest 5+) may read.
func ListAttachments(ctx context.Context, pool *pgxpool.Pool,
	wsSlug, identifier, issueID, actorID string) ([]Attachment, error) {
	if _, _, _, err := resolveSatelliteIssue(ctx, pool, wsSlug, identifier, issueID, actorID); err != nil {
		return nil, err
	}
	rows, err := pool.Query(ctx,
		`SELECT `+attachmentColumns+`
		 FROM attachments a JOIN users u ON u.id = a.uploaded_by
		 WHERE a.issue_id = $1::uuid
		 ORDER BY a.created_at DESC`,
		normalizeIssueID(issueID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Attachment{}
	for rows.Next() {
		a, err := scanAttachment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *a)
	}
	return out, rows.Err()
}

// GetAttachment returns one attachment's metadata; the id must belong
// to the issue (otherwise ErrAttachmentNotFound). Any member may read.
// The handler resolves the bytes via the returned StoredPath.
func GetAttachment(ctx context.Context, pool *pgxpool.Pool,
	wsSlug, identifier, issueID, attachmentID, actorID string) (*Attachment, error) {
	if _, _, _, err := resolveSatelliteIssue(ctx, pool, wsSlug, identifier, issueID, actorID); err != nil {
		return nil, err
	}
	a, err := scanAttachment(pool.QueryRow(ctx,
		`SELECT `+attachmentColumns+`
		 FROM attachments a JOIN users u ON u.id = a.uploaded_by
		 WHERE a.id = $1::uuid AND a.issue_id = $2::uuid`,
		attachmentID, normalizeIssueID(issueID)))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) || isInvalidUUID(err) {
			return nil, ErrAttachmentNotFound
		}
		return nil, err
	}
	return a, nil
}

// DeleteAttachment removes the attachment row and its bytes. Member
// (15)+. The id must belong to the issue.
func DeleteAttachment(ctx context.Context, pool *pgxpool.Pool, st store.AttachmentStore,
	wsSlug, identifier, issueID, attachmentID, actorID string) error {
	if _, _, role, err := resolveSatelliteIssue(ctx, pool, wsSlug, identifier, issueID, actorID); err != nil {
		return err
	} else if err := requireSatelliteWriter(role); err != nil {
		return err
	}
	var storedPath string
	err := pool.QueryRow(ctx,
		`DELETE FROM attachments
		 WHERE id = $1::uuid AND issue_id = $2::uuid
		 RETURNING stored_path`,
		attachmentID, normalizeIssueID(issueID)).Scan(&storedPath)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) || isInvalidUUID(err) {
			return ErrAttachmentNotFound
		}
		return err
	}
	// Best effort: the row is gone either way; a failed byte-delete
	// leaves an orphaned file, never a dangling row.
	_ = st.Delete(storedPath)
	return nil
}
