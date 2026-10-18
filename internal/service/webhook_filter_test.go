package service

// Webhook event filters (C69T0): validation, matching, and enqueue-time
// filtering. All tests run against the real test database — no skips.

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// filterFixture is a workspace + project + issue with deterministic
// filterable attributes: priority high(3), state in the "started" group,
// one assignee, one label, target_date 2026-10-25.
type filterFixture struct {
	actorID    string
	wsSlug     string
	ident      string
	issueID    string
	projectID  string
	stateID    string
	assigneeID string
	labelID    string
}

func newFilterFixture(t *testing.T, pool *pgxpool.Pool) filterFixture {
	t.Helper()
	ctx := context.Background()
	actorID, assigneeID, wsSlug, ident, issueID := createNotifyFixture(t, pool)

	var projectID, stateID string
	if err := pool.QueryRow(ctx,
		`SELECT project_id::text, state_id::text FROM issues WHERE id = $1::uuid`,
		issueID).Scan(&projectID, &stateID); err != nil {
		t.Fatalf("read issue row: %v", err)
	}
	// Deterministic attributes: high priority, a target date, and the
	// "started" state (every project seeds one).
	if _, err := pool.Exec(ctx,
		`UPDATE issues SET priority = 3, target_date = '2026-10-25'::date,
		 state_id = (SELECT id FROM states WHERE project_id = $2::uuid AND "group" = 'started')
		 WHERE id = $1::uuid`,
		issueID, projectID); err != nil {
		t.Fatalf("set issue attributes: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`SELECT state_id::text FROM issues WHERE id = $1::uuid`,
		issueID).Scan(&stateID); err != nil {
		t.Fatalf("read state: %v", err)
	}
	if err := AssignAssignee(ctx, pool, wsSlug, ident, issueID, assigneeID, actorID); err != nil {
		t.Fatalf("AssignAssignee: %v", err)
	}
	color := "#ff0000"
	lbl, err := CreateLabel(ctx, pool, wsSlug, ident, actorID, LabelInput{Name: "filter-bug", Color: &color})
	if err != nil {
		t.Fatalf("CreateLabel: %v", err)
	}
	if err := AssignLabel(ctx, pool, wsSlug, ident, issueID, lbl.ID, actorID); err != nil {
		t.Fatalf("AssignLabel: %v", err)
	}
	return filterFixture{
		actorID: actorID, wsSlug: wsSlug, ident: ident, issueID: issueID,
		projectID: projectID, stateID: stateID,
		assigneeID: assigneeID, labelID: lbl.ID,
	}
}

func cond(field, op string, value any) WebhookCondition {
	return WebhookCondition{Field: field, Op: op, Value: value}
}

// matchInTx evaluates MatchWebhookFilter inside a rolled-back tx.
func matchInTx(t *testing.T, pool *pgxpool.Pool, filter WebhookFilter, event string, data map[string]any) (bool, error) {
	t.Helper()
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback(ctx)
	return MatchWebhookFilter(ctx, tx, filter, event, data)
}

// ---------- validation ----------

func TestValidateWebhookFilter(t *testing.T) {
	validUUID := uuid.NewString()
	tests := []struct {
		name    string
		filter  WebhookFilter
		wantErr bool
	}{
		{"nil filter", nil, false},
		{"empty filter", WebhookFilter{}, false},
		{"priority eq", WebhookFilter{cond("priority", "eq", "high")}, false},
		{"priority eq case-insensitive", WebhookFilter{cond("priority", "eq", "Urgent")}, false},
		{"priority in", WebhookFilter{cond("priority", "in", []any{"low", "high"})}, false},
		{"state_group eq", WebhookFilter{cond("state_group", "eq", "started")}, false},
		{"state_group in", WebhookFilter{cond("state_group", "in", []any{"started", "completed"})}, false},
		{"state_id eq", WebhookFilter{cond("state_id", "eq", validUUID)}, false},
		{"assignee_id in", WebhookFilter{cond("assignee_id", "in", []any{validUUID, uuid.NewString()})}, false},
		{"label_id eq", WebhookFilter{cond("label_id", "eq", validUUID)}, false},
		{"project_id eq", WebhookFilter{cond("project_id", "eq", validUUID)}, false},
		{"target_date eq", WebhookFilter{cond("target_date", "eq", "2026-10-25")}, false},
		{"target_date in", WebhookFilter{cond("target_date", "in", []any{"2026-10-25", "2026-11-01"})}, false},
		{"multiple conditions", WebhookFilter{
			cond("priority", "eq", "high"),
			cond("state_group", "in", []any{"started", "completed"}),
		}, false},

		{"unknown field", WebhookFilter{cond("start_date", "eq", "2026-10-25")}, true},
		{"empty field", WebhookFilter{cond("", "eq", "high")}, true},
		{"unknown op", WebhookFilter{cond("priority", "neq", "high")}, true},
		{"empty op", WebhookFilter{cond("priority", "", "high")}, true},
		{"bad priority name", WebhookFilter{cond("priority", "eq", "critical")}, true},
		{"priority numeric value", WebhookFilter{cond("priority", "eq", float64(3))}, true},
		{"bad state group", WebhookFilter{cond("state_group", "eq", "in-progress")}, true},
		// The cycle-69 plan pins Plane's 5-group vocabulary; glance's
		// own "triage" group is deliberately not filterable.
		{"triage state group rejected", WebhookFilter{cond("state_group", "eq", "triage")}, true},
		{"bad state_id uuid", WebhookFilter{cond("state_id", "eq", "not-a-uuid")}, true},
		{"bad assignee_id uuid", WebhookFilter{cond("assignee_id", "eq", "123")}, true},
		{"bad label_id uuid", WebhookFilter{cond("label_id", "eq", "not-a-uuid")}, true},
		{"bad project_id uuid", WebhookFilter{cond("project_id", "eq", "not-a-uuid")}, true},
		{"bad target_date", WebhookFilter{cond("target_date", "eq", "25-10-2026")}, true},
		{"target_date datetime rejected", WebhookFilter{cond("target_date", "eq", "2026-10-25T00:00:00Z")}, true},
		{"in with empty array", WebhookFilter{cond("priority", "in", []any{})}, true},
		{"in with non-array", WebhookFilter{cond("priority", "in", "high")}, true},
		{"in with one bad element", WebhookFilter{cond("priority", "in", []any{"high", "bogus"})}, true},
		{"in with bad uuid element", WebhookFilter{cond("label_id", "in", []any{validUUID, "nope"})}, true},
		{"nil value", WebhookFilter{cond("priority", "eq", nil)}, true},
		{"object value", WebhookFilter{cond("priority", "eq", map[string]any{"x": 1})}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateWebhookFilter(tc.filter)
			if tc.wantErr && err == nil {
				t.Fatalf("ValidateWebhookFilter(%v) = nil, want error", tc.filter)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("ValidateWebhookFilter(%v) = %v, want nil", tc.filter, err)
			}
			if tc.wantErr && !errors.Is(err, ErrInvalidWebhookFilter) {
				t.Fatalf("ValidateWebhookFilter(%v) = %v, want ErrInvalidWebhookFilter", tc.filter, err)
			}
		})
	}
}

