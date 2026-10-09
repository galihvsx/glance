package api

// GitHub importer HTTP tests (C8T2): error mapping (404 surfaces GitHub's
// message, 401, 429 with Retry-After, 502, 400), JSON body validation, and
// the auth gate. The fetch/map logic itself is pinned by the service
// tests against a stub GitHub API.

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v5"

	"glance/internal/service"
)

// TestGitHubImportErrorMapping pins the honest-error contract: GitHub's
// own message is surfaced on 404/502, a bad token is 401, rate-limit
// exhaustion is 429 WITH the Retry-After header (never retried silently).
func TestGitHubImportErrorMapping(t *testing.T) {
	cases := []struct {
		name        string
		err         error
		wantStatus  int
		wantCode    string
		wantMsgPart string
		wantRetry   string
	}{
		{
			name:        "repo not found",
			err:         fmt.Errorf("wrap: %w", fmt.Errorf("%w: Not Found", service.ErrGitHubRepoNotFound)),
			wantStatus:  http.StatusNotFound,
			wantCode:    ErrCodeNotFound,
			wantMsgPart: "Not Found",
		},
		{
			name:        "bad token",
			err:         fmt.Errorf("%w: Bad credentials", service.ErrGitHubUnauthorized),
			wantStatus:  http.StatusUnauthorized,
			wantCode:    ErrCodeUnauthorized,
			wantMsgPart: "Bad credentials",
		},
		{
			name:        "rate limited",
			err:         &service.GitHubRateLimitError{RetryAfter: 120, Message: "API rate limit exceeded"},
			wantStatus:  http.StatusTooManyRequests,
			wantCode:    ErrCodeRateLimited,
			wantMsgPart: "API rate limit exceeded",
			wantRetry:   "120",
		},
		{
			name:        "upstream 500",
			err:         fmt.Errorf("%w: boom", service.ErrGitHubUpstream),
			wantStatus:  http.StatusBadGateway,
			wantCode:    ErrCodeBadGateway,
			wantMsgPart: "boom",
		},
		{
			name:        "bad input",
			err:         fmt.Errorf("%w: owner/repo must be 1-100 chars", service.ErrGitHubBadInput),
			wantStatus:  http.StatusBadRequest,
			wantCode:    ErrCodeBadRequest,
			wantMsgPart: "owner/repo",
		},
		{
			name:        "project errors still map",
			err:         service.ErrForbidden,
			wantStatus:  http.StatusForbidden,
			wantCode:    ErrCodeForbidden,
			wantMsgPart: "forbidden",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, rec := envelopeContext()
			if err := githubImportError(c, tc.err); err != nil {
				t.Fatalf("githubImportError returned error: %v", err)
			}
			if rec.Code != tc.wantStatus {
				t.Errorf("status = %d, want %d (body: %s)", rec.Code, tc.wantStatus, rec.Body.String())
			}
			code, message, _, _ := decodeEnvelope(t, rec.Body.Bytes())
			if code != tc.wantCode {
				t.Errorf("code = %q, want %q", code, tc.wantCode)
			}
			if !strings.Contains(message, tc.wantMsgPart) {
				t.Errorf("message = %q, want it to contain %q", message, tc.wantMsgPart)
			}
			if tc.wantRetry != "" && rec.Header().Get("Retry-After") != tc.wantRetry {
				t.Errorf("Retry-After = %q, want %q", rec.Header().Get("Retry-After"), tc.wantRetry)
			}
		})
	}
}

// TestGitHubImportBodyValidation pins the JSON body gate: malformed
// bodies are 400 before any GitHub request happens.
func TestGitHubImportBodyValidation(t *testing.T) {
	e := echo.New()

	newCtx := func(body string) *echo.Context {
		req := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		return e.NewContext(req, rec)
	}

	// Not JSON at all.
	c := newCtx("this is not json")
	in, ok := decodeGitHubImportBody(c)
	if ok {
		t.Errorf("decode(invalid json): ok = true, want false")
	}
	_ = in

	// Valid JSON decodes.
	c = newCtx(`{"owner":"acme","repo":"widgets","token":"t","state_filter":"all","max":50}`)
	in, ok = decodeGitHubImportBody(c)
	if !ok {
		t.Fatalf("decode(valid json): ok = false")
	}
	if in.Owner != "acme" || in.Repo != "widgets" || in.Token != "t" || in.StateFilter != "all" || in.Max != 50 {
		t.Errorf("decoded input = %+v, want all fields", in)
	}
}

// TestGitHubImportRoutesRequireAuth pins the auth gate on both new
// routes: no session cookie → 401 without touching GitHub.
func TestGitHubImportRoutesRequireAuth(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	e := testServer(t, pool)
	RegisterImportRoutes(e, &ProjectHandler{Pool: pool})

	for _, path := range []string{
		"/api/v1/workspaces/nope/projects/NOPE/imports/github/preview",
		"/api/v1/workspaces/nope/projects/NOPE/imports/github",
	} {
		req := httptest.NewRequest(http.MethodPost, path,
			strings.NewReader(`{"owner":"a","repo":"b"}`))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("POST %s without auth: status = %d, want 401", path, rec.Code)
		}
	}
}
