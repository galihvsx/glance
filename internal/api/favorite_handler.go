package api

// Favorite endpoints (C7T4): star/unstar issues and projects, and list the
// caller's stars. All auth-only, scoped to the caller's own favorites:
// POST /api/v1/favorites {type, id} → 201 (200 when already starred),
// DELETE /api/v1/favorites {type, id} → 204 (idempotent),
// GET /api/v1/favorites → 200 {issues, projects} summaries with routing
// context (display_id / project identifier / workspace slug).

import (
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v5"

	"glance/internal/service"
)

// FavoriteHandler serves the favorites routes.
type FavoriteHandler struct {
	Pool *pgxpool.Pool
}

// RegisterFavoriteRoutes mounts the favorites endpoints. Call before the
// SPA catch-all.
func RegisterFavoriteRoutes(e *echo.Echo, h *FavoriteHandler) {
	g := e.Group("/api/v1/favorites", RequireAuth(h.Pool))
	g.POST("", h.star)
	g.DELETE("", h.unstar)
	g.GET("", h.list)
}

// favoriteBody is the POST/DELETE payload: {type: "issue"|"project", id}.
type favoriteBody struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

func validFavoriteType(t string) bool {
	return t == service.FavoriteIssue || t == service.FavoriteProject
}

// star implements POST /api/v1/favorites: stars the target for the caller.
// 201 with the favorite on a new star, 200 with the existing favorite when
// already starred. 404 when the target is missing or not visible to the
// caller (workspace membership), 400 on a bad type/id.
func (h *FavoriteHandler) star(c *echo.Context) error {
	u := CurrentUser(c)
	if u == nil {
		return WriteError(c, http.StatusUnauthorized, ErrCodeUnauthorized, "unauthorized", nil)
	}
	var b favoriteBody
	if err := c.Bind(&b); err != nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body", nil)
	}
	if !validFavoriteType(b.Type) {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest,
			"type must be one of: issue, project", nil)
	}
	if b.ID == "" {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "id is required", nil)
	}
	fav, created, err := service.StarFavorite(c.Request().Context(), h.Pool, u.ID, b.Type, b.ID)
	switch {
	case errors.Is(err, service.ErrFavoriteTargetNotFound):
		return WriteError(c, http.StatusNotFound, ErrCodeNotFound, "target not found", nil)
	case err != nil:
		return WriteInternalError(c)
	}
	if created {
		return c.JSON(http.StatusCreated, fav)
	}
	return c.JSON(http.StatusOK, fav)
}

// unstar implements DELETE /api/v1/favorites: removes the caller's star.
// Always 204 — unstarring a target that was never starred is a no-op.
func (h *FavoriteHandler) unstar(c *echo.Context) error {
	u := CurrentUser(c)
	if u == nil {
		return WriteError(c, http.StatusUnauthorized, ErrCodeUnauthorized, "unauthorized", nil)
	}
	var b favoriteBody
	if err := c.Bind(&b); err != nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body", nil)
	}
	if !validFavoriteType(b.Type) {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest,
			"type must be one of: issue, project", nil)
	}
	if b.ID == "" {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "id is required", nil)
	}
	if err := service.UnstarFavorite(c.Request().Context(), h.Pool, u.ID, b.Type, b.ID); err != nil {
		return WriteInternalError(c)
	}
	return c.NoContent(http.StatusNoContent)
}

// list implements GET /api/v1/favorites: the caller's stars as
// {issues: [...], projects: [...]} summaries. Targets the caller can no
// longer see drop out of the list.
func (h *FavoriteHandler) list(c *echo.Context) error {
	u := CurrentUser(c)
	if u == nil {
		return WriteError(c, http.StatusUnauthorized, ErrCodeUnauthorized, "unauthorized", nil)
	}
	list, err := service.ListFavorites(c.Request().Context(), h.Pool, u.ID)
	if err != nil {
		return WriteInternalError(c)
	}
	return c.JSON(http.StatusOK, list)
}