// ---------- matching ----------

func TestMatchWebhookFilterFields(t *testing.T) {
	pool := newTestPool(t)
	fx := newFilterFixture(t, pool)
	issueData := map[string]any{"id": fx.issueID}

	tests := []struct {
		name   string
		filter WebhookFilter
		want   bool
	}{
		{"priority eq hit", WebhookFilter{cond("priority", "eq", "high")}, true},
		{"priority eq miss", WebhookFilter{cond("priority", "eq", "low")}, false},
		{"priority in hit", WebhookFilter{cond("priority", "in", []any{"low", "high"})}, true},
		{"priority in miss", WebhookFilter{cond("priority", "in", []any{"low", "urgent"})}, false},
		{"state_group eq hit", WebhookFilter{cond("state_group", "eq", "started")}, true},
		{"state_group eq miss", WebhookFilter{cond("state_group", "eq", "completed")}, false},
		{"state_group in hit", WebhookFilter{cond("state_group", "in", []any{"backlog", "started"})}, true},
		{"state_id eq hit", WebhookFilter{cond("state_id", "eq", fx.stateID)}, true},
		{"state_id eq miss", WebhookFilter{cond("state_id", "eq", uuid.NewString())}, false},
		{"assignee_id eq hit", WebhookFilter{cond("assignee_id", "eq", fx.assigneeID)}, true},
		{"assignee_id eq miss", WebhookFilter{cond("assignee_id", "eq", uuid.NewString())}, false},
		{"assignee_id in hit", WebhookFilter{cond("assignee_id", "in", []any{uuid.NewString(), fx.assigneeID})}, true},
		{"label_id eq hit", WebhookFilter{cond("label_id", "eq", fx.labelID)}, true},
		{"label_id eq miss", WebhookFilter{cond("label_id", "eq", uuid.NewString())}, false},
		{"label_id in hit", WebhookFilter{cond("label_id", "in", []any{fx.labelID, uuid.NewString()})}, true},
		{"project_id eq hit", WebhookFilter{cond("project_id", "eq", fx.projectID)}, true},
		{"project_id eq miss", WebhookFilter{cond("project_id", "eq", uuid.NewString())}, false},
		{"target_date eq hit", WebhookFilter{cond("target_date", "eq", "2026-10-25")}, true},
		{"target_date eq miss", WebhookFilter{cond("target_date", "eq", "2026-10-26")}, false},
		{"target_date in hit", WebhookFilter{cond("target_date", "in", []any{"2026-10-26", "2026-10-25"})}, true},
		{"target_date in miss", WebhookFilter{cond("target_date", "in", []any{"2026-10-26"})}, false},
		// AND semantics: every condition must hold.
		{"AND all hit", WebhookFilter{
			cond("priority", "eq", "high"),
			cond("state_group", "eq", "started"),
			cond("assignee_id", "eq", fx.assigneeID),
		}, true},
		{"AND one miss", WebhookFilter{
			cond("priority", "eq", "high"),
			cond("state_group", "eq", "completed"),
		}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := matchInTx(t, pool, tc.filter, EventIssueCreated, issueData)
			if err != nil {
				t.Fatalf("MatchWebhookFilter: %v", err)
			}
			if got != tc.want {
				t.Fatalf("MatchWebhookFilter(%v) = %v, want %v", tc.filter, got, tc.want)
			}
		})
	}
}

