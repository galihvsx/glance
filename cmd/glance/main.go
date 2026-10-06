// Command glance is the glance issue tracker server: a single Go binary
// serving the API and the embedded React SPA.
package main

import (
	"context"
	"io/fs"
	"log"
	"net/http"
	"time"

	"github.com/labstack/echo/v5"

	"glance/internal/api"
	"glance/internal/config"
	"glance/internal/mail"
	"glance/internal/realtime"
	"glance/internal/service"
	"glance/internal/store"
	"glance/internal/ticker"
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

	// Mail dispatcher: drains the transactional outbox (OTP emails, webhook
	// deliveries) on a ticker. Without this the outbox rows written by
	// mail.Enqueue would sit pending forever — in particular, OTP codes
	// would never reach the user. LogSender (SMTP_HOST unset) prints each
	// message to stdout, which is how the dev login flow surfaces the code.
	mailDispatcher := mail.NewDispatcher(pool, mail.NewSender(cfg))
	go func() {
		t := time.NewTicker(5 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if err := mailDispatcher.Run(ctx); err != nil {
					log.Printf("glance: mail dispatch: %v", err)
				}
			}
		}
	}()

	// Webhook dispatcher (Task 26): drains the "webhook.%" outbox
	// namespace with HMAC-signed POSTs and exponential-backoff retry. It
	// runs on its OWN ticker goroutine — webhook endpoints can be slow or
	// down, and that must never starve the mail pass above.
	webhookDispatcher := service.NewWebhookDispatcher(pool)
	go func() {
		t := time.NewTicker(5 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if err := webhookDispatcher.Run(ctx); err != nil {
					log.Printf("glance: webhook dispatch: %v", err)
				}
			}
		}
	}()

	// Cycle rollover ticker: activates upcoming cycles whose start_date
	// has arrived and completes ended cycles (freezing the progress
	// snapshot, transferring/detaching incomplete issues per the
	// project's close_in_days rule) on a 1-minute interval. In-process by
	// design (spec §3): one instance only in v1. Start is explicit here —
	// a ticker that is built but never started is the Task 10 dispatcher
	// bug all over again.
	cycleTicker := &ticker.Ticker{Pool: pool, Interval: time.Minute}
	cycleTicker.Start(ctx)

	e := echo.New()
	e.GET("/health", func(c *echo.Context) error {
		return c.JSON(http.StatusOK, map[string]string{"status": "ok", "version": Version})
	})

	// Realtime hub (Task 24, spec §6): in-process websocket fan-out.
	// service.Realtime is the service layer's broadcast sink (ruling R1) —
	// set before any route can trigger a mutation.
	hub := realtime.NewHub()
	service.Realtime = hub

	// /api/v1/* routes land here (Tasks 5+). /ws lands here (Task 24).
	authHandler := &api.AuthHandler{Pool: pool, Config: cfg}
	api.RegisterAuthRoutes(e, authHandler)

	wsHandler := &api.WSHandler{Pool: pool, Hub: hub}
	api.RegisterWSRoutes(e, wsHandler)

	workspaceHandler := &api.WorkspaceHandler{Pool: pool}
	api.RegisterWorkspaceRoutes(e, workspaceHandler)

	projectHandler := &api.ProjectHandler{Pool: pool}
	api.RegisterProjectRoutes(e, projectHandler)

	issueHandler := &api.IssueHandler{Pool: pool}
	api.RegisterIssueRoutes(e, issueHandler)
	api.RegisterTokenRoutes(e, &api.TokenHandler{Pool: pool})
	api.RegisterNotifyRoutes(e, &api.NotifyHandler{Pool: pool})
	api.RegisterTaxonomyRoutes(e, issueHandler)
	api.RegisterSatelliteRoutes(e, issueHandler)
	api.RegisterIntakeRoutes(e, issueHandler)
	api.RegisterCycleRoutes(e, issueHandler)

	// OTP_PEPPER enforcement lives in config.Load (fail closed; the
	// ALLOW_INSECURE_OTP_PEPPER hatch warns loudly there). By this point a
	// missing pepper without the hatch has already refused to boot.
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
