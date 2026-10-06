// Command glance is the glance issue tracker server: a single Go binary
// serving the API and the embedded React SPA.
package main

import (
	"log"
	"net/http"

	"github.com/labstack/echo/v5"

	"glance/internal/config"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("glance: %v", err)
	}

	e := echo.New()
	e.GET("/health", func(c *echo.Context) error {
		return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
	})

	if err := e.Start(":" + cfg.Port); err != nil && err != http.ErrServerClosed {
		log.Fatalf("glance: server error: %v", err)
	}
}