// TestMatchWebhookFilterBypasses: an empty filter and non-work-item
// events never consult the issue snapshot.
func TestMatchWebhookFilterBypasses(t *testing.T) {
	pool := newTestPool(t)

	// Empty filter on a work-item event: no snapshot needed (bogus id
	// would fail closed if it were consulted).
	if got, err := matchInTx(t, pool, nil, EventIssueCreated, map[string]any{"id": "bogus"}); err != nil || !got {
		t.Fatalf("empty filter = (%v, %v), want (true, nil)", got, err)
	}
	if got, err := matchInTx(t, pool, WebhookFilter{}, EventCommentCreated, map[string]any{}); err != nil || !got {
		t.Fatalf("empty filter on comment event = (%v, %v), want (true, nil)", got, err)
	}
	// Non-work-item events ignore the filter entirely — no id required.
	filter := WebhookFilter{cond("priority", "eq", "high")}
	if got, err := matchInTx(t, pool, filter, "page.created", map[string]any{}); err != nil || !got {
		t.Fatalf("non-work-item event = (%v, %v), want (true, nil)", got, err)
	}
}

// TestMatchWebhookFilterCommentEvent: comment events resolve the issue
// through data["issue_id"].
func TestMatchWebhookFilterCommentEvent(t *testing.T) {
	pool := newTestPool(t)
	fx := newFilterFixture(t, pool)
	data := map[string]any{"id": uuid.NewString(), "issue_id": fx.issueID}

	got, err := matchInTx(t, pool, WebhookFilter{cond("priority", "eq", "high")}, EventCommentCreated, data)
	if err != nil || !got {
		t.Fatalf("comment event priority hit = (%v, %v), want (true, nil)", got, err)
	}
	got, err = matchInTx(t, pool, WebhookFilter{cond("priority", "eq", "low")}, EventCommentCreated, data)
	if err != nil || got {
		t.Fatalf("comment event priority miss = (%v, %v), want (false, nil)", got, err)
	}
}

// TestMatchWebhookFilterFailClosed: anything that prevents evaluating
// the filter against a real issue snapshot is an error — the enqueue
// hook skips the delivery rather than delivering unfiltered.
func TestMatchWebhookFilterFailClosed(t *testing.T) {
	pool := newTestPool(t)
	filter := WebhookFilter{cond("priority", "eq", "high")}

	cases := []struct {
		name  string
		event string
		data  map[string]any
	}{
		{"issue event missing id", EventIssueCreated, map[string]any{"display_id": "X-1"}},
		{"issue event empty id", EventIssueUpdated, map[string]any{"id": ""}},
		{"issue event malformed id", EventIssueDeleted, map[string]any{"id": "not-a-uuid"}},
		{"issue event unknown id", EventIssueCreated, map[string]any{"id": uuid.NewString()}},
		{"comment event missing issue_id", EventCommentCreated, map[string]any{"id": uuid.NewString()}},
		{"comment event unknown issue_id", EventCommentCreated, map[string]any{"issue_id": uuid.NewString()}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := matchInTx(t, pool, filter, tc.event, tc.data)
			if err == nil {
				t.Fatalf("MatchWebhookFilter = (%v, nil), want an error (fail closed)", got)
			}
			if got {
				t.Fatalf("MatchWebhookFilter = (true, %v), want false on evaluation failure", err)
			}
		})
	}
}

