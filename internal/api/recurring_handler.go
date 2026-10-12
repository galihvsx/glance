package api

// Recurring issue schedule endpoints (C28T2).
//
//   GET    /api/v1/workspaces/:slug/projects/:identifier/schedules          — list (any role)
//   POST   /api/v1/workspaces/:slug/projects/:identifier/schedules          — create. Member (15)+.
//   GET    /api/v1/workspaces/:slug/projects/:identifier/schedules/:scheduleID
//   PATCH  /api/v1/workspaces/:slug/projects/:identifier/schedules/:scheduleID — partial update. Member (15)+.
//   DELETE /api/v1/workspaces/:slug/projects/:identifier/schedules/:scheduleID — 204. Member (15)+.
//   POST   .../schedules/:scheduleID/run-now — fire the next occurrence
//     now (the caller becomes the issue's actor). Member (15)+.
//     200 {issue, schedule} with the created issue and the updated schedule.
//
// PATCH tri-state: the nullable day_of_month/ends_on/
// description_template/state_id are decoded as *json.RawMessage — absent
// means untouched, explicit JSON null clears, a value sets.
//
// Error codes follow the api envelope conventions:
// ErrScheduleNotFound → 404 schedule_not_found; ErrInvalidSchedule /
// ErrInvalidScheduleID / ErrNothingToUpdate → 400 bad_request; the shared
// tenancy sentinels (ErrNotFound → workspace 404, ErrProjectNotFound →
// project 404, ErrForbidden → 403) delegate to projectError.

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/labstack/echo/v5"

	"glance/internal/service"
)

// scheduleMaxBytes caps the schedule JSON body: 64KB is generous for
// template fields (no files ride these endpoints).
const scheduleMaxBytes = 64 << 10

// RegisterRecurringRoutes mounts the schedule endpoints on the project
// group. Call this before the SPA catch-all so API routes are never
// shadowed.
func RegisterRecurringRoutes(e *echo.Echo, h *ProjectHandler) {
	g := e.Group("/api/v1/workspaces/:slug/projects/:identifier/schedules", RequireAuth(h.Pool))
	g.GET("", h.listSchedules)
	g.POST("", h.createSchedule)
	g.GET("/:scheduleID", h.getSchedule)
	g.PATCH("/:scheduleID", h.updateSchedule)
	g.DELETE("/:scheduleID", h.deleteSchedule)
	g.POST("/:scheduleID/run-now", h.runScheduleNow)
}

// recurringError maps schedule sentinel errors to HTTP statuses,
// delegating the shared tenancy sentinels to projectError. A
// *service.ScheduleStaleRef means ExecuteSchedule deactivated the
// schedule (stale label/assignee/actor) — a 400 naming the cause,
// never a 500.
func recurringError(c *echo.Context, err error) error {
	switch {
	case errors.Is(err, service.ErrScheduleNotFound):
		return WriteError(c, http.StatusNotFound, ErrCodeScheduleNotFound, "schedule not found", nil)
	case errors.Is(err, service.ErrInvalidSchedule),
		errors.Is(err, service.ErrInvalidScheduleID),
		errors.Is(err, service.ErrNothingToUpdate):
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, err.Error(), nil)
	default:
		var stale *service.ScheduleStaleRef
		if errors.As(err, &stale) {
			return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest,
				"schedule paused: "+stale.Error(), nil)
		}
		return projectError(c, err)
	}
}

// requireScheduleMember enforces member (15)+ on the workspace named by
// :slug (slug or UUID, mirroring the service tenancy). A bad slug or a
// non-member caller is a 404 (never a leak); a guest is a 403. true
// means the check passed; false means the error response was already
// written and the caller must return nil without touching the response
// again (the decodeScheduleJSON pattern: WriteError answers with nil
// on a successful write, so its return cannot double as an error
// sentinel — returning it as error and testing err != nil lets the
// handler fall through and write the response twice).
func requireScheduleMember(c *echo.Context, h *ProjectHandler) bool {
	var role int
	err := h.Pool.QueryRow(c.Request().Context(),
		`SELECT m.role
		   FROM workspaces w
		   JOIN workspace_members m ON m.workspace_id = w.id
		  WHERE (w.slug = $1 OR w.id::text = $1)
		    AND m.user_id = $2::uuid`,
		c.Param("slug"), CurrentUser(c).ID).Scan(&role)
	if err != nil {
		WriteError(c, http.StatusNotFound, ErrCodeNotFound, "workspace not found", nil)
		return false
	}
	if role < service.RoleMember {
		WriteError(c, http.StatusForbidden, ErrCodeForbidden, "forbidden", nil)
		return false
	}
	return true
}

