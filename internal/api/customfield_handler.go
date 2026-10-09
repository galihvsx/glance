package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/labstack/echo/v5"

	"glance/internal/service"
)

// Custom field endpoints (C7T2): project-scoped typed fields plus
// per-issue values. Every route sits behind RequireAuth — workspace
// membership is the tenancy boundary.
// Errors use the spec §5 envelope via customFieldError (which falls back
// to issueError for the shared sentinels).
//
// Roles: any member (guest 5+) may read fields and values; every
// mutation — field CRUD and value set/clear — needs member (15)+.

// RegisterCustomFieldRoutes mounts the custom-field endpoints. Call this
// before the SPA catch-all so API routes are never shadowed.
func RegisterCustomFieldRoutes(e *echo.Echo, h *IssueHandler) {
	g := e.Group("/api/v1/workspaces/:slug/projects/:identifier/custom-fields", RequireAuth(h.Pool))
	g.POST("", h.createCustomField)
	g.GET("", h.listCustomFields)
	g.GET("/:fieldID", h.getCustomField)
	g.PATCH("/:fieldID", h.updateCustomField)
	g.DELETE("/:fieldID", h.deleteCustomField)

	v := e.Group("/api/v1/workspaces/:slug/projects/:identifier/issues/:uuid/custom-values", RequireAuth(h.Pool))
	v.PUT("", h.setCustomValues)
	v.DELETE("/:fieldID", h.clearCustomValue)
}

// customFieldError maps custom-field sentinel errors to HTTP statuses,
// delegating the shared sentinels (ErrNotFound, ErrProjectNotFound,
// ErrIssueNotFound, ErrForbidden, ErrNameRequired, ErrNothingToUpdate)
// to issueError. A *service.CustomValueError is an honest 400 naming the
// field and exactly why the value was rejected.
func customFieldError(c *echo.Context, err error) error {
	var ve *service.CustomValueError
	if errors.As(err, &ve) {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, ve.Message, map[string]any{
			"field_id":   ve.FieldID,
			"field_name": ve.FieldName,
		})
	}
	switch {
	case errors.Is(err, service.ErrCustomFieldNotFound):
		return WriteError(c, http.StatusNotFound, ErrCodeNotFound, "custom field not found in this project", nil)
	case errors.Is(err, service.ErrCustomFieldConflict):
		return WriteError(c, http.StatusConflict, ErrCodeConflict, "custom field name already exists", nil)
	case errors.Is(err, service.ErrInvalidCustomField):
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid custom field: bad name, field_type, or options", nil)
	case errors.Is(err, service.ErrInvalidCustomFieldID):
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid field id", nil)
	case errors.Is(err, service.ErrCustomValuesEmpty):
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "values is required", nil)
	default:
		return issueError(c, err)
	}
}

type customFieldBody struct {
	Name      string          `json:"name"`
	FieldType string          `json:"field_type"`
	Options   json.RawMessage `json:"options"`
	Required  *bool           `json:"required"`
	Position  *int            `json:"position"`
}

// createCustomField implements POST
// /api/v1/workspaces/{slug}/projects/{identifier}/custom-fields.
func (h *IssueHandler) createCustomField(c *echo.Context) error {
	var body customFieldBody
	if err := c.Bind(&body); err != nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body", nil)
	}
	f, err := service.CreateCustomField(c.Request().Context(), h.Pool,
		c.Param("slug"), c.Param("identifier"), CurrentUser(c).ID,
		service.CustomFieldInput{
			Name:      body.Name,
			FieldType: body.FieldType,
			Options:   body.Options,
			Required:  body.Required,
			Position:  body.Position,
		})
	if err != nil {
		return customFieldError(c, err)
	}
	return c.JSON(http.StatusCreated, f)
}

// listCustomFields implements GET
// /api/v1/workspaces/{slug}/projects/{identifier}/custom-fields.
func (h *IssueHandler) listCustomFields(c *echo.Context) error {
	fields, err := service.ListCustomFields(c.Request().Context(), h.Pool,
		c.Param("slug"), c.Param("identifier"), CurrentUser(c).ID)
	if err != nil {
		return customFieldError(c, err)
	}
	return c.JSON(http.StatusOK, map[string]any{"custom_fields": fields})
}