// TestMatchWebhookFilterNullTargetDate: an issue with no target date
// never matches a target_date condition (NULL semantics).
func TestMatchWebhookFilterNullTargetDate(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	_, _, wsSlug, ident, issueID := createNotifyFixture(t, pool)
	if _, err := pool.Exec(ctx,
		`UPDATE issues SET target_date = NULL WHERE id = $1::uuid`, issueID); err != nil {
		t.Fatalf("clear target_date: %v", err)
	}
	_ = wsSlug
	_ = ident
	got, err := matchInTx(t, pool,
		WebhookFilter{cond("target_date", "eq", "2026-10-25")},
		EventIssueCreated, map[string]any{"id": issueID})
	if err != nil {
		t.Fatalf("MatchWebhookFilter: %v", err)
	}
	if got {
		t.Fatal("target_date condition matched an issue with NULL target_date")
	}
}

// ---------- enqueue integration ----------

func outboxWebhookRows(t *testing.T, pool *pgxpool.Pool, watermark int64) map[string]int {
	t.Helper()
	rows, err := pool.Query(context.Background(),
		`SELECT payload->>'webhook_id' FROM outbox
		  WHERE id > $1 AND event = 'webhook.deliver'`, watermark)
	if err != nil {
		t.Fatalf("read outbox: %v", err)
	}
	defer rows.Close()
	counts := map[string]int{}
	for rows.Next() {
		var wid string
		if err := rows.Scan(&wid); err != nil {
			t.Fatalf("scan: %v", err)
		}
		counts[wid]++
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	return counts
}

// TestEnqueueWebhookDeliveryTxFilter: a matching filter enqueues, a
// missing filter skips, and no filter enqueues (backwards compatible).
func TestEnqueueWebhookDeliveryTxFilter(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	fx := newFilterFixture(t, pool)
	actorID := fx.actorID

	mkFilter := func(field, op string, value any) *WebhookFilter {
		f := WebhookFilter{cond(field, op, value)}
		return &f
	}
	mk := func(events []string, f *WebhookFilter) *Webhook {
		t.Helper()
		wh, err := CreateWebhook(ctx, pool, fx.wsSlug, actorID, WebhookInput{
			URL: "https://example.com/hook", Events: events, Filter: f,
		})
		if err != nil {
			t.Fatalf("CreateWebhook: %v", err)
		}
		return wh
	}
	hit := mk([]string{EventIssueCreated}, mkFilter("priority", "eq", "high"))
	miss := mk([]string{EventIssueCreated}, mkFilter("priority", "eq", "low"))
	plain := mk([]string{EventIssueCreated}, nil)
	otherEvent := mk([]string{EventIssueUpdated}, mkFilter("priority", "eq", "high"))

	var watermark int64
	if err := pool.QueryRow(ctx, `SELECT COALESCE(max(id), 0) FROM outbox`).Scan(&watermark); err != nil {
		t.Fatalf("watermark: %v", err)
	}
	wsID := workspaceIDForTest(t, pool, fx.wsSlug)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := enqueueWebhookDeliveryTx(ctx, tx, wsID, EventIssueCreated,
		map[string]any{"id": fx.issueID}); err != nil {
		tx.Rollback(ctx)
		t.Fatalf("enqueueWebhookDeliveryTx: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}

	counts := outboxWebhookRows(t, pool, watermark)
	if counts[hit.ID] != 1 {
		t.Fatalf("matching filter: rows = %d, want 1", counts[hit.ID])
	}
	if counts[miss.ID] != 0 {
		t.Fatalf("missing filter: rows = %d, want 0", counts[miss.ID])
	}
	if counts[plain.ID] != 1 {
		t.Fatalf("no filter: rows = %d, want 1", counts[plain.ID])
	}
	if counts[otherEvent.ID] != 0 {
		t.Fatalf("unsubscribed event: rows = %d, want 0", counts[otherEvent.ID])
	}
}

// TestEnqueueWebhookDeliveryTxCorruptFilter: a stored filter that no
// longer validates (e.g. written before a vocabulary change) skips the
// delivery instead of firing unfiltered.
func TestEnqueueWebhookDeliveryTxCorruptFilter(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	fx := newFilterFixture(t, pool)
	actorID := fx.actorID

	wh, err := CreateWebhook(ctx, pool, fx.wsSlug, actorID, WebhookInput{
		URL: "https://example.com/hook", Events: []string{EventIssueCreated},
	})
	if err != nil {
		t.Fatalf("CreateWebhook: %v", err)
	}
	// Corrupt the stored filter behind the service's back.
	if _, err := pool.Exec(ctx,
		`UPDATE webhooks SET filter = $1::jsonb WHERE id = $2::uuid`,
		`[{"field":"priority","op":"eq","value":"critical"}]`, wh.ID); err != nil {
		t.Fatalf("corrupt filter: %v", err)
	}

	var watermark int64
	if err := pool.QueryRow(ctx, `SELECT COALESCE(max(id), 0) FROM outbox`).Scan(&watermark); err != nil {
		t.Fatalf("watermark: %v", err)
	}
	wsID := workspaceIDForTest(t, pool, fx.wsSlug)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := enqueueWebhookDeliveryTx(ctx, tx, wsID, EventIssueCreated,
		map[string]any{"id": fx.issueID}); err != nil {
		tx.Rollback(ctx)
		t.Fatalf("enqueueWebhookDeliveryTx: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if counts := outboxWebhookRows(t, pool, watermark); counts[wh.ID] != 0 {
		t.Fatalf("corrupt filter: rows = %d, want 0 (fail closed)", counts[wh.ID])
	}
}

// ---------- CRUD round-trip ----------

func TestWebhookFilterCRUD(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	actorID := createTestUser(t, pool, uniqueTestEmail("wh-crud-actor"))
	ws := createTestWorkspace(t, pool, "Filter Co", uniqueTestSlug("wh-filter"), actorID)

	// Create with a filter: stored and returned.
	f := WebhookFilter{cond("priority", "in", []any{"high", "urgent"})}
	wh, err := CreateWebhook(ctx, pool, ws.Slug, actorID, WebhookInput{
		URL: "https://example.com/hook", Events: []string{EventIssueCreated}, Filter: &f,
	})
	if err != nil {
		t.Fatalf("CreateWebhook: %v", err)
	}
	if wh.Filter == nil || len(*wh.Filter) != 1 || (*wh.Filter)[0].Field != "priority" {
		t.Fatalf("created filter = %+v, want the priority condition", wh.Filter)
	}

	// Invalid filter → 400-mapped sentinel.
	bad := WebhookFilter{cond("priority", "eq", "critical")}
	if _, err := CreateWebhook(ctx, pool, ws.Slug, actorID, WebhookInput{
		URL: "https://example.com/hook", Filter: &bad,
	}); !errors.Is(err, ErrInvalidWebhookFilter) {
		t.Fatalf("CreateWebhook with bad filter = %v, want ErrInvalidWebhookFilter", err)
	}

	// Update replaces the filter.
	f2 := WebhookFilter{cond("state_group", "eq", "started")}
	wh, err = UpdateWebhook(ctx, pool, ws.Slug, actorID, wh.ID, WebhookPatch{Filter: &f2})
	if err != nil {
		t.Fatalf("UpdateWebhook: %v", err)
	}
	if wh.Filter == nil || len(*wh.Filter) != 1 || (*wh.Filter)[0].Field != "state_group" {
		t.Fatalf("updated filter = %+v, want the state_group condition", wh.Filter)
	}

	// Update with an empty (non-nil) filter clears the column.
	empty := WebhookFilter{}
	wh, err = UpdateWebhook(ctx, pool, ws.Slug, actorID, wh.ID, WebhookPatch{Filter: &empty})
	if err != nil {
		t.Fatalf("UpdateWebhook clear: %v", err)
	}
	if wh.Filter != nil {
		t.Fatalf("cleared filter = %+v, want nil", wh.Filter)
	}

	// A nil patch leaves the filter untouched.
	f3 := WebhookFilter{cond("project_id", "eq", uuid.NewString())}
	wh, err = UpdateWebhook(ctx, pool, ws.Slug, actorID, wh.ID, WebhookPatch{Filter: &f3})
	if err != nil {
		t.Fatalf("UpdateWebhook set: %v", err)
	}
	wh, err = UpdateWebhook(ctx, pool, ws.Slug, actorID, wh.ID, WebhookPatch{Active: &[]bool{true}[0]})
	if err != nil {
		t.Fatalf("UpdateWebhook untouched: %v", err)
	}
	if wh.Filter == nil || (*wh.Filter)[0].Field != "project_id" {
		t.Fatalf("filter after untouched update = %+v, want project_id condition", wh.Filter)
	}

	// Get round-trips the filter.
	got, err := GetWebhook(ctx, pool, ws.Slug, actorID, wh.ID)
	if err != nil {
		t.Fatalf("GetWebhook: %v", err)
	}
	if got.Filter == nil || (*got.Filter)[0].Field != "project_id" {
		t.Fatalf("get filter = %+v, want project_id condition", got.Filter)
	}
}
