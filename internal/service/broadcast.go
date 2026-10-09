package service

// Realtime broadcast dependency (ruling R1, Task 24).
//
// The service layer announces domain events after successful mutations, but
// it must not depend on the transport: this package owns the Broadcaster
// interface and the event vocabulary, the realtime package satisfies the
// interface with its Hub, and main wires them together. realtime never
// imports service, service never imports realtime — no cycle possible.
//
// In unit tests Realtime is nil and announce is a silent no-op.

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Domain event vocabulary. notification.created has no call sites yet —
// Task 26 (notifications) will emit it; the type exists so the contract is
// visible here.
const (
	EventIssueCreated        = "issue.created"
	EventIssueUpdated        = "issue.updated"
	EventIssueDeleted        = "issue.deleted"
	EventCommentCreated      = "comment.created"
	EventCycleUpdated        = "cycle.updated"
	EventModuleUpdated       = "module.updated"
	EventPageUpdated         = "page.updated"
	EventReleaseUpdated      = "release.updated"
	EventTemplateUpdated     = "template.updated"
	EventIntakeUpdated       = "intake.updated"
	EventNotificationCreated = "notification.created"
)

// Broadcaster is the realtime fan-out sink. channels are fully-qualified
// channel names (workspace:{slug}, project:{slug}:{identifier}, issue:{uuid},
// user:{id}); eventType is one of the Event* constants; data is the minimal
// resource JSON (never the full object — clients refetch via REST).
type Broadcaster interface {
	Broadcast(channels []string, eventType string, data any)
}

// Realtime is set by main at boot. Nil in tests → announce is silent.
var Realtime Broadcaster

// announce fans out an event when a broadcaster is wired. Never blocks the
// caller: Hub.Broadcast drops on full subscriber buffers.
func announce(channels []string, eventType string, data any) {
	if b := Realtime; b != nil {
		b.Broadcast(channels, eventType, data)
	}
}

// Channel name builders (spec §6, amended R11: project channels are
// workspace-qualified — identifiers are unique per workspace only, so the
// bare project:{identifier} scheme would leak events across workspaces).
func workspaceChannel(slug string) string             { return "workspace:" + slug }
func projectChannel(wsSlug, identifier string) string { return "project:" + wsSlug + ":" + identifier }
func issueChannel(id string) string                   { return "issue:" + id }
func userChannel(id string) string                    { return "user:" + id }

// issueEventData is the minimal issue JSON carried by issue.* events.
type issueEventData struct {
	ID        string `json:"id"`
	DisplayID string `json:"display_id,omitempty"`
	Name      string `json:"name,omitempty"`
	StateID   string `json:"state_id,omitempty"`
}

func announceIssueCreated(wsSlug, identifier string, iss *Issue) {
	announce(
		[]string{projectChannel(wsSlug, identifier), workspaceChannel(wsSlug)},
		EventIssueCreated,
		issueEventData{ID: iss.ID, DisplayID: iss.DisplayID, Name: iss.Name, StateID: iss.StateID},
	)
}

func announceIssueUpdated(wsSlug, identifier, issueID string, iss *Issue) {
	announce(
		[]string{issueChannel(issueID), projectChannel(wsSlug, identifier), workspaceChannel(wsSlug)},
		EventIssueUpdated,
		issueEventData{ID: iss.ID, DisplayID: iss.DisplayID, Name: iss.Name, StateID: iss.StateID},
	)
}

func announceIssueDeleted(wsSlug, identifier, issueID string) {
	announce(
		[]string{issueChannel(issueID), projectChannel(wsSlug, identifier), workspaceChannel(wsSlug)},
		EventIssueDeleted,
		map[string]string{"id": issueID},
	)
}

// announceCycleUpdated resolves the workspace/project channels for a cycle
// from its project id and broadcasts cycle.updated. Best-effort: a lookup
// failure skips the broadcast rather than failing the mutation.
func announceCycleUpdated(ctx context.Context, pool *pgxpool.Pool, cycleID, projectID, status string) {
	var wsSlug, identifier string
	err := pool.QueryRow(ctx,
		`SELECT w.slug, p.identifier
		   FROM projects p JOIN workspaces w ON w.id = p.workspace_id
		  WHERE p.id = $1::uuid`, projectID).Scan(&wsSlug, &identifier)
	if err != nil {
		return
	}
	announce(
		[]string{projectChannel(wsSlug, identifier), workspaceChannel(wsSlug)},
		EventCycleUpdated,
		map[string]string{"id": cycleID, "status": status},
	)
}

