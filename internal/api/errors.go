package api

import (
	"net/http"

	"github.com/labstack/echo/v5"
)

// Stable machine-readable error codes for the spec §5 error envelope.
// Every handler error path answers with one of these — never a bare
// status and never a free-form string the client must substring-match.
const (
	ErrCodeBadRequest      = "bad_request"
	ErrCodeUnauthorized    = "unauthorized"
	ErrCodeForbidden       = "forbidden"
	ErrCodeNotFound        = "not_found"
	ErrCodeUserNotFound    = "user_not_found"
	ErrCodeMemberNotFound  = "member_not_found"
	ErrCodeConflict        = "conflict"
	ErrCodeRateLimited     = "rate_limited"
	ErrCodeGone            = "gone"
	ErrCodePayloadTooLarge = "payload_too_large"
	ErrCodeInternal        = "internal"
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
