package api

import (
	"errors"
	"net/http"

	"github.com/labstack/echo/v5"

	"glance/internal/service"
)

// OKR endpoints (C35T0) nested under workspaces — workspace-scoped,
// like initiatives. Every route sits behind RequireAuth — workspace
// membership is the tenancy boundary. Errors use the spec §5 envelope
// via okrError (which falls back to issueError for the shared
// sentinels: ErrNotFound for a bad slug or a non-member caller,
// ErrForbidden for guest writes, ErrIssueNotFound for an unknown or
// foreign-workspace issue, ErrNameRequired, ErrNothingToUpdate).

// RegisterObjectiveRoutes mounts the OKR endpoints. Call this before
// the SPA catch-all so API routes are never shadowed.
func RegisterObjectiveRoutes(e *echo.Echo, h *IssueHandler) {
	g := e.Group("/api/v1/workspaces/:slug/objectives", RequireAuth(h.Pool))
	g.POST("", h.createObjective)
	g.GET("", h.listObjectives)
	g.GET("/:objectiveID", h.getObjective)
	g.PATCH("/:objectiveID", h.updateObjective)
	g.DELETE("/:objectiveID", h.deleteObjective)
	g.POST("/:objectiveID/key-results", h.createKeyResult)
	g.PATCH("/:objectiveID/key-results/:krID", h.updateKeyResult)
	g.DELETE("/:objectiveID/key-results/:krID", h.deleteKeyResult)
	g.POST("/:objectiveID/key-results/:krID/issues", h.linkIssueToKeyResult)
	g.DELETE("/:objectiveID/key-results/:krID/issues/:issueID", h.unlinkIssueFromKeyResult)
}

// okrError maps OKR sentinel errors to HTTP statuses, delegating the
// shared sentinels to issueError.
func okrError(c *echo.Context, err error) error {
	switch {
	case errors.Is(err, service.ErrObjectiveNotFound):
		return WriteError(c, http.StatusNotFound, ErrCodeNotFound, "objective not found", nil)
	case errors.Is(err, service.ErrObjectiveConflict):
		return WriteError(c, http.StatusConflict, ErrCodeConflict, "objective title already exists", nil)
	case errors.Is(err, service.ErrInvalidObjective):
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid objective: bad status, date, or owner", nil)
	case errors.Is(err, service.ErrInvalidObjectiveID):
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid id", nil)
	case errors.Is(err, service.ErrKeyResultNotFound):
		return WriteError(c, http.StatusNotFound, ErrCodeNotFound, "key result not found", nil)
	case errors.Is(err, service.ErrInvalidKeyResult):
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid key result: bad kind, zero-span target, kind change, metric-only field on an issue KR, or issue link to a metric KR", nil)
	default:
		return issueError(c, err)
	}
}

type objectiveBody struct {
	Title       string  `json:"title"`
	Description *string `json:"description"`
	OwnerID     *string `json:"owner_id"`
	StartDate   *string `json:"start_date"`
	TargetDate  *string `json:"target_date"`
	Status      string  `json:"status"`
}

// createObjective implements POST
// /api/v1/workspaces/{slug}/objectives. Member (15)+; the service
// enforces it.
func (h *IssueHandler) createObjective(c *echo.Context) error {
	var body objectiveBody
	if err := c.Bind(&body); err != nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body", nil)
	}
	ob, err := service.CreateObjective(c.Request().Context(), h.Pool,
		c.Param("slug"), CurrentUser(c).ID,
		service.ObjectiveInput{
			Title:       body.Title,
			Description: body.Description,
			OwnerID:     body.OwnerID,
			StartDate:   body.StartDate,
			TargetDate:  body.TargetDate,
			Status:      body.Status,
		})
	if err != nil {
		return okrError(c, err)
	}
	return c.JSON(http.StatusCreated, ob)
}

