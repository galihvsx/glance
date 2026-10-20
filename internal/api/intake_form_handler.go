package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/labstack/echo/v5"

	"glance/internal/service"
)

// Intake form endpoints (C82T0): per-project shareable forms.
//
// Management routes sit under the project intake path and require the
// workspace admin role (20) — forms are a settings surface, strictly
// above the member bar for issue creation:
//
//	GET|POST   /api/v1/workspaces/:slug/projects/:identifier/intake/forms
//	GET|PATCH|DELETE .../intake/forms/:id
//	POST       .../intake/forms/:id/regenerate
//
// Public routes carry no auth — the share token is the capability.
// Unknown and disabled tokens both answer 404, never 403:
//
//	GET  /api/v1/forms/:token
//	POST /api/v1/forms/:token/submit
//
// Errors use the spec §5 envelope via intakeFormError, which maps the
// form sentinels and delegates the shared sentinels (ErrNotFound,
// ErrProjectNotFound, ErrForbidden, custom-field errors) to
// customFieldError/issueError.

// RegisterIntakeFormPublicRoutes mounts the public (NO auth) form
// endpoints. Call before the SPA catch-all so API routes are never
// shadowed.
func RegisterIntakeFormPublicRoutes(e *echo.Echo, h *IssueHandler) {
	e.GET("/api/v1/forms/:token", h.getPublicIntakeForm)
	e.POST("/api/v1/forms/:token/submit", h.submitIntakeForm)
}

// intakeFormError maps the intake-form sentinel errors to HTTP statuses.
// FormDefinitionError / FormSubmissionError are honest 400s carrying the
// service's client-facing message; ErrIntakeFormNotFound is 404; the
// custom-field errors (required/type) delegate to customFieldError, which
// falls back to issueError for the shared sentinels.
func intakeFormError(c *echo.Context, err error) error {
	var de *service.FormDefinitionError
	if errors.As(err, &de) {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, de.Message, nil)
	}
	var se *service.FormSubmissionError
	if errors.As(err, &se) {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, se.Message, nil)
	}
	if errors.Is(err, service.ErrIntakeFormNotFound) {
		return WriteError(c, http.StatusNotFound, ErrCodeNotFound, "intake form not found", nil)
	}
	return customFieldError(c, err)
}

type intakeFormFieldBody struct {
	Key      string `json:"key"`
	Label    string `json:"label"`
	Required bool   `json:"required"`
}

type intakeFormCreateBody struct {
	Name        string                `json:"name"`
	Description *string               `json:"description"`
	IsEnabled   *bool                 `json:"is_enabled"`
	Fields      []intakeFormFieldBody `json:"fields"`
}

type intakeFormPatchBody struct {
	Name        *string                    `json:"name"`
	Description service.PatchField[string] `json:"description"`
	IsEnabled   *bool                      `json:"is_enabled"`
	Fields      *[]intakeFormFieldBody     `json:"fields"`
}

func toServiceFields(in []intakeFormFieldBody) []service.IntakeFormField {
	out := make([]service.IntakeFormField, 0, len(in))
	for _, f := range in {
		out = append(out, service.IntakeFormField{Key: f.Key, Label: f.Label, Required: f.Required})
	}
	return out
}

// listIntakeForms implements GET .../intake/forms.
func (h *IssueHandler) listIntakeForms(c *echo.Context) error {
	forms, err := service.ListIntakeForms(c.Request().Context(), h.Pool,
		c.Param("slug"), c.Param("identifier"), CurrentUser(c).ID)
	if err != nil {
		return intakeFormError(c, err)
	}
	return c.JSON(http.StatusOK, map[string]any{"forms": forms})
}

// createIntakeForm implements POST .../intake/forms.
func (h *IssueHandler) createIntakeForm(c *echo.Context) error {
	var body intakeFormCreateBody
	if err := c.Bind(&body); err != nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body", nil)
	}
	f, err := service.CreateIntakeForm(c.Request().Context(), h.Pool,
		c.Param("slug"), c.Param("identifier"), CurrentUser(c).ID,
		service.IntakeFormInput{
			Name:        body.Name,
			Description: body.Description,
			IsEnabled:   body.IsEnabled,
			Fields:      toServiceFields(body.Fields),
		})
	if err != nil {
		return intakeFormError(c, err)
	}
	return c.JSON(http.StatusCreated, f)
}

