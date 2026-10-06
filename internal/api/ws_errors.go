package api

import (
	"errors"

	"github.com/jackc/pgx/v5"
)

// Sentinel errors for websocket channel authorization (Task 24). They map
// to client-facing messages; no internals leak.
var (
	errWSUnauthorized  = errors.New("unauthorized")
	errWSTicketInvalid = errors.New("invalid or expired ticket")
	errWSForbidden     = errors.New("forbidden: no access to channel")
	errWSBadChannel    = errors.New("unknown or malformed channel")
	errWSCheckFailed   = errors.New("channel authorization check failed")
)

// authorizeErr maps a membership lookup outcome to an authz verdict:
// row found → nil, no row → forbidden, query error → generic failure
// (fail closed; neither case leaks existence or internals).
func authorizeErr(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return errWSForbidden
	}
	return errWSCheckFailed
}
