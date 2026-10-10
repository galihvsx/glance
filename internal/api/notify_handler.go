package api

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v5"

	"glance/internal/service"
)

// NotifyHandler serves the notification and webhook endpoints (Task 26).
// Every route sits behind RequireAuth. Webhook CRUD is workspace-admin-only
// (role 20): webhooks exfiltrate workspace data to third parties and hold
// signing secrets — a deliberate choice, documented in service/webhook.go.
type NotifyHandler struct {
	Pool *pgxpool.Pool
}

// RegisterNotifyRoutes mounts the notification + webhook endpoints. Call
// before the SPA catch-all.
func RegisterNotifyRoutes(e *echo.Echo, h *NotifyHandler) {
	g := e.Group("/api/v1", RequireAuth(h.Pool))
	g.GET("/notifications", h.listNotifications)
	g.POST("/notifications/read", h.markAllNotificationsRead)
	g.POST("/notifications/:id/read", h.markNotificationRead)
	g.GET("/notification-prefs", h.listNotificationPrefs)
	g.PUT("/notification-prefs/:event", h.setNotificationPref)
	// C11T2: digest schedule (frequency + send-after hour). The schedule
	// rides notification_prefs via value-encoded keys (see
	// service/digest_schedule.go), managed only through these endpoints —
	// the generic pref endpoints reject the schedule keys.
	g.GET("/digest-schedule", h.getDigestSchedule)
	g.PUT("/digest-schedule", h.setDigestSchedule)

	w := e.Group("/api/v1/workspaces/:slug/webhooks", RequireAuth(h.Pool))
	w.POST("", h.createWebhook)
	w.GET("", h.listWebhooks)
	w.GET("/:id", h.getWebhook)
	w.PATCH("/:id", h.updateWebhook)
	w.DELETE("/:id", h.deleteWebhook)
}

// notifyError maps service sentinel errors to HTTP statuses. Unknown
// errors are 500 with no detail leaked.
func notifyError(c *echo.Context, err error) error {
	switch {
	case errors.Is(err, service.ErrNotificationNotFound):
		return WriteError(c, http.StatusNotFound, ErrCodeNotFound, "notification not found", nil)
	case errors.Is(err, service.ErrUnknownNotifyEvent):
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "unknown notification event", nil)
	case errors.Is(err, service.ErrBadDigestFrequency):
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "digest frequency must be daily or weekly", nil)
	case errors.Is(err, service.ErrBadDigestHour):
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "digest hour must be 0-23", nil)
	case errors.Is(err, service.ErrWebhookNotFound):
		return WriteError(c, http.StatusNotFound, ErrCodeNotFound, "webhook not found", nil)
	case errors.Is(err, service.ErrBadWebhookURL):
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "webhook url must be http(s)", nil)
	case errors.Is(err, service.ErrUnknownWebhookEvent):
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "unknown webhook event", nil)
	case errors.Is(err, service.ErrNothingToUpdate):
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "nothing to update", nil)
	case errors.Is(err, service.ErrForbidden):
		return WriteError(c, http.StatusForbidden, ErrCodeForbidden, "forbidden", nil)
	case errors.Is(err, service.ErrNotFound):
		return WriteError(c, http.StatusNotFound, ErrCodeNotFound, "workspace not found", nil)
	default:
		return WriteInternalError(c)
	}
}

func (h *NotifyHandler) listNotifications(c *echo.Context) error {
	u := CurrentUser(c)
	if u == nil {
		return WriteError(c, http.StatusUnauthorized, ErrCodeUnauthorized, "unauthorized", nil)
	}
	unreadOnly := c.QueryParam("unread_only") == "true"
	limit := 25
	if raw := c.QueryParam("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n <= 0 {
			return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid limit", nil)
		}
		limit = n
	}
	notifs, unread, err := service.ListNotifications(c.Request().Context(), h.Pool, u.ID, unreadOnly, limit)
	if err != nil {
		return notifyError(c, err)
	}
	return c.JSON(http.StatusOK, map[string]any{
		"notifications": notifs,
		"unread_count":  unread,
	})
}

func (h *NotifyHandler) markNotificationRead(c *echo.Context) error {
	u := CurrentUser(c)
	if u == nil {
		return WriteError(c, http.StatusUnauthorized, ErrCodeUnauthorized, "unauthorized", nil)
	}
	id, ok := requireUUIDParam(c, "id", "notification id")
	if !ok {
		return nil
	}
	if err := service.MarkNotificationRead(c.Request().Context(), h.Pool, u.ID, id); err != nil {
		return notifyError(c, err)
	}
	return c.JSON(http.StatusOK, map[string]bool{"ok": true})
}

func (h *NotifyHandler) markAllNotificationsRead(c *echo.Context) error {
	u := CurrentUser(c)
	if u == nil {
		return WriteError(c, http.StatusUnauthorized, ErrCodeUnauthorized, "unauthorized", nil)
	}
	if err := service.MarkAllNotificationsRead(c.Request().Context(), h.Pool, u.ID); err != nil {
		return notifyError(c, err)
	}
	return c.JSON(http.StatusOK, map[string]bool{"ok": true})
}

func (h *NotifyHandler) listNotificationPrefs(c *echo.Context) error {
	u := CurrentUser(c)
	if u == nil {
		return WriteError(c, http.StatusUnauthorized, ErrCodeUnauthorized, "unauthorized", nil)
	}
	prefs, err := service.ListNotificationPrefs(c.Request().Context(), h.Pool, u.ID)
	if err != nil {
		return notifyError(c, err)
	}
	return c.JSON(http.StatusOK, map[string]any{"prefs": prefs})
}

type setPrefBody struct {
	InApp bool `json:"in_app"`
	Email bool `json:"email"`
}

