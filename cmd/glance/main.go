// Command glance is the glance issue tracker server: a single Go binary
// serving the API and the embedded React SPA.
package main

import (
	"context"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/labstack/echo/v5"

	"glance/internal/api"
	"glance/internal/auth"
	"glance/internal/config"
	"glance/internal/mail"
	"glance/internal/realtime"
	"glance/internal/service"
	"glance/internal/store"
	"glance/internal/ticker"
	migrationsfs "glance/migrations"
)

func main() {
	// --health-check is the container HEALTHCHECK probe (distroless has no
	// shell/curl). It runs before config load on purpose: probing must
	// never require OTP_PEPPER or a database.
	if len(os.Args) > 1 && os.Args[1] == "--health-check" {
		os.Exit(runHealthCheck())
	}

	ctx := context.Background()

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("glance: %v", err)
	}

	// C2T8: the dispatchers and tickers below select on ctx.Done(), so
	// ctx must actually BE cancelled on shutdown — a bare
	// context.Background() never fires, and the goroutines would leak
	// until the process was SIGKILLed. signal.NotifyContext ties the
	// whole server lifecycle to SIGINT/SIGTERM.
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

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

	// C5T0: instance-admin bootstrap. GLANCE_ADMIN_EMAILS is applied
	// idempotently at every boot (promote-only, never demotes), so an
	// operator can always regain admin access by setting the env var and
	// restarting — no manual SQL needed. The first-ever registered user
	// is seeded at registration time instead (see
	// internal/auth.provisionUserTx).
	if n, err := auth.SeedAdminEmails(ctx, pool, cfg.AdminEmails); err != nil {
		log.Fatalf("glance: admin bootstrap: %v", err)
	} else if n > 0 {
		log.Printf("glance: admin bootstrap: promoted %d user(s) from GLANCE_ADMIN_EMAILS", n)
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

	// Snooze-expiry ticker (C2T7, spec §3): flips intake rows whose
	// snoozed_till has passed back to pending, so expired snoozes
	// resurface without any page interaction. Same in-process,
	// one-instance design as the cycle ticker.
	snoozeTicker := &ticker.SnoozeTicker{Pool: pool, Interval: time.Minute}
	snoozeTicker.Start(ctx)

	e := echo.New()

	// C2T8: proxy headers (X-Forwarded-For) feed IP-keyed rate limits via
	// c.RealIP(). They are honored ONLY from TRUSTED_PROXY_CIDRS; with
	// the env unset (default) the direct TCP peer is the client IP and
	// spoofed XFF headers are ignored.
	if len(cfg.TrustedProxyNets) > 0 {
		e.IPExtractor = api.ProxyAwareIPExtractor(cfg.TrustedProxyNets)
		log.Printf("glance: trusting X-Forwarded-For from proxies in %s", cfg.TrustedProxyCIDRs)
	}
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
	api.RegisterImportRoutes(e, projectHandler)

	issueHandler := &api.IssueHandler{Pool: pool}
	// C4T2: issue attachment file storage, rooted at
	// <GLANCE_DATA_DIR>/attachments. Fail boot if the directory cannot
	// be created — uploads must never silently go nowhere.
	attachmentStore, err := store.NewFileAttachmentStore(filepath.Join(cfg.DataDir, "attachments"))
	if err != nil {
		log.Fatalf("glance: attachment storage: %v", err)
	}
	issueHandler.Attachments = attachmentStore
	issueHandler.MaxUploadBytes = int64(cfg.MaxUploadMB) << 20
	api.RegisterIssueRoutes(e, issueHandler)
	api.RegisterWorkItemRoutes(e, issueHandler)
	api.RegisterTokenRoutes(e, &api.TokenHandler{Pool: pool})
	api.RegisterNotifyRoutes(e, &api.NotifyHandler{Pool: pool})
	api.RegisterAdminRoutes(e, &api.AdminHandler{Pool: pool})
	api.RegisterTaxonomyRoutes(e, issueHandler)
	api.RegisterStateRoutes(e, &api.StateHandler{Pool: pool})
	api.RegisterSatelliteRoutes(e, issueHandler)
	api.RegisterIntakeRoutes(e, issueHandler)
	api.RegisterIssueLinkRoutes(e, issueHandler)
	api.RegisterMyWorkRoutes(e, &api.MyWorkHandler{Pool: pool})
	api.RegisterCycleRoutes(e, issueHandler)
	api.RegisterModuleRoutes(e, issueHandler)
	api.RegisterPageRoutes(e, issueHandler)
	api.RegisterReleaseRoutes(e, issueHandler)
	api.RegisterPublicRoutes(e, issueHandler)
	api.RegisterAnalyticsRoutes(e, issueHandler)
	api.RegisterActivityRoutes(e, issueHandler)

	// C5T2: AI assist (description drafting + triage) over a
	// provider-agnostic OpenAI-compatible endpoint. Fail-open at boot:
	// without GLANCE_AI_API_KEY the endpoints answer 503
	// (ai_not_configured) instead of refusing to start the server.
	if !cfg.AI.Configured() {
		log.Println("glance: AI disabled: GLANCE_AI_API_KEY unset")
	}
	api.RegisterAIRoutes(e, &api.AIHandler{Pool: pool, AI: cfg.AI})

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

	// C2T8: graceful shutdown via the standard library server (echo v5's
	// Start has its own internal signal handling and no Shutdown method,
	// so drive http.Server directly). The signal ctx above is shared with
	// the mail/webhook dispatchers and the cycle ticker: SIGINT/SIGTERM
	// cancels it (their <-ctx.Done() branches fire), then Shutdown drains
	// in-flight requests before the process exits.
	srv := &http.Server{Addr: ":" + cfg.Port, Handler: e}
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("glance: server error: %v", err)
		}
	}()
	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("glance: shutdown: %v", err)
	}
}

// runHealthCheck probes /health on the configured port and reports 0/1.
// Used by the Docker HEALTHCHECK; kept dependency-free on purpose.
func runHealthCheck() int {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get("http://127.0.0.1:" + port + "/health")
	if err != nil {
		return 1
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 1
	}
	// A bare 200 is not enough: another service could be squatting the
	// port (seen in the wild). Require glance's health body.
	body, err := io.ReadAll(io.LimitReader(resp.Body, 256))
	if err != nil {
		return 1
	}
	if !strings.Contains(string(body), `"status":"ok"`) {
		return 1
	}
	return 0
}
