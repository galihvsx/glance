package api

import (
	"encoding/json"
	"net/http"

	"github.com/labstack/echo/v5"

	"glance/internal/service"
)

// Page comments (C18T2): flat comment thread per wiki page.
// Routes (nested under the page group, same tenancy pattern as issue
// comments): GET/POST .../pages/{pageID}/comments, PATCH/DELETE
// .../pages/{pageID}/comments/{commentID}.
// Authz: viewer (10)+ reads, member (15)+ writes; edit/delete is the
// comment's author or a workspace admin. Bodies are TipTap JSON docs
// validated exactly like issue comment content (no new error shapes).

type pageCommentBody struct {
	Body json.RawMessage `json:"body"`
}

// listPageComments implements GET .../pages/{pageID}/comments: the
// flat comment list, oldest first. Viewer (10)+.
func (h *IssueHandler) listPageComments(c *echo.Context) error {
	pageID, ok := requireUUIDParam(c, "pageID", "page id")
	if !ok {
		return nil
	}
	comments, err := service.ListPageComments(c.Request().Context(), h.Pool,
		c.Param("slug"), c.Param("identifier"), pageID, CurrentUser(c).ID)
	if err != nil {
		return pageError(c, err)
	}
	return c.JSON(http.StatusOK, map[string]any{"comments": comments})
}

// createPageComment implements POST .../pages/{pageID}/comments
// {"body": <TipTap doc>}. Member (15)+.
func (h *IssueHandler) createPageComment(c *echo.Context) error {
	var body pageCommentBody
	if err := c.Bind(&body); err != nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body", nil)
	}
	pageID, ok := requireUUIDParam(c, "pageID", "page id")
	if !ok {
		return nil
	}
	comment, err := service.CreatePageComment(c.Request().Context(), h.Pool,
		c.Param("slug"), c.Param("identifier"), pageID, CurrentUser(c).ID, body.Body)
	if err != nil {
		return pageError(c, err)
	}
	return c.JSON(http.StatusCreated, comment)
}

// updatePageComment implements PATCH
// .../pages/{pageID}/comments/{commentID}: author or admin.
func (h *IssueHandler) updatePageComment(c *echo.Context) error {
	var body pageCommentBody
	if err := c.Bind(&body); err != nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body", nil)
	}
	pageID, ok := requireUUIDParam(c, "pageID", "page id")
	if !ok {
		return nil
	}
	commentID, ok := requireUUIDParam(c, "commentID", "comment id")
	if !ok {
		return nil
	}
	comment, err := service.UpdatePageComment(c.Request().Context(), h.Pool,
		c.Param("slug"), c.Param("identifier"), pageID, commentID, CurrentUser(c).ID, body.Body)
	if err != nil {
		return pageError(c, err)
	}
	return c.JSON(http.StatusOK, comment)
}

// deletePageComment implements DELETE
// .../pages/{pageID}/comments/{commentID}: hard delete, author or
// admin.
func (h *IssueHandler) deletePageComment(c *echo.Context) error {
	pageID, ok := requireUUIDParam(c, "pageID", "page id")
	if !ok {
		return nil
	}
	commentID, ok := requireUUIDParam(c, "commentID", "comment id")
	if !ok {
		return nil
	}
	if err := service.DeletePageComment(c.Request().Context(), h.Pool,
		c.Param("slug"), c.Param("identifier"), pageID, commentID, CurrentUser(c).ID); err != nil {
		return pageError(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}