// ---------- list ----------

// listSchedules implements GET .../schedules: the project's schedules
// ordered by creation. Any role may read. The array is never null.
func (h *ProjectHandler) listSchedules(c *echo.Context) error {
	schedules, err := service.ListSchedules(c.Request().Context(), h.Pool,
		c.Param("slug"), c.Param("identifier"), CurrentUser(c).ID)
	if err != nil {
		return recurringError(c, err)
	}
	return c.JSON(http.StatusOK, schedules)
}

// ---------- get ----------

// getSchedule implements GET .../schedules/:scheduleID. Any role may
// read.
func (h *ProjectHandler) getSchedule(c *echo.Context) error {
	s, err := service.GetSchedule(c.Request().Context(), h.Pool,
		c.Param("slug"), c.Param("identifier"), c.Param("scheduleID"), CurrentUser(c).ID)
	if err != nil {
		return recurringError(c, err)
	}
	return c.JSON(http.StatusOK, s)
}

// ---------- create ----------

// createScheduleBody is the POST body for a schedule. Interval 0 means
// "unset" → defaults to 1; starts_on/ends_on are YYYY-MM-DD dates;
// hour is 0..23 UTC.
type createScheduleBody struct {
	Name                string   `json:"name"`
	Active              *bool    `json:"active"`
	Frequency           string   `json:"frequency"`
	Interval            int      `json:"interval"`
	DaysOfWeek          []int    `json:"days_of_week"`
	DayOfMonth          *int     `json:"day_of_month"`
	Hour                int      `json:"hour"`
	StartsOn            string   `json:"starts_on"`
	EndsOn              *string  `json:"ends_on"`
	TitleTemplate       string   `json:"title_template"`
	DescriptionTemplate *string  `json:"description_template"`
	StateID             *string  `json:"state_id"`
	Priority            int      `json:"priority"`
	LabelIDs            []string `json:"label_ids"`
	AssigneeIDs         []string `json:"assignee_ids"`
}

// decodeScheduleJSON reads a JSON body under the size cap.
func decodeScheduleJSON(c *echo.Context, dst any) bool {
	r := c.Request()
	r.Body = http.MaxBytesReader(c.Response(), r.Body, scheduleMaxBytes)
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		WriteError(c, http.StatusBadRequest, ErrCodeBadRequest,
			"request body must be valid JSON", nil)
		return false
	}
	return true
}

// createSchedule implements POST .../schedules. Member (15)+.
func (h *ProjectHandler) createSchedule(c *echo.Context) error {
	var body createScheduleBody
	if !decodeScheduleJSON(c, &body) {
		return nil
	}
	startsOn, err := parseDateBody("starts_on", body.StartsOn)
	if err != nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest,
			"invalid starts_on: want YYYY-MM-DD", nil)
	}
	var endsOn *time.Time
	if body.EndsOn != nil {
		endsOn, err = parseDateBody("ends_on", *body.EndsOn)
		if err != nil {
			return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest,
				"invalid ends_on: want YYYY-MM-DD", nil)
		}
	}
	s, err := service.CreateSchedule(c.Request().Context(), h.Pool,
		c.Param("slug"), c.Param("identifier"),
		service.CreateScheduleInput{
			Name:                body.Name,
			Active:              body.Active,
			Frequency:           body.Frequency,
			Interval:            body.Interval,
			DaysOfWeek:          body.DaysOfWeek,
			DayOfMonth:          body.DayOfMonth,
			Hour:                body.Hour,
			StartsOn:            timeOrNow(startsOn),
			EndsOn:              endsOn,
			TitleTemplate:       body.TitleTemplate,
			DescriptionTemplate: body.DescriptionTemplate,
			StateID:             body.StateID,
			Priority:            body.Priority,
			LabelIDs:            body.LabelIDs,
			AssigneeIDs:         body.AssigneeIDs,
		}, CurrentUser(c).ID)
	if err != nil {
		return recurringError(c, err)
	}
	return c.JSON(http.StatusCreated, s)
}