func (h *NotifyHandler) setNotificationPref(c *echo.Context) error {
	u := CurrentUser(c)
	if u == nil {
		return WriteError(c, http.StatusUnauthorized, ErrCodeUnauthorized, "unauthorized", nil)
	}
	var b setPrefBody
	if err := c.Bind(&b); err != nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid body", nil)
	}
	p, err := service.SetNotificationPref(c.Request().Context(), h.Pool, u.ID, c.Param("event"), b.InApp, b.Email)
	if err != nil {
		return notifyError(c, err)
	}
	return c.JSON(http.StatusOK, p)
}

// getDigestSchedule returns the caller's digest cadence (C11T2).
// server_tz names the zone the hour is interpreted in — the glance
// server's local zone. Per-user timezones are future work; the UI shows
// this value so the caveat is honest, not buried.
func (h *NotifyHandler) getDigestSchedule(c *echo.Context) error {
	u := CurrentUser(c)
	if u == nil {
		return WriteError(c, http.StatusUnauthorized, ErrCodeUnauthorized, "unauthorized", nil)
	}
	sched, err := service.GetDigestSchedule(c.Request().Context(), h.Pool, u.ID)
	if err != nil {
		return WriteInternalError(c)
	}
	return c.JSON(http.StatusOK, map[string]any{
		"frequency": sched.Frequency,
		"hour":      sched.Hour,
		"server_tz": time.Local.String(),
	})
}

type digestScheduleBody struct {
	Frequency string `json:"frequency"`
	Hour      int    `json:"hour"`
}

// setDigestSchedule stores the caller's digest cadence (C11T2). Invalid
// frequency/hour → 400; the stored schedule is untouched on rejection.
func (h *NotifyHandler) setDigestSchedule(c *echo.Context) error {
	u := CurrentUser(c)
	if u == nil {
		return WriteError(c, http.StatusUnauthorized, ErrCodeUnauthorized, "unauthorized", nil)
	}
	var b digestScheduleBody
	if err := c.Bind(&b); err != nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid body", nil)
	}
	sched, err := service.SetDigestSchedule(c.Request().Context(), h.Pool, u.ID, b.Frequency, b.Hour)
	if err != nil {
		return notifyError(c, err)
	}
	return c.JSON(http.StatusOK, sched)
}

type webhookBody struct {
	URL    string   `json:"url"`
	Secret *string  `json:"secret"`
	Events []string `json:"events"`
	Active *bool    `json:"active"`
}

func (h *NotifyHandler) createWebhook(c *echo.Context) error {
	u := CurrentUser(c)
	if u == nil {
		return WriteError(c, http.StatusUnauthorized, ErrCodeUnauthorized, "unauthorized", nil)
	}
	var b webhookBody
	if err := c.Bind(&b); err != nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid body", nil)
	}
	if b.URL == "" {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "url is required", nil)
	}
	w, err := service.CreateWebhook(c.Request().Context(), h.Pool, c.Param("slug"), u.ID, service.WebhookInput{
		URL:    b.URL,
		Secret: b.Secret,
		Events: b.Events,
		Active: b.Active,
	})
	if err != nil {
		return notifyError(c, err)
	}
	// The 201 is the only response that reveals the signing secret
	// (show-on-create); list/get/update omit it.
	return c.JSON(http.StatusCreated, w.CreateResponse())
}

func (h *NotifyHandler) listWebhooks(c *echo.Context) error {
	u := CurrentUser(c)
	if u == nil {
		return WriteError(c, http.StatusUnauthorized, ErrCodeUnauthorized, "unauthorized", nil)
	}
	hooks, err := service.ListWebhooks(c.Request().Context(), h.Pool, c.Param("slug"), u.ID)
	if err != nil {
		return notifyError(c, err)
	}
	return c.JSON(http.StatusOK, map[string]any{"webhooks": hooks})
}

func (h *NotifyHandler) getWebhook(c *echo.Context) error {
	u := CurrentUser(c)
	if u == nil {
		return WriteError(c, http.StatusUnauthorized, ErrCodeUnauthorized, "unauthorized", nil)
	}
	id, ok := requireUUIDParam(c, "id", "webhook id")
	if !ok {
		return nil
	}
	w, err := service.GetWebhook(c.Request().Context(), h.Pool, c.Param("slug"), u.ID, id)
	if err != nil {
		return notifyError(c, err)
	}
	return c.JSON(http.StatusOK, w)
}

func (h *NotifyHandler) updateWebhook(c *echo.Context) error {
	u := CurrentUser(c)
	if u == nil {
		return WriteError(c, http.StatusUnauthorized, ErrCodeUnauthorized, "unauthorized", nil)
	}
	var b webhookBody
	if err := c.Bind(&b); err != nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid body", nil)
	}
	patch := service.WebhookPatch{Secret: b.Secret, Events: b.Events, Active: b.Active}
	if b.URL != "" {
		patch.URL = &b.URL
	}
	id, ok := requireUUIDParam(c, "id", "webhook id")
	if !ok {
		return nil
	}
	w, err := service.UpdateWebhook(c.Request().Context(), h.Pool, c.Param("slug"), u.ID, id, patch)
	if err != nil {
		return notifyError(c, err)
	}
	return c.JSON(http.StatusOK, w)
}

func (h *NotifyHandler) deleteWebhook(c *echo.Context) error {
	u := CurrentUser(c)
	if u == nil {
		return WriteError(c, http.StatusUnauthorized, ErrCodeUnauthorized, "unauthorized", nil)
	}
	id, ok := requireUUIDParam(c, "id", "webhook id")
	if !ok {
		return nil
	}
	if err := service.DeleteWebhook(c.Request().Context(), h.Pool, c.Param("slug"), u.ID, id); err != nil {
		return notifyError(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}
