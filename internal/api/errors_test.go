package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v5"
)

// envelopeContext builds a bare echo context for unit-testing WriteError.
func envelopeContext() (*echo.Context, *httptest.ResponseRecorder) {
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	return e.NewContext(req, rec), rec
}

// decodeEnvelope unmarshals a WriteError body into its code/message/details.
func decodeEnvelope(t *testing.T, body []byte) (code, message string, details map[string]any, hasDetails bool) {
	t.Helper()
	var decoded struct {
		Error struct {
			Code    string         `json:"code"`
			Message string         `json:"message"`
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	raw := map[string]any{}
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatalf("decode envelope %q: %v", body, err)
	}
	if _, ok := raw["error"]; !ok {
		t.Fatalf("body %q: missing top-level error key", body)
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("decode envelope %q: %v", body, err)
	}
	_, hasDetails = raw["error"].(map[string]any)["details"]
	return decoded.Error.Code, decoded.Error.Message, decoded.Error.Details, hasDetails
}

// TestWriteErrorEnvelopeShape pins the spec §5 envelope: code and message
// are always present, details is omitted when nil and present when set.
func TestWriteErrorEnvelopeShape(t *testing.T) {
	c, rec := envelopeContext()
	if err := WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "name is required", nil); err != nil {
		t.Fatalf("WriteError: %v", err)
	}
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	body := rec.Body.Bytes()
	if strings.Contains(string(body), `"details"`) {
		t.Fatalf("body %q: details must be omitted when nil", body)
	}
	code, message, _, hasDetails := decodeEnvelope(t, body)
	if code != "bad_request" || message != "name is required" {
		t.Fatalf("code = %q, message = %q; want bad_request / name is required", code, message)
	}
	if hasDetails {
		t.Fatalf("body %q: details key must be absent when nil", body)
	}

	c2, rec2 := envelopeContext()
	if err := WriteError(c2, http.StatusBadRequest, ErrCodeBadRequest, "invalid", map[string]any{"field": "name"}); err != nil {
		t.Fatalf("WriteError with details: %v", err)
	}
	_, _, details, hasDetails := decodeEnvelope(t, rec2.Body.Bytes())
	if !hasDetails {
		t.Fatalf("body %q: details key must be present when set", rec2.Body.String())
	}
	if details["field"] != "name" {
		t.Fatalf("details = %v, want field=name", details)
	}
}

// TestWriteInternalError pins the 500 path: status 500, code "internal",
// generic message, no detail leaked.
func TestWriteInternalError(t *testing.T) {
	c, rec := envelopeContext()
	if err := WriteInternalError(c); err != nil {
		t.Fatalf("WriteInternalError: %v", err)
	}
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	code, message, _, hasDetails := decodeEnvelope(t, rec.Body.Bytes())
	if code != "internal" {
		t.Fatalf("code = %q, want internal", code)
	}
	if message != "internal error" {
		t.Fatalf("message = %q, want internal error", message)
	}
	if hasDetails {
		t.Fatalf("body %q: 500 must not leak details", rec.Body.String())
	}
}
