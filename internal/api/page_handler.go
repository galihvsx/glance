package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/labstack/echo/v5"

	"glance/internal/service"
)

// Page endpoints (C3T3) nested under projects. Every route sits behind
// RequireAuth — workspace membership is the tenancy boundary. Errors use
// the spec §5 envelope via pageError (which falls back to issueError for
// the shared sentinels).
//
// PATCH tri-state: a missing key leaves the field untouched, any present
// value sets it (content may be ""; title must be non-empty when given).
//
// Move tri-state: the move body is decoded with key-presence detection —
// a missing "parent_id" keeps the parent, explicit null moves to root,
// a uuid reparents. A move under the page itself or a descendant is 400.

// RegisterPageRoutes mounts the page endpoints. Call this before the
// SPA catch-all so API routes are never shadowed.
func RegisterPageRoutes(e *echo.Echo, h *IssueHandler) {
	g := e.Group("/api/v1/workspaces/:slug/projects/:identifier/pages", RequireAuth(h.Pool))
	g.POST("", h.createPage)
	g.GET("", h.listPages)
	g.GET("/:pageID", h.getPage)
	g.PATCH("/:pageID", h.updatePage)
	g.DELETE("/:pageID", h.deletePage)
	g.POST("/:pageID/move", h.movePage)
	g.GET("/:pageID/revisions", h.listPageRevisions)
	g.POST("/:pageID/restore", h.restorePageRevision)
}

// pageError maps page sentinel errors to HTTP statuses, delegating the
// shared sentinels (ErrNotFound, ErrProjectNotFound, ErrForbidden,
// ErrNothingToUpdate) to issueError.
func pageError(c *echo.Context, err error) error {
	switch {
	case errors.Is(err, service.ErrPageNotFound):
		return WriteError(c, http.StatusNotFound, ErrCodeNotFound, "page not found", nil)
	case errors.Is(err, service.ErrInvalidPage):
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid page: bad title, parent, or position", nil)
	case errors.Is(err, service.ErrInvalidPageID):
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid id", nil)
	case errors.Is(err, service.ErrPageCycle):
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "page move would create a cycle", nil)
	case errors.Is(err, service.ErrRevisionNotFound):
		return WriteError(c, http.StatusNotFound, ErrCodeNotFound, "page revision not found", nil)
	default:
		return issueError(c, err)
	}
}

type pageBody struct {
	Title    string  `json:"title"`
	Content  *string `json:"content"`
	ParentID *string `json:"parent_id"`
}

// createPage implements POST
// /api/v1/workspaces/{slug}/projects/{identifier}/pages.
// Member (15)+; the service enforces it.
func (h *IssueHandler) createPage(c *echo.Context) error {
	var body pageBody
	if err := c.Bind(&body); err != nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body", nil)
	}
	page, err := service.CreatePage(c.Request().Context(), h.Pool,
		c.Param("slug"), c.Param("identifier"), CurrentUser(c).ID,
		service.PageInput{
			Title:    body.Title,
			Content:  body.Content,
			ParentID: body.ParentID,
		})
	if err != nil {
		return pageError(c, err)
	}
	return c.JSON(http.StatusCreated, page)
}

// listPages implements GET
// /api/v1/workspaces/{slug}/projects/{identifier}/pages: flat list, roots
// first, siblings by position — the frontend builds the tree.
func (h *IssueHandler) listPages(c *echo.Context) error {
	pages, err := service.ListPages(c.Request().Context(), h.Pool,
		c.Param("slug"), c.Param("identifier"), CurrentUser(c).ID)
	if err != nil {
		return pageError(c, err)
	}
	return c.JSON(http.StatusOK, map[string]any{"pages": pages})
}

// getPage implements GET .../pages/{pageID}.
func (h *IssueHandler) getPage(c *echo.Context) error {
	pageID, ok := requireUUIDParam(c, "pageID", "page id")
	if !ok {
		return nil
	}
	page, err := service.GetPage(c.Request().Context(), h.Pool,
		c.Param("slug"), c.Param("identifier"), CurrentUser(c).ID, pageID)
	if err != nil {
		return pageError(c, err)
	}
	return c.JSON(http.StatusOK, page)
}

