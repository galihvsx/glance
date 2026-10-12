package api

import (
	"net/http"

	"github.com/labstack/echo/v5"

	"glance/internal/service"
)

// Page reactions (C24T3): the page analogue of the issue-reaction
// endpoints. Routes (nested under the page group in
// RegisterPageRoutes):
//
//	POST   .../pages/{pageID}/reactions?emoji=<emoji> — idempotent add
//	DELETE .../pages/{pageID}/reactions?emoji=<emoji> — idempotent remove
//
// The read side is the embedded reactions[] on GET page detail, shaped
// exactly like the issue reaction groups (emoji/count/reacted).
// Authz: viewer (10)+ reads (via the page detail surface); writes need
// member (15)+, like page comments. ErrInvalidReaction maps to 400 via
// pageError → issueError.

// addPageReaction implements POST .../pages/{pageID}/reactions?emoji=:
// idempotent add. Member (15)+.
func (h *IssueHandler) addPageReaction(c *echo.Context) error {
	pageID, ok := requireUUIDParam(c, "pageID", "page id")
	if !ok {
		return nil
	}
	if err := service.AddPageReaction(c.Request().Context(), h.Pool,
		c.Param("slug"), c.Param("identifier"), pageID,
		CurrentUser(c).ID, c.QueryParam("emoji")); err != nil {
		return pageError(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}

// removePageReaction implements DELETE
// .../pages/{pageID}/reactions?emoji=: idempotent remove. Member (15)+.
func (h *IssueHandler) removePageReaction(c *echo.Context) error {
	pageID, ok := requireUUIDParam(c, "pageID", "page id")
	if !ok {
		return nil
	}
	if err := service.RemovePageReaction(c.Request().Context(), h.Pool,
		c.Param("slug"), c.Param("identifier"), pageID,
		CurrentUser(c).ID, c.QueryParam("emoji")); err != nil {
		return pageError(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}
