// Command glance is the glance issue tracker server: a single Go binary
// serving the API and the embedded React SPA.
package main

import (
	"context"
	"io/fs"
	"log"
	"net/http"

	"github.com/labstack/echo/v5"

	"glance/internal/api"
	"glance/internal/config"
	"glance/internal/store"
	migrationsfs "glance/migrations"
)

func main() {
	ctx := context.Background()

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("glance: %v", err)
	}

	pool, err := store.NewPool(ctx, cfg)
	if err != nil {
		log.Fatalf("glance: %v", err)
	}
	defer pool.Close()

	migrationsFS, err := fs.Sub(migrationsfs.FS, ".")
	if err != nil {
		log.Fatalf("glance: migrations fs: %v", err)
	}
	if err := store.Migrate(ctx, pool, migrationsFS); err != nil {
		log.Fatalf("glance: %v", err)
	}

	e := echo.New()
	e.GET("/health", func(c *echo.Context) error {
		return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
	})

	// /api/v1/* routes land here (Tasks 5+). /ws lands here (Task 24).
	authHandler := &api.AuthHandler{Pool: pool, Config: cfg}
	api.RegisterAuthRoutes(e, authHandler)

	if cfg.OTPPepper == "" {
		log.Println("glance: WARNING: OTP_PEPPER is not set — OTP code hashes are weaker without it; set it in production")
	}
	if cfg.OAuthStateSecret == "" {
		log.Println("glance: WARNING: OAUTH_STATE_SECRET is not set — OAuth login endpoints will refuse to operate; set it to enable Google/GitHub login")
	}

	// SPA catch-all goes LAST so it never shadows API or health routes.
	if err := api.RegisterSPA(e); err != nil {
		log.Fatalf("glance: spa: %v", err)
	}

	if err := e.Start(":" + cfg.Port); err != nil && err != http.ErrServerClosed {
		log.Fatalf("glance: server error: %v", err)
	}
}