// timeOrNow dereferences a *time.Time; nil is left zero for the service
// to reject (starts_on is required).
func timeOrNow(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return *t
}

// ---------- update ----------

// triStateRaw is a JSON value that remembers whether its key was
// present, so PATCH can distinguish absent (untouched) from explicit
// null (clear) from a value (set). encoding/json would otherwise fold a
// JSON null into a nil *json.RawMessage — indistinguishable from an
// absent key.
type triStateRaw struct {
	present bool
	raw     json.RawMessage
}

// UnmarshalJSON marks the key present and keeps the raw bytes.
func (t *triStateRaw) UnmarshalJSON(data []byte) error {
	t.present = true
	t.raw = append([]byte(nil), data...)
	return nil
}

// updateScheduleBody is the PATCH body. Nullable fields decode as
// triStateRaw (value, not pointer — a pointer field would swallow JSON
// null before UnmarshalJSON ever runs): !present = untouched, JSON
// null = clear, a value = set. Plain *T fields follow the same
// absent/value semantics.
type updateScheduleBody struct {
	Name                *string     `json:"name"`
	Active              *bool       `json:"active"`
	Frequency           *string     `json:"frequency"`
	Interval            *int        `json:"interval"`
	DaysOfWeek          *[]int      `json:"days_of_week"`
	DayOfMonth          triStateRaw `json:"day_of_month"`
	Hour                *int        `json:"hour"`
	StartsOn            *string     `json:"starts_on"`
	EndsOn              triStateRaw `json:"ends_on"`
	TitleTemplate       *string     `json:"title_template"`
	DescriptionTemplate triStateRaw `json:"description_template"`
	StateID             triStateRaw `json:"state_id"`
	Priority            *int        `json:"priority"`
	LabelIDs            *[]string   `json:"label_ids"`
	AssigneeIDs         *[]string   `json:"assignee_ids"`
}

// triStateInt decodes a nullable integer: absent → untouched (nil
// **int); JSON null → clear (pointer to nil); a number → set.
func triStateInt(raw *triStateRaw) (**int, error) {
	if raw == nil || !raw.present {
		return nil, nil
	}
	if string(raw.raw) == "null" {
		var clear *int
		return &clear, nil
	}
	var v int
	if err := json.Unmarshal(raw.raw, &v); err != nil {
		return nil, err
	}
	p := &v
	return &p, nil
}

// triStateString decodes a nullable string with the same tri-state
// semantics as triStateInt.
func triStateString(raw *triStateRaw) (**string, error) {
	if raw == nil || !raw.present {
		return nil, nil
	}
	if string(raw.raw) == "null" {
		var clear *string
		return &clear, nil
	}
	var v string
	if err := json.Unmarshal(raw.raw, &v); err != nil {
		return nil, err
	}
	p := &v
	return &p, nil
}

// triStateDate decodes a nullable YYYY-MM-DD date with tri-state
// semantics.
func triStateDate(field string, raw *triStateRaw) (**time.Time, error) {
	if raw == nil || !raw.present {
		return nil, nil
	}
	if string(raw.raw) == "null" {
		var clear *time.Time
		return &clear, nil
	}
	var v string
	if err := json.Unmarshal(raw.raw, &v); err != nil {
		return nil, err
	}
	t, err := parseDateBody(field, v)
	if err != nil {
		return nil, err
	}
	if t == nil {
		return nil, errors.New("invalid " + field + ": want YYYY-MM-DD")
	}
	return &t, nil
}