// listObjectives implements GET /api/v1/workspaces/{slug}/objectives.
func (h *IssueHandler) listObjectives(c *echo.Context) error {
	obs, err := service.ListObjectives(c.Request().Context(), h.Pool,
		c.Param("slug"), CurrentUser(c).ID)
	if err != nil {
		return okrError(c, err)
	}
	return c.JSON(http.StatusOK, map[string]any{"objectives": obs})
}

// getObjective implements GET .../objectives/{objectiveID}: the
// objective with its KRs (per-KR progress; issue KRs carry linked
// issues).
func (h *IssueHandler) getObjective(c *echo.Context) error {
	objectiveID, ok := requireUUIDParam(c, "objectiveID", "objective id")
	if !ok {
		return nil
	}
	ob, err := service.GetObjective(c.Request().Context(), h.Pool,
		c.Param("slug"), CurrentUser(c).ID, objectiveID)
	if err != nil {
		return okrError(c, err)
	}
	return c.JSON(http.StatusOK, ob)
}

type patchObjectiveBody struct {
	Title       *string `json:"title"`
	Description *string `json:"description"`
	OwnerID     *string `json:"owner_id"`
	StartDate   *string `json:"start_date"`
	TargetDate  *string `json:"target_date"`
	Status      *string `json:"status"`
}

// updateObjective implements PATCH .../objectives/{objectiveID}.
// Partial semantics: missing keys are untouched, "" clears
// description / owner / dates, any other value sets it. Member (15)+.
func (h *IssueHandler) updateObjective(c *echo.Context) error {
	var body patchObjectiveBody
	if err := c.Bind(&body); err != nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body", nil)
	}
	objectiveID, ok := requireUUIDParam(c, "objectiveID", "objective id")
	if !ok {
		return nil
	}
	ob, err := service.UpdateObjective(c.Request().Context(), h.Pool,
		c.Param("slug"), CurrentUser(c).ID, objectiveID,
		service.ObjectivePatch{
			Title:       body.Title,
			Description: body.Description,
			OwnerID:     body.OwnerID,
			StartDate:   body.StartDate,
			TargetDate:  body.TargetDate,
			Status:      body.Status,
		})
	if err != nil {
		return okrError(c, err)
	}
	return c.JSON(http.StatusOK, ob)
}

// deleteObjective implements DELETE .../objectives/{objectiveID}.
// KRs + links cascade; linked issues are untouched. Member (15)+.
func (h *IssueHandler) deleteObjective(c *echo.Context) error {
	objectiveID, ok := requireUUIDParam(c, "objectiveID", "objective id")
	if !ok {
		return nil
	}
	if err := service.DeleteObjective(c.Request().Context(), h.Pool,
		c.Param("slug"), CurrentUser(c).ID, objectiveID); err != nil {
		return okrError(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}

type keyResultBody struct {
	Title        string   `json:"title"`
	Kind         string   `json:"kind"`
	Unit         *string  `json:"unit"`
	StartValue   *float64 `json:"start_value"`
	TargetValue  *float64 `json:"target_value"`
	CurrentValue *float64 `json:"current_value"`
}

// createKeyResult implements POST
// .../objectives/{objectiveID}/key-results. Member (15)+.
func (h *IssueHandler) createKeyResult(c *echo.Context) error {
	var body keyResultBody
	if err := c.Bind(&body); err != nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body", nil)
	}
	objectiveID, ok := requireUUIDParam(c, "objectiveID", "objective id")
	if !ok {
		return nil
	}
	kr, err := service.CreateKeyResult(c.Request().Context(), h.Pool,
		c.Param("slug"), CurrentUser(c).ID, objectiveID,
		service.KeyResultInput{
			Title:        body.Title,
			Kind:         body.Kind,
			Unit:         body.Unit,
			StartValue:   body.StartValue,
			TargetValue:  body.TargetValue,
			CurrentValue: body.CurrentValue,
		})
	if err != nil {
		return okrError(c, err)
	}
	return c.JSON(http.StatusCreated, kr)
}

type patchKeyResultBody struct {
	Title        *string  `json:"title"`
	Unit         *string  `json:"unit"`
	TargetValue  *float64 `json:"target_value"`
	CurrentValue *float64 `json:"current_value"`
	Kind         *string  `json:"kind"`
}

// updateKeyResult implements PATCH
// .../objectives/{objectiveID}/key-results/{krID}. Kind is immutable:
// a present kind key is 400. Member (15)+.
func (h *IssueHandler) updateKeyResult(c *echo.Context) error {
	var body patchKeyResultBody
	if err := c.Bind(&body); err != nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body", nil)
	}
	objectiveID, ok := requireUUIDParam(c, "objectiveID", "objective id")
	if !ok {
		return nil
	}
	krID, ok := requireUUIDParam(c, "krID", "key result id")
	if !ok {
		return nil
	}
	kr, err := service.UpdateKeyResult(c.Request().Context(), h.Pool,
		c.Param("slug"), CurrentUser(c).ID, objectiveID, krID,
		service.KeyResultPatch{
			Title:        body.Title,
			Unit:         body.Unit,
			TargetValue:  body.TargetValue,
			CurrentValue: body.CurrentValue,
			Kind:         body.Kind,
		})
	if err != nil {
		return okrError(c, err)
	}
	return c.JSON(http.StatusOK, kr)
}

