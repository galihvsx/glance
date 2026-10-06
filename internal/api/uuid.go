package api

import (
	"net/http"

	"github.com/labstack/echo/v5"
)

// validUUID reports whether s is a canonical 8-4-4-4-12 hex UUID.
//
// A malformed UUID must never reach a Postgres ::uuid cast: the parse
// error would surface as a 500. Every handler that takes a UUID-typed
// path param validates it up front — via requireUUIDParam (400) or, for
// the auth-adjacent session endpoints, a deliberate 404 that never
// distinguishes malformed from missing (no enumeration).
func validUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i := 0; i < 36; i++ {
		ch := s[i]
		switch i {
		case 8, 13, 18, 23:
			if ch != '-' {
				return false
			}
		default:
			if !(ch >= '0' && ch <= '9' || ch >= 'a' && ch <= 'f' || ch >= 'A' && ch <= 'F') {
				return false
			}
		}
	}
	return true
}

// requireUUIDParam validates that the named path param is a canonical
// UUID. A malformed value answers 400 bad_request through the spec §5
// envelope, so client-controlled input never reaches a ::uuid cast.
//
// ok=false means the error response was already written; the handler
// must return nil (not an error — the response is committed).
func requireUUIDParam(c *echo.Context, param, what string) (v string, ok bool) {
	v = c.Param(param)
	if !validUUID(v) {
		WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid "+what, nil)
		return "", false
	}
	return v, true
}
