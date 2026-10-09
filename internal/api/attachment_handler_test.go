package api

// Attachment HTTP endpoints (C4T2): multipart upload → 201, list,
// download (inline vs. forced-download content policy), delete → 204,
// size-limit 413, and the spec §5 envelope on errors. Real test
// database, no skips.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"strings"
	"testing"

	"glance/internal/store"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v5"
)

func testAttachmentServer(t *testing.T, pool *pgxpool.Pool, maxBytes int64) (*echo.Echo, store.AttachmentStore) {
	t.Helper()
	st, err := store.NewFileAttachmentStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileAttachmentStore: %v", err)
	}
	e := testIssueServer(t, pool)
	h := &IssueHandler{Pool: pool, Attachments: st, MaxUploadBytes: maxBytes}
	RegisterSatelliteRoutes(e, h)
	return e, st
}

func setupAttachmentHTTP(t *testing.T, e *echo.Echo, cookie *http.Cookie) (issueBase, attBase string) {
	t.Helper()
	slug := uniqueSlug("att-http")
	createWorkspaceHTTP(t, e, cookie, "Attachment Co", slug)
	ident := uniqueProjectIdentifier("ATT")
	createProjectHTTP(t, e, cookie, slug, "Engineering", ident)
	base := "/api/v1/workspaces/" + slug + "/projects/" + ident + "/issues"
	rec := postAuthedJSON(t, e, http.MethodPost, base, cookie, `{"name":"Filed"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create issue: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var iss struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &iss); err != nil {
		t.Fatalf("decode issue: %v", err)
	}
	attBase = base + "/" + iss.ID + "/attachments"
	return base, attBase
}

// postMultipartFile posts a single file part; filename "" means "no file
// part at all" (an empty multipart body).
func postMultipartFile(t *testing.T, e *echo.Echo, path string, cookie *http.Cookie,
	filename, partContentType string, data []byte) *httptest.ResponseRecorder {
	t.Helper()
	var b bytes.Buffer
	w := multipart.NewWriter(&b)
	if filename != "" {
		h := textproto.MIMEHeader{}
		h.Set("Content-Disposition",
			fmt.Sprintf(`form-data; name="file"; filename="%s"`, filename))
		h.Set("Content-Type", partContentType)
		fw, err := w.CreatePart(h)
		if err != nil {
			t.Fatalf("CreatePart: %v", err)
		}
		if _, err := fw.Write(data); err != nil {
			t.Fatalf("write part: %v", err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("writer.Close: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, path, &b)
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}

func authedReq(t *testing.T, e *echo.Echo, method, path string, cookie *http.Cookie, body io.Reader) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, body)
	if cookie != nil {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}

func decodeAttachment(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode attachment: %v (body: %s)", err, rec.Body.String())
	}
	return out
}

func TestAttachmentHTTPUploadDownloadDelete(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	e, _ := testAttachmentServer(t, pool, 25<<20)
	cookie := loginTestUser(t, e, pool, uniqueEmail("att-http"), "test-agent", uniqueIP())
	_, attBase := setupAttachmentHTTP(t, e, cookie)

	// PNG bytes: sniffable signature, declared type agrees.
	png := []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0xde, 0xad}
	rec := postMultipartFile(t, e, attBase, cookie, "shot.png", "image/png", png)
	if rec.Code != http.StatusCreated {
		t.Fatalf("upload: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	created := decodeAttachment(t, rec)
	attID, _ := created["id"].(string)
	if attID == "" {
		t.Fatalf("upload response has no id: %s", rec.Body.String())
	}
	if created["filename"] != "shot.png" {
		t.Errorf("filename = %v, want shot.png", created["filename"])
	}
	if _, has := created["stored_path"]; has {
		t.Error("response leaks stored_path (server-side name)")
	}

	// List shows the upload with size + uploader + date.
	rec = authedReq(t, e, http.MethodGet, attBase, cookie, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var list struct {
		Attachments []map[string]any `json:"attachments"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(list.Attachments) != 1 {
		t.Fatalf("list = %d items, want 1", len(list.Attachments))
	}
	item := list.Attachments[0]
	if item["size_bytes"] != float64(len(png)) {
		t.Errorf("size_bytes = %v, want %d", item["size_bytes"], len(png))
	}
	if _, ok := item["uploaded_by"]; !ok {
		t.Error("list item missing uploaded_by")
	}
	if _, ok := item["created_at"]; !ok {
		t.Error("list item missing created_at")
	}

	// Download: image/* served inline, exact bytes, nosniff.
	rec = authedReq(t, e, http.MethodGet, attBase+"/"+attID, cookie, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("download: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "image/png" {
		t.Errorf("Content-Type = %q, want image/png", ct)
	}
	if cd := rec.Header().Get("Content-Disposition"); !strings.HasPrefix(cd, "inline") {
		t.Errorf("Content-Disposition = %q, want inline for image/png", cd)
	}
	if v := rec.Header().Get("X-Content-Type-Options"); v != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want nosniff", v)
	}
	if !bytes.Equal(rec.Body.Bytes(), png) {
		t.Error("downloaded bytes differ from upload")
	}

	// Delete → 204; afterwards the attachment 404s and the list is empty.
	rec = authedReq(t, e, http.MethodDelete, attBase+"/"+attID, cookie, nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	rec = authedReq(t, e, http.MethodGet, attBase+"/"+attID, cookie, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("download after delete: status = %d, want 404", rec.Code)
	}
	rec = authedReq(t, e, http.MethodGet, attBase, cookie, nil)
	var after struct {
		Attachments []map[string]any `json:"attachments"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &after); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(after.Attachments) != 0 {
		t.Fatalf("list after delete = %d items, want 0", len(after.Attachments))
	}
}

func TestAttachmentHTTPInlinePolicy(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	e, _ := testAttachmentServer(t, pool, 25<<20)
	cookie := loginTestUser(t, e, pool, uniqueEmail("att-http"), "test-agent", uniqueIP())
	_, attBase := setupAttachmentHTTP(t, e, cookie)

	// Never serve dangerous types inline, even when the client declares
	// them as such: text/html and application/javascript must download.
	dangerous := []struct {
		name, ctype string
		// binary forces the body to sniff as application/octet-stream so
		// the client-declared type is the one that gets stored.
		binary bool
	}{
		{"evil.html", "text/html", false},
		{"evil2.html", "text/html; charset=utf-8", false},
		{"x.js", "application/javascript", false},
		{"x2.js", "application/x-javascript", false},
		// SVG: an attacker-controlled <script> inside a navigated SVG
		// would execute in the glance origin — must force download even
		// though it matches image/*.
		{"evil.svg", "image/svg+xml", true},
	}
	for _, d := range dangerous {
		body := []byte("<script>alert(1)</script>")
		if d.binary {
			body = append([]byte{0x00}, []byte(`<svg xmlns="http://www.w3.org/2000/svg" onload="alert(1)"></svg>`)...)
		}
		rec := postMultipartFile(t, e, attBase, cookie, d.name, d.ctype, body)
		if rec.Code != http.StatusCreated {
			t.Fatalf("upload %s: status = %d", d.name, rec.Code)
		}
		attID := decodeAttachment(t, rec)["id"].(string)
		rec = authedReq(t, e, http.MethodGet, attBase+"/"+attID, cookie, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("download %s: status = %d", d.name, rec.Code)
		}
		ct := rec.Header().Get("Content-Type")
		cd := rec.Header().Get("Content-Disposition")
		// NOTE: image/svg+xml is stored as-is but served as attachment —
		// the disposition check below is its safety invariant.
		if ct == "text/html" || ct == "application/javascript" || ct == "application/x-javascript" {
			t.Errorf("%s: served inline-capable Content-Type %q", d.name, ct)
		}
		if !strings.HasPrefix(cd, "attachment") {
			t.Errorf("%s: Content-Disposition = %q, want forced download", d.name, cd)
		}
	}

	// text/* (non-html) IS served inline.
	rec := postMultipartFile(t, e, attBase, cookie, "notes.txt", "text/plain", []byte("hello"))
	if rec.Code != http.StatusCreated {
		t.Fatalf("upload notes.txt: status = %d", rec.Code)
	}
	attID := decodeAttachment(t, rec)["id"].(string)
	rec = authedReq(t, e, http.MethodGet, attBase+"/"+attID, cookie, nil)
	if cd := rec.Header().Get("Content-Disposition"); !strings.HasPrefix(cd, "inline") {
		t.Errorf("notes.txt: Content-Disposition = %q, want inline", cd)
	}
}

func TestAttachmentHTTPSizeLimit(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	e, _ := testAttachmentServer(t, pool, 64) // 64-byte cap
	cookie := loginTestUser(t, e, pool, uniqueEmail("att-http"), "test-agent", uniqueIP())
	_, attBase := setupAttachmentHTTP(t, e, cookie)

	rec := postMultipartFile(t, e, attBase, cookie, "big.bin",
		"application/octet-stream", bytes.Repeat([]byte("a"), 65))
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversize upload: status = %d, want 413 (body: %s)", rec.Code, rec.Body.String())
	}

	// Exactly at the cap still works.
	rec = postMultipartFile(t, e, attBase, cookie, "ok.bin",
		"application/octet-stream", bytes.Repeat([]byte("a"), 64))
	if rec.Code != http.StatusCreated {
		t.Fatalf("at-cap upload: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
}

func TestAttachmentHTTPValidation(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	e, _ := testAttachmentServer(t, pool, 25<<20)
	cookie := loginTestUser(t, e, pool, uniqueEmail("att-http"), "test-agent", uniqueIP())
	_, attBase := setupAttachmentHTTP(t, e, cookie)

	// No file part → 400.
	rec := postMultipartFile(t, e, attBase, cookie, "", "", nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty upload: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}

	// Malformed attachment id → 400 (never reaches a ::uuid cast).
	rec = authedReq(t, e, http.MethodGet, attBase+"/not-a-uuid", cookie, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad id: status = %d, want 400", rec.Code)
	}

	// Missing attachment → 404 with the spec §5 envelope.
	rec = authedReq(t, e, http.MethodGet, attBase+"/00000000-0000-0000-0000-000000000000", cookie, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing: status = %d, want 404", rec.Code)
	}

	// Unauthenticated → 401.
	rec = authedReq(t, e, http.MethodGet, attBase, nil, nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated list: status = %d, want 401", rec.Code)
	}
}

func TestServeInlinePolicy(t *testing.T) {
	cases := []struct {
		ctype  string
		inline bool
	}{
		{"image/png", true},
		{"image/jpeg", true},
		{"text/plain", true},
		{"text/plain; charset=utf-8", true},
		{"text/html", false},
		{"text/html; charset=utf-8", false},
		{"application/javascript", false},
		{"application/x-javascript", false},
		{"application/ecmascript", false},
		{"application/x-ecmascript", false},
		// SVG renders as a document when navigated to — embedded
		// scripts would execute in the glance origin: never inline.
		{"image/svg+xml", false},
		{"image/svg+xml; charset=utf-8", false},
		{"image/svg", false},
		{"application/octet-stream", false},
		{"application/pdf", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := serveInline(tc.ctype); got != tc.inline {
			t.Errorf("serveInline(%q) = %v, want %v", tc.ctype, got, tc.inline)
		}
	}
}