// getCustomField implements GET .../custom-fields/{fieldID}.
func (h *IssueHandler) getCustomField(c *echo.Context) error {
	fieldID, ok := requireUUIDParam(c, "fieldID", "custom field id")
	if !ok {
		return nil
	}
	f, err := service.GetCustomField(c.Request().Context(), h.Pool,
		c.Param("slug"), c.Param("identifier"), CurrentUser(c).ID, fieldID)
	if err != nil {
		return customFieldError(c, err)
	}
	return c.JSON(http.StatusOK, f)
}

type customFieldPatchBody struct {
	Name      *string          `json:"name"`
	FieldType *string          `json:"field_type"`
	Options   *json.RawMessage `json:"options"`
	Required  *bool            `json:"required"`
	Position  *int             `json:"position"`
}

// hasAnyCustomFieldPatchKey reports whether the body carries at least one
// known patch key, so PATCH {} is 400 (nothing to update) rather than a
// no-op 200.
func hasAnyCustomFieldPatchKey(body customFieldPatchBody) bool {
	return body.Name != nil || body.FieldType != nil || body.Options != nil ||
		body.Required != nil || body.Position != nil
}

// updateCustomField implements PATCH .../custom-fields/{fieldID}.
func (h *IssueHandler) updateCustomField(c *echo.Context) error {
	fieldID, ok := requireUUIDParam(c, "fieldID", "custom field id")
	if !ok {
		return nil
	}
	var body customFieldPatchBody
	if err := c.Bind(&body); err != nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body", nil)
	}
	if !hasAnyCustomFieldPatchKey(body) {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "nothing to update", nil)
	}
	f, err := service.UpdateCustomField(c.Request().Context(), h.Pool,
		c.Param("slug"), c.Param("identifier"), CurrentUser(c).ID, fieldID,
		service.CustomFieldPatch{
			Name:      body.Name,
			FieldType: body.FieldType,
			Options:   body.Options,
			Required:  body.Required,
			Position:  body.Position,
		})
	if err != nil {
		return customFieldError(c, err)
	}
	return c.JSON(http.StatusOK, f)
}

// deleteCustomField implements DELETE .../custom-fields/{fieldID}. The
// field's values die with it (ON DELETE CASCADE).
func (h *IssueHandler) deleteCustomField(c *echo.Context) error {
	fieldID, ok := requireUUIDParam(c, "fieldID", "custom field id")
	if !ok {
		return nil
	}
	if err := service.DeleteCustomField(c.Request().Context(), h.Pool,
		c.Param("slug"), c.Param("identifier"), CurrentUser(c).ID, fieldID); err != nil {
		return customFieldError(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}

type setCustomValuesBody struct {
	Values map[string]json.RawMessage `json:"values"`
}

// setCustomValues implements PUT
// /api/v1/workspaces/{slug}/projects/{identifier}/issues/{uuid}/custom-values.
// Bulk-sets the issue's custom values: {values: {field_id: value}}.
// Each value is type-checked against its field (400 with an honest
// message on mismatch); a JSON null clears that field. Fields from
// another project are 404. Responds with the issue's resulting values.
func (h *IssueHandler) setCustomValues(c *echo.Context) error {
	uuid, ok := requireUUIDParam(c, "uuid", "issue id")
	if !ok {
		return nil
	}
	var body setCustomValuesBody
	if err := c.Bind(&body); err != nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body", nil)
	}
	if len(body.Values) == 0 {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "values is required", nil)
	}
	values, err := service.SetCustomValues(c.Request().Context(), h.Pool,
		c.Param("slug"), c.Param("identifier"), CurrentUser(c).ID, uuid, body.Values)
	if err != nil {
		return customFieldError(c, err)
	}
	if values == nil {
		values = map[string]service.CustomValue{}
	}
	return c.JSON(http.StatusOK, map[string]any{"custom_values": values})
}

// clearCustomValue implements DELETE
// .../issues/{uuid}/custom-values/{fieldID}: deletes one custom value.
// Clearing an unset value is a silent no-op (204 either way).
func (h *IssueHandler) clearCustomValue(c *echo.Context) error {
	uuid, ok := requireUUIDParam(c, "uuid", "issue id")
	if !ok {
		return nil
	}
	fieldID, ok := requireUUIDParam(c, "fieldID", "custom field id")
	if !ok {
		return nil
	}
	if err := service.ClearCustomValue(c.Request().Context(), h.Pool,
		c.Param("slug"), c.Param("identifier"), CurrentUser(c).ID, uuid, fieldID); err != nil {
		return customFieldError(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}
