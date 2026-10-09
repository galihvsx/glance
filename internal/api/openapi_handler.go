package api

import (
	"net/http"

	"github.com/labstack/echo/v5"

	"glance/docs"
)

// RegisterOpenAPIRoutes exposes the embedded OpenAPI document. The spec
// itself is public metadata (no auth), so API consumers and tooling can
// fetch it without a session. The bytes are served verbatim: the source
// file is JSON syntax (valid YAML 1.2), so no conversion step exists to
// drift — docs/openapi.yaml IS the response body.
func RegisterOpenAPIRoutes(e *echo.Echo) {
	e.GET("/api/v1/openapi.json", func(c *echo.Context) error {
		return c.Blob(http.StatusOK, "application/json", docs.OpenAPI)
	})
}
