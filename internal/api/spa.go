// Package api wires glance's HTTP handlers onto the Echo router.
package api

import (
	"io/fs"
	"net/http"
	"path"
	"strings"

	"github.com/labstack/echo/v5"

	webassets "glance/web"
)

// RegisterSPA mounts the SPA catch-all on e. Call it LAST, after
// /health, /api/* and /ws: Echo's radix tree already prefers static
// routes over wildcards, and the /api/ guard below is a second line of
// defense so the fallback can never swallow a future API route.
//
// Files that exist in the embedded dist (JS/CSS assets, favicon) are
// served directly; every other non-API GET falls back to dist/index.html
// so the React router can take over client-side.
func RegisterSPA(e *echo.Echo) error {
	dist, err := fs.Sub(webassets.Dist, "dist")
	if err != nil {
		return err
	}
	index, err := fs.ReadFile(dist, "index.html")
	if err != nil {
		return err
	}
	fileServer := http.FileServer(http.FS(dist))

	e.GET("/*", func(c *echo.Context) error {
		p := c.Request().URL.Path
		if p == "/api" || strings.HasPrefix(p, "/api/") {
			return WriteError(c, http.StatusNotFound, ErrCodeNotFound, "not found", nil)
		}
		rel := strings.TrimPrefix(path.Clean("/"+p), "/")
		if info, err := fs.Stat(dist, rel); err == nil && !info.IsDir() {
			fileServer.ServeHTTP(c.Response(), c.Request())
			return nil
		}
		return c.HTMLBlob(http.StatusOK, index)
	})
	return nil
}