// deleteKeyResult implements DELETE
// .../objectives/{objectiveID}/key-results/{krID}. Links cascade;
// linked issues are untouched. Member (15)+.
func (h *IssueHandler) deleteKeyResult(c *echo.Context) error {
	objectiveID, ok := requireUUIDParam(c, "objectiveID", "objective id")
	if !ok {
		return nil
	}
	krID, ok := requireUUIDParam(c, "krID", "key result id")
	if !ok {
		return nil
	}
	if err := service.DeleteKeyResult(c.Request().Context(), h.Pool,
		c.Param("slug"), CurrentUser(c).ID, objectiveID, krID); err != nil {
		return okrError(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}

type linkIssueBody struct {
	IssueID string `json:"issue_id"`
}

// linkIssueToKeyResult implements POST
// .../key-results/{krID}/issues {issue_id}. Idempotent: repeats are
// no-ops. Issue KRs only; cross-workspace issues are 404. Member
// (15)+.
func (h *IssueHandler) linkIssueToKeyResult(c *echo.Context) error {
	var body linkIssueBody
	if err := c.Bind(&body); err != nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body", nil)
	}
	objectiveID, ok := requireUUIDParam(c, "objectiveID", "objective id")
	if !ok {
		return nil
	}
	krID, ok := requireUUIDParam(c, "krID", "key result id")
	if !ok {
		return nil
	}
	if err := service.LinkIssueToKeyResult(c.Request().Context(), h.Pool,
		c.Param("slug"), CurrentUser(c).ID, objectiveID, krID, body.IssueID); err != nil {
		return okrError(c, err)
	}
	return c.JSON(http.StatusCreated, map[string]any{"linked": body.IssueID})
}

// unlinkIssueFromKeyResult implements DELETE
// .../key-results/{krID}/issues/{issueID}. Idempotent: removing a
// non-link is a no-op. Member (15)+.
func (h *IssueHandler) unlinkIssueFromKeyResult(c *echo.Context) error {
	objectiveID, ok := requireUUIDParam(c, "objectiveID", "objective id")
	if !ok {
		return nil
	}
	krID, ok := requireUUIDParam(c, "krID", "key result id")
	if !ok {
		return nil
	}
	issueID, ok := requireUUIDParam(c, "issueID", "issue id")
	if !ok {
		return nil
	}
	if err := service.UnlinkIssueFromKeyResult(c.Request().Context(), h.Pool,
		c.Param("slug"), CurrentUser(c).ID, objectiveID, krID, issueID); err != nil {
		return okrError(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}