// BroadcastCycleUpdated announces a cycle.updated event for (cycleID,
// projectID). Exported for the cycle ticker, whose activation path bypasses
// the service mutation layer (it runs a raw UPDATE ... RETURNING).
func BroadcastCycleUpdated(ctx context.Context, pool *pgxpool.Pool, cycleID, projectID, status string) {
	announceCycleUpdated(ctx, pool, cycleID, projectID, status)
}

// announceModuleUpdated resolves the workspace/project channels for a
// module from its project id and broadcasts module.updated. Best-effort:
// a lookup failure skips the broadcast rather than failing the mutation.
// The payload carries only the id — clients refetch via REST.
func announceModuleUpdated(ctx context.Context, pool *pgxpool.Pool, moduleID, projectID string) {
	var wsSlug, identifier string
	err := pool.QueryRow(ctx,
		`SELECT w.slug, p.identifier
		   FROM projects p JOIN workspaces w ON w.id = p.workspace_id
		  WHERE p.id = $1::uuid`, projectID).Scan(&wsSlug, &identifier)
	if err != nil {
		return
	}
	announce(
		[]string{projectChannel(wsSlug, identifier), workspaceChannel(wsSlug)},
		EventModuleUpdated,
		map[string]string{"id": moduleID},
	)
}

// announcePageUpdated resolves the workspace/project channels for a
// page from its project id and broadcasts page.updated. Best-effort:
// a lookup failure skips the broadcast rather than failing the mutation.
// The payload carries only the id — clients refetch via REST.
func announcePageUpdated(ctx context.Context, pool *pgxpool.Pool, pageID, projectID string) {
	var wsSlug, identifier string
	err := pool.QueryRow(ctx,
		`SELECT w.slug, p.identifier
		   FROM projects p JOIN workspaces w ON w.id = p.workspace_id
		  WHERE p.id = $1::uuid`, projectID).Scan(&wsSlug, &identifier)
	if err != nil {
		return
	}
	announce(
		[]string{projectChannel(wsSlug, identifier), workspaceChannel(wsSlug)},
		EventPageUpdated,
		map[string]string{"id": pageID},
	)
}

// BroadcastIntakeResurfaced announces intake rows whose snooze the
// background ticker expired (status flipped snoozed → pending), so open
// inboxes refresh without a page reload. Exported for the snooze ticker,
// whose expiry path bypasses the service mutation layer (it runs a raw
// UPDATE ... RETURNING).
func BroadcastIntakeResurfaced(wsSlug, identifier, intakeIssueID, issueID string) {
	announce(
		[]string{projectChannel(wsSlug, identifier), workspaceChannel(wsSlug)},
		EventIntakeUpdated,
		map[string]any{
			"id":          intakeIssueID,
			"issue_id":    issueID,
			"status":      IntakePending,
			"status_name": IntakeStatusName(IntakePending),
		},
	)
}

// announceReleaseUpdated resolves the workspace/project channels for a
// release from its project id and broadcasts release.updated.
// Best-effort: a lookup failure skips the broadcast rather than failing
// the mutation. The payload carries only the id — clients refetch via
// REST.
func announceReleaseUpdated(ctx context.Context, pool *pgxpool.Pool, releaseID, projectID string) {
	var wsSlug, identifier string
	err := pool.QueryRow(ctx,
		`SELECT w.slug, p.identifier
		   FROM projects p JOIN workspaces w ON w.id = p.workspace_id
		  WHERE p.id = $1::uuid`, projectID).Scan(&wsSlug, &identifier)
	if err != nil {
		return
	}
	announce(
		[]string{projectChannel(wsSlug, identifier), workspaceChannel(wsSlug)},
		EventReleaseUpdated,
		map[string]string{"id": releaseID},
	)
}

// announceTemplateUpdated fans a template.updated event out to the
// project + workspace channels. Best-effort like its siblings: a lookup
// failure skips the broadcast rather than failing the mutation.
func announceTemplateUpdated(ctx context.Context, pool *pgxpool.Pool, templateID, projectID string) {
	var wsSlug, identifier string
	err := pool.QueryRow(ctx,
		`SELECT w.slug, p.identifier
		   FROM projects p JOIN workspaces w ON w.id = p.workspace_id
		  WHERE p.id = $1::uuid`, projectID).Scan(&wsSlug, &identifier)
	if err != nil {
		return
	}
	announce(
		[]string{projectChannel(wsSlug, identifier), workspaceChannel(wsSlug)},
		EventTemplateUpdated,
		map[string]string{"id": templateID},
	)
}