type patchPageBody struct {
	Title   *string `json:"title"`
	Content *string `json:"content"`
}

// updatePage implements PATCH .../pages/{pageID}. Partial semantics:
// missing keys are untouched. A title/content change records a revision
// of the pre-update state. Member (15)+.
func (h *IssueHandler) updatePage(c *echo.Context) error {
	var body patchPageBody
	if err := c.Bind(&body); err != nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body", nil)
	}
	pageID, ok := requireUUIDParam(c, "pageID", "page id")
	if !ok {
		return nil
	}
	page, err := service.UpdatePage(c.Request().Context(), h.Pool,
		c.Param("slug"), c.Param("identifier"), CurrentUser(c).ID, pageID,
		service.PagePatch{
			Title:   body.Title,
			Content: body.Content,
		})
	if err != nil {
		return pageError(c, err)
	}
	return c.JSON(http.StatusOK, page)
}

// deletePage implements DELETE .../pages/{pageID}. The whole subtree
// goes with it (documented cascade). Member (15)+.
func (h *IssueHandler) deletePage(c *echo.Context) error {
	pageID, ok := requireUUIDParam(c, "pageID", "page id")
	if !ok {
		return nil
	}
	if err := service.DeletePage(c.Request().Context(), h.Pool,
		c.Param("slug"), c.Param("identifier"), CurrentUser(c).ID, pageID); err != nil {
		return pageError(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}

// movePage implements POST .../pages/{pageID}/move
// {"parent_id": <uuid|null>, "position": <int>}. Key presence decides:
// missing parent_id keeps the parent, explicit null moves to root.
// Member (15)+.
func (h *IssueHandler) movePage(c *echo.Context) error {
	var raw map[string]json.RawMessage
	if err := c.Bind(&raw); err != nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body", nil)
	}
	pageID, ok := requireUUIDParam(c, "pageID", "page id")
	if !ok {
		return nil
	}
	var in service.PageMoveInput
	if rawParent, present := raw["parent_id"]; present {
		in.ParentIDSet = true
		if string(rawParent) != "null" {
			var pid string
			if err := json.Unmarshal(rawParent, &pid); err != nil {
				return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid parent_id", nil)
			}
			in.ParentID = &pid
		}
	}
	if rawPos, present := raw["position"]; present {
		var pos int
		if err := json.Unmarshal(rawPos, &pos); err != nil {
			return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid position", nil)
		}
		in.PositionSet = true
		in.Position = pos
	}
	page, err := service.MovePage(c.Request().Context(), h.Pool,
		c.Param("slug"), c.Param("identifier"), CurrentUser(c).ID, pageID, in)
	if err != nil {
		return pageError(c, err)
	}
	return c.JSON(http.StatusOK, page)
}

// listPageRevisions implements GET .../pages/{pageID}/revisions:
// newest first.
func (h *IssueHandler) listPageRevisions(c *echo.Context) error {
	pageID, ok := requireUUIDParam(c, "pageID", "page id")
	if !ok {
		return nil
	}
	revs, err := service.ListPageRevisions(c.Request().Context(), h.Pool,
		c.Param("slug"), c.Param("identifier"), CurrentUser(c).ID, pageID)
	if err != nil {
		return pageError(c, err)
	}
	return c.JSON(http.StatusOK, map[string]any{"revisions": revs})
}

type restorePageBody struct {
	RevisionID string `json:"revision_id"`
}

// restorePageRevision implements POST .../pages/{pageID}/restore
// {"revision_id"}. The pre-restore state is snapshotted first, so a
// restore is itself undoable. Member (15)+.
func (h *IssueHandler) restorePageRevision(c *echo.Context) error {
	var body restorePageBody
	if err := c.Bind(&body); err != nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body", nil)
	}
	pageID, ok := requireUUIDParam(c, "pageID", "page id")
	if !ok {
		return nil
	}
	page, err := service.RestorePageRevision(c.Request().Context(), h.Pool,
		c.Param("slug"), c.Param("identifier"), CurrentUser(c).ID, pageID, body.RevisionID)
	if err != nil {
		return pageError(c, err)
	}
	return c.JSON(http.StatusOK, page)
}