// getIntakeForm implements GET .../intake/forms/:id.
func (h *IssueHandler) getIntakeForm(c *echo.Context) error {
	id, ok := requireUUIDParam(c, "id", "form id")
	if !ok {
		return nil
	}
	f, err := service.GetIntakeForm(c.Request().Context(), h.Pool,
		c.Param("slug"), c.Param("identifier"), CurrentUser(c).ID, id)
	if err != nil {
		return intakeFormError(c, err)
	}
	return c.JSON(http.StatusOK, f)
}

// updateIntakeForm implements PATCH .../intake/forms/:id.
func (h *IssueHandler) updateIntakeForm(c *echo.Context) error {
	id, ok := requireUUIDParam(c, "id", "form id")
	if !ok {
		return nil
	}
	var body intakeFormPatchBody
	if err := c.Bind(&body); err != nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body", nil)
	}
	var fields *[]service.IntakeFormField
	if body.Fields != nil {
		sf := toServiceFields(*body.Fields)
		fields = &sf
	}
	f, err := service.UpdateIntakeForm(c.Request().Context(), h.Pool,
		c.Param("slug"), c.Param("identifier"), CurrentUser(c).ID, id,
		service.IntakeFormPatch{
			Name:        body.Name,
			Description: body.Description,
			IsEnabled:   body.IsEnabled,
			Fields:      fields,
		})
	if err != nil {
		return intakeFormError(c, err)
	}
	return c.JSON(http.StatusOK, f)
}

// deleteIntakeForm implements DELETE .../intake/forms/:id.
func (h *IssueHandler) deleteIntakeForm(c *echo.Context) error {
	id, ok := requireUUIDParam(c, "id", "form id")
	if !ok {
		return nil
	}
	if err := service.DeleteIntakeForm(c.Request().Context(), h.Pool,
		c.Param("slug"), c.Param("identifier"), CurrentUser(c).ID, id); err != nil {
		return intakeFormError(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}

// regenerateIntakeFormToken implements POST .../intake/forms/:id/regenerate:
// rotates the share token — the old token is immediately invalid.
func (h *IssueHandler) regenerateIntakeFormToken(c *echo.Context) error {
	id, ok := requireUUIDParam(c, "id", "form id")
	if !ok {
		return nil
	}
	f, err := service.RegenerateIntakeFormToken(c.Request().Context(), h.Pool,
		c.Param("slug"), c.Param("identifier"), CurrentUser(c).ID, id)
	if err != nil {
		return intakeFormError(c, err)
	}
	return c.JSON(http.StatusOK, f)
}

// getPublicIntakeForm implements GET /api/v1/forms/:token — NO auth. The
// token is the capability; unknown or disabled tokens answer 404 with no
// existence leak, and the payload never carries internal ids.
func (h *IssueHandler) getPublicIntakeForm(c *echo.Context) error {
	f, err := service.GetPublicIntakeForm(c.Request().Context(), h.Pool, c.Param("token"))
	if err != nil {
		return intakeFormError(c, err)
	}
	return c.JSON(http.StatusOK, f)
}

type intakeFormSubmitBody struct {
	Values map[string]json.RawMessage `json:"values"`
}

// submitIntakeForm implements POST /api/v1/forms/:token/submit — NO auth.
// A valid submission creates the issue through the same service path as
// in-app creation (issue.created activity + webhook fan-out included) and
// lands it in the project's intake inbox as pending. 201 {issue_id}.
func (h *IssueHandler) submitIntakeForm(c *echo.Context) error {
	var body intakeFormSubmitBody
	if err := c.Bind(&body); err != nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body", nil)
	}
	iss, err := service.SubmitIntakeForm(c.Request().Context(), h.Pool, c.Param("token"), body.Values)
	if err != nil {
		return intakeFormError(c, err)
	}
	return c.JSON(http.StatusCreated, map[string]any{"issue_id": iss.ID})
}