// updateSchedule implements PATCH .../schedules/:scheduleID: partial
// update with tri-state semantics for the nullable fields. Member (15)+.
func (h *ProjectHandler) updateSchedule(c *echo.Context) error {
	var body updateScheduleBody
	if !decodeScheduleJSON(c, &body) {
		return nil
	}

	in := service.UpdateScheduleInput{
		Name:          body.Name,
		Active:        body.Active,
		Frequency:     body.Frequency,
		Interval:      body.Interval,
		DaysOfWeek:    body.DaysOfWeek,
		Hour:          body.Hour,
		TitleTemplate: body.TitleTemplate,
		Priority:      body.Priority,
		LabelIDs:      body.LabelIDs,
		AssigneeIDs:   body.AssigneeIDs,
	}

	var err error
	if in.DayOfMonth, err = triStateInt(&body.DayOfMonth); err != nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest,
			"invalid day_of_month: want an integer 1..31 or null", nil)
	}
	if in.EndsOn, err = triStateDate("ends_on", &body.EndsOn); err != nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest,
			"invalid ends_on: want YYYY-MM-DD or null", nil)
	}
	if in.DescriptionTemplate, err = triStateString(&body.DescriptionTemplate); err != nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest,
			"invalid description_template: want a string or null", nil)
	}
	if in.StateID, err = triStateString(&body.StateID); err != nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest,
			"invalid state_id: want a UUID or null", nil)
	}
	if body.StartsOn != nil {
		startsOn, perr := parseDateBody("starts_on", *body.StartsOn)
		if perr != nil || startsOn == nil {
			return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest,
				"invalid starts_on: want YYYY-MM-DD", nil)
		}
		in.StartsOn = startsOn
	}

	s, err := service.UpdateSchedule(c.Request().Context(), h.Pool,
		c.Param("slug"), c.Param("identifier"), c.Param("scheduleID"),
		CurrentUser(c).ID, in)
	if err != nil {
		return recurringError(c, err)
	}
	return c.JSON(http.StatusOK, s)
}

// ---------- delete ----------

// deleteSchedule implements DELETE .../schedules/:scheduleID: 204.
// Member (15)+. Already-created issues are untouched — the schedule owns
// no issue rows.
func (h *ProjectHandler) deleteSchedule(c *echo.Context) error {
	if err := service.DeleteSchedule(c.Request().Context(), h.Pool,
		c.Param("slug"), c.Param("identifier"), c.Param("scheduleID"), CurrentUser(c).ID); err != nil {
		return recurringError(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}

// ---------- run-now ----------

// scheduleRunNowResult is the POST .../run-now response: the created
// issue in the standard issue shape, plus the updated schedule.
type scheduleRunNowResult struct {
	Issue    *service.Issue             `json:"issue"`
	Schedule *service.RecurringSchedule `json:"schedule"`
}

// runScheduleNow implements POST .../schedules/:scheduleID/run-now: the
// manual "fire now" for testing a rule. It fires the schedule's next
// pending occurrence immediately (at NextRunAt, not "now") with the
// CALLING user as the issue's actor. Member (15)+. An exhausted (zero
// next_run_at) or inactive schedule is a 400.
func (h *ProjectHandler) runScheduleNow(c *echo.Context) error {
	if !requireScheduleMember(c, h) {
		return nil
	}
	ctx := c.Request().Context()
	s, err := service.GetSchedule(ctx, h.Pool,
		c.Param("slug"), c.Param("identifier"), c.Param("scheduleID"), CurrentUser(c).ID)
	if err != nil {
		return recurringError(c, err)
	}
	if !s.Active {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest,
			"schedule is not active", nil)
	}
	if s.NextRunAt.IsZero() {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest,
			"schedule is exhausted: no further runs", nil)
	}
	iss, err := service.ExecuteSchedule(ctx, h.Pool,
		s.WorkspaceID, s.ProjectID, s, s.NextRunAt, CurrentUser(c).ID)
	if err != nil {
		return recurringError(c, err)
	}
	if iss == nil {
		// Lost a race: the ticker claimed the occurrence (or the
		// schedule exhausted) between the check above and the
		// execution. Nothing was created; the client can retry.
		return WriteError(c, http.StatusConflict, ErrCodeConflict,
			"occurrence already claimed; reload the schedule and retry", nil)
	}
	updated, err := service.GetSchedule(ctx, h.Pool,
		c.Param("slug"), c.Param("identifier"), c.Param("scheduleID"), CurrentUser(c).ID)
	if err != nil {
		return recurringError(c, err)
	}
	return c.JSON(http.StatusOK, scheduleRunNowResult{Issue: iss, Schedule: updated})
}
