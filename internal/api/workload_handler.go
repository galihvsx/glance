package api

// Workload & capacity endpoints (C29T0).
//
//   GET /api/v1/workspaces/:slug/workload[?project=<uuid>...]
//     Per-member workload report (open counts, estimate points,
//     overdue, priority buckets, 14-day velocity, overloaded flag)
//     plus per-project open unassigned counts. Any workspace member
//     (guest 5+) may read. ?project may repeat to scope issue
//     counting to projects; a malformed project id is a 400, a
//     project of another workspace is a 404.
//   PUT /api/v1/workspaces/:slug/members/:userId/capacity
//     Set a member's weekly capacity (story points); JSON null
//     clears it back to NULL. Member (15)+.
//
// Error codes follow the api envelope conventions:
// ErrInvalidCapacity → 400 bad_request; ErrMemberNotFound → 404
// member_not_found; ErrProjectNotFound → 404 project; ErrNotFound →
// 404 workspace. A bad slug or a non-member caller is a 404 (never a
// leak); a guest on PUT is a 403.

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v5"

	"glance/internal/service"
)

// workloadMaxBytes caps the capacity JSON body: 64KB is generous for a
// single integer field.
const workloadMaxBytes = 64 << 10

// WorkloadHandler serves the workload endpoints.
type WorkloadHandler struct {
	Pool *pgxpool.Pool
}

// RegisterWorkloadRoutes mounts the workload endpoints on the
// workspace group. Call this before the SPA catch-all so API routes
// are never shadowed.
func RegisterWorkloadRoutes(e *echo.Echo, h *WorkloadHandler) {
	g := e.Group("/api/v1/workspaces/:slug", RequireAuth(h.Pool))
	g.GET("/workload", h.getWorkload)
	g.PUT("/members/:userId/capacity", h.setCapacity)
}

// workloadError maps workload sentinel errors to HTTP statuses.
func workloadError(c *echo.Context, err error) error {
	switch {
	case errors.Is(err, service.ErrInvalidCapacity):
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, err.Error(), nil)
	case errors.Is(err, service.ErrMemberNotFound):
		return WriteError(c, http.StatusNotFound, ErrCodeMemberNotFound, "member not found", nil)
	case errors.Is(err, service.ErrProjectNotFound):
		return WriteError(c, http.StatusNotFound, ErrCodeNotFound, "project not found", nil)
	case errors.Is(err, service.ErrNotFound):
		return WriteError(c, http.StatusNotFound, ErrCodeNotFound, "workspace not found", nil)
	default:
		return WriteInternalError(c)
	}
}

// resolveWorkloadWorkspace resolves :slug (slug or UUID) to the
// workspace id for the calling member. A bad slug or a non-member
// caller gets a 404; a caller below minRole gets a 403 (never a leak).
// minRole 0 skips the role check (any member may read). ok=false means
// the response was already written — the caller must return nil
// (the decodeScheduleJSON pattern: WriteError answers with nil on a
// successful write, so it cannot double as the error return).
func (h *WorkloadHandler) resolveWorkloadWorkspace(c *echo.Context, minRole int) (wsID uuid.UUID, ok bool) {
	var rawID string
	var role int
	err := h.Pool.QueryRow(c.Request().Context(),
		`SELECT w.id::text, m.role
		   FROM workspaces w
		   JOIN workspace_members m ON m.workspace_id = w.id
		  WHERE (w.slug = $1 OR w.id::text = $1)
		    AND m.user_id = $2::uuid`,
		c.Param("slug"), CurrentUser(c).ID).Scan(&rawID, &role)
	if err != nil {
		WriteError(c, http.StatusNotFound, ErrCodeNotFound, "workspace not found", nil)
		return uuid.Nil, false
	}
	if role < minRole {
		WriteError(c, http.StatusForbidden, ErrCodeForbidden, "forbidden", nil)
		return uuid.Nil, false
	}
	id, err := uuid.Parse(rawID)
	if err != nil {
		WriteInternalError(c) // cannot happen: uuid from the database
		return uuid.Nil, false
	}
	return id, true
}

// ---------- get ----------

// getWorkload implements GET /api/v1/workspaces/:slug/workload: the
// per-member workload report. Any workspace member may read.
func (h *WorkloadHandler) getWorkload(c *echo.Context) error {
	wsID, ok := h.resolveWorkloadWorkspace(c, 0)
	if !ok {
		return nil
	}
	var projectIDs []uuid.UUID
	for _, raw := range c.QueryParams()["project"] {
		id, perr := uuid.Parse(raw)
		if perr != nil {
			return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest,
				"invalid project id: want a UUID", nil)
		}
		projectIDs = append(projectIDs, id)
	}
	rep, err := service.GetWorkload(c.Request().Context(), h.Pool, wsID, CurrentUser(c).ID, projectIDs)
	if err != nil {
		return workloadError(c, err)
	}
	return c.JSON(http.StatusOK, rep)
}

// ---------- set capacity ----------

// setCapacity implements PUT
// /api/v1/workspaces/:slug/members/:userId/capacity. Member (15)+.
// The body carries exactly one field: {"weekly_capacity_points":
// <int|null>} — absent is a 400 (nothing to do), explicit null clears
// the capacity, a number sets it (0..999, else 400).
func (h *WorkloadHandler) setCapacity(c *echo.Context) error {
	wsID, ok := h.resolveWorkloadWorkspace(c, service.RoleMember)
	if !ok {
		return nil
	}
	userID, err := uuid.Parse(c.Param("userId"))
	if err != nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest,
			"invalid user id: want a UUID", nil)
	}

	r := c.Request()
	r.Body = http.MaxBytesReader(c.Response(), r.Body, workloadMaxBytes)
	var raw map[string]json.RawMessage
	if derr := json.NewDecoder(r.Body).Decode(&raw); derr != nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest,
			"request body must be valid JSON", nil)
	}
	v, present := raw["weekly_capacity_points"]
	if !present {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest,
			"weekly_capacity_points is required (null clears the capacity)", nil)
	}
	var capacity *int
	if string(bytes.TrimSpace(v)) != "null" {
		var n int
		if uerr := json.Unmarshal(v, &n); uerr != nil {
			return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest,
				"weekly_capacity_points must be an integer 0..999 or null", nil)
		}
		capacity = &n
	}

	if err := service.SetMemberCapacity(c.Request().Context(), h.Pool, wsID, userID, capacity); err != nil {
		return workloadError(c, err)
	}
	return c.JSON(http.StatusOK, map[string]any{
		"user_id":                userID.String(),
		"weekly_capacity_points": capacity,
	})
}
