package api

import (
	"net/http"

	"github.com/labstack/echo/v5"
)

// Stable machine-readable error codes for the spec §5 error envelope.
// Every handler error path answers with one of these — never a bare
// status and never a free-form string the client must substring-match.
const (
	ErrCodeBadRequest     = "bad_request"
	ErrCodeUnauthorized   = "unauthorized"
	ErrCodeForbidden      = "forbidden"
	ErrCodeNotFound       = "not_found"
	ErrCodeUserNotFound   = "user_not_found"
	ErrCodeMemberNotFound = "member_not_found"
	ErrCodeConflict       = "conflict"
	// ErrCodeOpenBlockers is the 409 code (C16T3) when an issue is moved
	// to a completed state while it has open blockers. details carries
	// {"blockers":[...]} — the blocker display IDs.
	ErrCodeOpenBlockers = "open_blockers"
	// ErrCodeIssueLinkCycle is the 409 code (C94T0) when a new 'blocks'
	// link would close a dependency cycle. details carries
	// {"path":[...]} — the cycle path as display IDs, starting and
	// ending at the new edge's source.
	ErrCodeIssueLinkCycle = "issue_link_cycle"
	// ErrCodeStateTransitionNotAllowed is the 409 code (C68T0) when a
	// state move is not an allowed transition of the project's workflow.
	// details carries {"allowed":[...]} — the permitted next-state IDs.
	ErrCodeStateTransitionNotAllowed = "state_transition_not_allowed"
	ErrCodeRateLimited               = "rate_limited"
	ErrCodeGone                      = "gone"
	ErrCodePayloadTooLarge           = "payload_too_large"
	ErrCodeInternal                  = "internal"
	// ErrCodeScheduleNotFound is the 404 code (C28T2) for an unknown
	// recurring schedule id within a confirmed project.
	ErrCodeScheduleNotFound = "schedule_not_found"
	// ErrCodeAINotConfigured is the 503 code for the AI assist endpoints
	// when no provider key is configured (C5T2): an honest "not
	// configured" rather than a fake error or a 500.
	ErrCodeAINotConfigured = "ai_not_configured"
	// ErrCodeBadGateway is the 502 code when a configured AI provider
	// answers with an error (C5T2): the failure is the provider's, not
	// glance's — never report it as a 500.
	ErrCodeBadGateway = "bad_gateway"
)

// errorEnvelope is the spec §5 error shape:
//
//	{"error":{"code":"…","message":"…","details":…}}
//
// details is omitted when nil, so clients can rely on error.code and
// error.message always being present.
type errorEnvelope struct {
	Error errorBody `json:"error"`
}

type errorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Details any    `json:"details,omitempty"`
}

// WriteError writes the spec §5 error envelope with the given HTTP status
// and machine-readable code. details carries optional structured context
// (validation failures, retry hints) and is omitted from the JSON when nil.
// It is the ONLY writer of the top-level "error" key — grep for `"error"`
// outside this file must find nothing.
func WriteError(c *echo.Context, status int, code, message string, details any) error {
	return c.JSON(status, errorEnvelope{Error: errorBody{Code: code, Message: message, Details: details}})
}

// WriteInternalError is the 500 path: code "internal", generic message, no
// detail leaked. Use it wherever a handler would otherwise hand-roll the
// same three lines.
func WriteInternalError(c *echo.Context) error {
	return WriteError(c, http.StatusInternalServerError, ErrCodeInternal, "internal error", nil)
}
