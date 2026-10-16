package api

// Linear importer HTTP tests (C31T0): error mapping (bad key / Linear
// 401 → 502 GENERIC, never the key; GraphQL errors → 502; team not
// found → 400; 429 with Retry-After), JSON body validation, and the
// auth gate. The fetch/map logic itself is pinned by the service tests
// against a stub Linear GraphQL API.

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v5"

	"glance/internal/service"
)

// TestLinearImportErrorMapping pins the honest-error contract: a bad
// key is 502 with a FIXED generic message (the handler deliberately
// does not surface Linear's message or the key), GraphQL errors are
// 502, a missing team is 400, rate-limit exhaustion is 429 WITH the
// Retry-After header (never retried silently).
func TestLinearImportErrorMapping(t *testing.T) {
	cases := []struct {
		name        string
		err         error
		wantStatus  int
		wantCode    string
		wantMsgPart string
		wantRetry   string
		// mustNotContain pins that the response never echoes a
		// credential-like value, even when the wrapped error carries one.
		mustNotContain string
	}{
		{
			name:           "team not found",
			err:            fmt.Errorf("wrap: %w", fmt.Errorf("%w: team-missing", service.ErrLinearTeamNotFound)),
			wantStatus:     http.StatusBadRequest,
			wantCode:       ErrCodeBadRequest,
			wantMsgPart:    "team",
			mustNotContain: "",
		},
		{
			name:           "bad key is generic 502",
			err:            fmt.Errorf("%w: key=lin_api_SECRETKEY", service.ErrLinearUnauthorized),
			wantStatus:     http.StatusBadGateway,
			wantCode:       ErrCodeBadGateway,
			wantMsgPart:    "linear authentication failed",
			mustNotContain: "lin_api_SECRETKEY",
		},
		{
			name:           "rate limited",
			err:            &service.LinearRateLimitError{RetryAfter: 120, Message: "linear rate limit exceeded"},
			wantStatus:     http.StatusTooManyRequests,
			wantCode:       ErrCodeRateLimited,
			wantMsgPart:    "linear rate limit exceeded",
			wantRetry:      "120",
			mustNotContain: "",
		},
		{
			name:           "upstream 500",
			err:            fmt.Errorf("%w: boom", service.ErrLinearUpstream),
			wantStatus:     http.StatusBadGateway,
			wantCode:       ErrCodeBadGateway,
			wantMsgPart:    "boom",
			mustNotContain: "",
		},
		{
			name:           "bad input",
			err:            fmt.Errorf("%w: team_id is required", service.ErrLinearBadInput),
			wantStatus:     http.StatusBadRequest,
			wantCode:       ErrCodeBadRequest,
			wantMsgPart:    "team_id",
			mustNotContain: "",
		},
		{
			name:           "project errors still map",
			err:            service.ErrForbidden,
			wantStatus:     http.StatusForbidden,
			wantCode:       ErrCodeForbidden,
			wantMsgPart:    "forbidden",
			mustNotContain: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, rec := envelopeContext()
			if err := linearImportError(c, tc.err); err != nil {
				t.Fatalf("linearImportError returned error: %v", err)
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
			if tc.mustNotContain != "" && strings.Contains(rec.Body.String(), tc.mustNotContain) {
				t.Errorf("response leaks credential material: %s", rec.Body.String())
			}
		})
	}
}

// TestLinearImportBodyValidation pins the JSON body gate: malformed
// bodies are 400 before any Linear request happens.
func TestLinearImportBodyValidation(t *testing.T) {
	e := echo.New()

	newCtx := func(body string) *echo.Context {
		req := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		return e.NewContext(req, rec)
	}

	// Not JSON at all.
	c := newCtx("this is not json")
	in, ok := decodeLinearImportBody(c)
	if ok {
		t.Errorf("decode(invalid json): ok = true, want false")
	}
	_ = in

	// Valid JSON decodes.
	c = newCtx(`{"api_key":"k","team_id":"team-1","max":50}`)
	in, ok = decodeLinearImportBody(c)
	if !ok {
		t.Fatalf("decode(valid json): ok = false")
	}
	if in.APIKey != "k" || in.TeamID != "team-1" || in.Max != 50 {
		t.Errorf("decoded input = %+v, want all fields", in)
	}

	// Teams body decodes.
	c = newCtx(`{"api_key":"k"}`)
	key, ok := decodeLinearTeamsBody(c)
	if !ok || key != "k" {
		t.Errorf("decode teams body: key=%q ok=%v, want k/true", key, ok)
	}
}

// TestLinearImportRoutesRequireAuth pins the auth gate on all three
// new routes: no session cookie → 401 without touching Linear.
func TestLinearImportRoutesRequireAuth(t *testing.T) {
	pool := newTestPool(t)
	e := testServer(t, pool)
	RegisterImportRoutes(e, &ProjectHandler{Pool: pool})

	for _, path := range []string{
		"/api/v1/workspaces/nope/projects/NOPE/imports/linear/teams",
		"/api/v1/workspaces/nope/projects/NOPE/imports/linear/preview",
		"/api/v1/workspaces/nope/projects/NOPE/imports/linear",
	} {
		body := `{"api_key":"k"}`
		if strings.HasSuffix(path, "/preview") || !strings.HasSuffix(path, "/teams") {
			body = `{"api_key":"k","team_id":"team-1"}`
		}
		req := httptest.NewRequest(http.MethodPost, path,
			strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("POST %s without auth: status = %d, want 401", path, rec.Code)
		}
	}
}
