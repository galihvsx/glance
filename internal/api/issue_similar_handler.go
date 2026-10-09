package api

// C8T7: duplicate detection on issue create — GET .../issues/similar?q=.
// The frontend create modal calls this as the user types (debounced), so
// the response is a small flat array, not a paginated envelope.

import (
	"errors"
	"net/http"
	"strings"

	"github.com/labstack/echo/v5"

	"glance/internal/service"
)

// similarIssues implements GET
// /api/v1/workspaces/{slug}/projects/{identifier}/issues/similar:
// returns up to 5 of the project's working-set issues whose titles are
// trigram-similar (pg_trgm similarity > 0.3) to ?q=, most-similar first.
// ?q= is required (blank → 400). 200 with the array of
// {id, display_id, name, state, similarity}.
func (h *IssueHandler) similarIssues(c *echo.Context) error {
	got, err := service.FindSimilarIssues(c.Request().Context(), h.Pool, c.Param("slug"), c.Param("identifier"), CurrentUser(c).ID, strings.TrimSpace(c.QueryParam("q")))
	if err != nil {
		if errors.Is(err, service.ErrEmptySimilarQuery) {
			return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "q is required", nil)
		}
		return issueError(c, err)
	}
	return c.JSON(http.StatusOK, got)
}
