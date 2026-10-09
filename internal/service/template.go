package service

// Issue templates (C7T0): named, reusable issue blueprints scoped to a
// project. A template holds a template_data JSONB document with the issue
// defaults — {name?, description?, priority?, estimate_point_id?,
// label_ids?[], state_id?} — and POST .../templates/{id}/apply merges it
// with optional per-call overrides, validates the referenced
// state/labels/estimates still exist (a stale ref is a 404, not a
// silently wrong issue), and creates the issue.
//
// Conventions (mirror modules/releases):
//   - Tenancy: every op resolves workspace membership + project via
//     resolveIssueProject. A bad slug or non-member caller surfaces
//     ErrNotFound ("workspace not found"); a bad project identifier
//     surfaces ErrProjectNotFound; a bad template id surfaces
//     ErrTemplateNotFound. Distinct 404s on purpose (Task 11 ruling).
//   - Roles: any member (guest 5+) may read; mutations need member (15)+.
//     Apply creates an issue, so it also needs member (15)+.
//   - template_data is validated for shape (name non-empty, priority 0-4)
//     on write, but its state/label/estimate refs are NOT resolved until
//     apply: a ref may be deleted after the template is saved, and apply
//     must fail loudly (TemplateStaleRef → 404) instead of creating a
//     wrong issue.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	// ErrTemplateNotFound is returned when the template id matches nothing
	// in the project. Deliberately distinct from ErrProjectNotFound: on
	// this path the workspace and project are confirmed and the caller
	// is a member.
	ErrTemplateNotFound = errors.New("service: issue template not found")
	// ErrTemplateConflict is returned when the template name is already
	// taken in the project (UNIQUE(project_id, name)).
	ErrTemplateConflict = errors.New("service: issue template name already exists")
	// ErrInvalidTemplate is returned for malformed template input: empty
	// name, a priority outside 0-4, or template_data that does not parse.
	ErrInvalidTemplate = errors.New("service: invalid issue template")
	// ErrInvalidTemplateID is returned when a template id is not a
	// syntactically valid UUID. The handler maps it to 400 bad_request.
	ErrInvalidTemplateID = errors.New("service: invalid template id")
)

// TemplateStaleRef is returned by ApplyTemplate when a state, label, or
// estimate referenced by the template (or by an override) no longer
// exists in the project/workspace — the ref went stale after the
// template was saved, or an override pointed at nothing. The handler
// maps it to 404 with the kind + id in the message: an honest error
// instead of a silently wrong issue.
type TemplateStaleRef struct {
	Kind string // "state", "label", or "estimate"
	ID   string
}

func (e *TemplateStaleRef) Error() string {
	return fmt.Sprintf("service: template references a missing %s: %s", e.Kind, e.ID)
}

// TemplateData is the issue-defaults document stored in
// issue_templates.template_data. Every field is optional; absent fields
// fall back to the ordinary issue defaults at apply time (backlog state,
// no labels, NULL description).
type TemplateData struct {
	Name            *string         `json:"name,omitempty"`
	Description     json.RawMessage `json:"description,omitempty"`
	Priority        *int            `json:"priority,omitempty"`
	EstimatePointID *string         `json:"estimate_point_id,omitempty"`
	LabelIDs        []string        `json:"label_ids,omitempty"`
	StateID         *string         `json:"state_id,omitempty"`
}

// IssueTemplate is one project issue template.
type IssueTemplate struct {
	ID           string       `json:"id"`
	ProjectID    string       `json:"project_id"`
	Name         string       `json:"name"`
	Description  string       `json:"description"`
	TemplateData TemplateData `json:"template_data"`
	CreatedBy    *string      `json:"created_by,omitempty"`
	CreatedAt    time.Time    `json:"created_at"`
	UpdatedAt    time.Time    `json:"updated_at"`
}

// TemplateInput carries template creation fields. TemplateData is the
// raw template_data document; nil/empty stores '{}'.
type TemplateInput struct {
	Name         string
	Description  *string
	TemplateData json.RawMessage
}

// TemplatePatch is a partial template update: nil fields are untouched.
// TemplateData, when non-nil, replaces the whole document; Name must be
// non-empty when provided; Description sets verbatim ("" clears it).
type TemplatePatch struct {
	Name         *string
	Description  *string
	TemplateData json.RawMessage
}

// TemplateOverrides carries the per-call overrides for ApplyTemplate.
// Nil fields fall back to the template defaults; a non-nil LabelIDs
// (even empty) replaces the template's label set; an explicit JSON null
// Description clears the description to NULL.
type TemplateOverrides struct {
	Name            *string
	Description     json.RawMessage
	Priority        *int
	StateID         *string
	EstimatePointID *string
	LabelIDs        *[]string
}

// resolveTemplateProject resolves (workspaceID, projectID, role) for the
// caller. Guests may read; callers pass needMember to enforce member
// (15)+.
func resolveTemplateProject(ctx context.Context, q queryRower, wsSlug, identifier, actorID string, needMember bool) (wsID, projectID string, role int, err error) {
	wsID, projectID, role, err = resolveIssueProject(ctx, q, wsSlug, identifier, actorID)
	if err != nil {
		return "", "", 0, err
	}
	if needMember && role < RoleMember {
		return "", "", 0, ErrForbidden
	}
	return wsID, projectID, role, nil
}

const templateColumns = `id::text, project_id::text, name, description,
	template_data, created_by::text, created_at, updated_at`

func scanTemplate(row pgx.Row) (IssueTemplate, error) {
	var t IssueTemplate
	var data []byte
	err := row.Scan(
		&t.ID, &t.ProjectID, &t.Name, &t.Description,
		&data, &t.CreatedBy, &t.CreatedAt, &t.UpdatedAt,
	)
	if err != nil {
		return IssueTemplate{}, err
	}
	if len(data) > 0 {
		if err := json.Unmarshal(data, &t.TemplateData); err != nil {
			// The column only ever holds what normalizeTemplateData
			// accepted, so a parse failure is data corruption —
			// surface it loudly rather than serving a half template.
			return IssueTemplate{}, fmt.Errorf("service: corrupt template_data for template %s: %w", t.ID, err)
		}
	}
	return t, nil
}

// resolveTemplate loads a template of the project or ErrTemplateNotFound.
func resolveTemplate(ctx context.Context, q queryRower, projectID, templateID string) (IssueTemplate, error) {
	if !isUUIDFormat(templateID) {
		return IssueTemplate{}, ErrInvalidTemplateID
	}
	t, err := scanTemplate(q.QueryRow(ctx,
		`SELECT `+templateColumns+` FROM issue_templates
		  WHERE id = $1::uuid AND project_id = $2::uuid`,
		templateID, projectID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return IssueTemplate{}, ErrTemplateNotFound
		}
		return IssueTemplate{}, err
	}
	return t, nil
}

// normalizeTemplateData parses and shape-checks a template_data document.
// Refs (state/labels/estimate) are deliberately NOT resolved here — they
// are validated at apply time. Returns ErrInvalidTemplate on bad shape.
func normalizeTemplateData(raw json.RawMessage) (TemplateData, error) {
	var data TemplateData
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return data, nil
	}
	if err := json.Unmarshal(raw, &data); err != nil {
		return TemplateData{}, ErrInvalidTemplate
	}
	if data.Name != nil && strings.TrimSpace(*data.Name) == "" {
		return TemplateData{}, ErrInvalidTemplate
	}
	if data.Priority != nil && (*data.Priority < 0 || *data.Priority > 4) {
		return TemplateData{}, ErrInvalidTemplate
	}
	return data, nil
}

// templateDataParam renders the normalized document back to a ::jsonb
// bind param ('{}' when empty).
func templateDataParam(data TemplateData) string {
	raw, err := json.Marshal(data)
	if err != nil || string(raw) == "null" {
		return "{}"
	}
	return string(raw)
}

// CreateTemplate creates an issue template in the project. Member (15)+.
func CreateTemplate(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, actorID string, in TemplateInput) (*IssueTemplate, error) {
	_, projectID, _, err := resolveTemplateProject(ctx, pool, wsSlug, identifier, actorID, true)
	if err != nil {
		return nil, err
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return nil, ErrNameRequired
	}
	data, err := normalizeTemplateData(in.TemplateData)
	if err != nil {
		return nil, err
	}
	desc := ""
	if in.Description != nil {
		desc = strings.TrimSpace(*in.Description)
	}
	t, err := scanTemplate(pool.QueryRow(ctx,
		`INSERT INTO issue_templates (project_id, name, description, template_data, created_by)
		 VALUES ($1::uuid, $2, $3, $4::jsonb, $5::uuid)
		 RETURNING `+templateColumns,
		projectID, name, desc, templateDataParam(data), actorID))
	if err != nil {
		if isUniqueViolation(err) {
			return nil, ErrTemplateConflict
		}
		return nil, err
	}
	announceTemplateUpdated(ctx, pool, t.ID, projectID)
	return &t, nil
}

// ListTemplates returns the project's templates, oldest first. Any role
// may read.
func ListTemplates(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, actorID string) ([]IssueTemplate, error) {
	_, projectID, _, err := resolveTemplateProject(ctx, pool, wsSlug, identifier, actorID, false)
	if err != nil {
		return nil, err
	}
	rows, err := pool.Query(ctx,
		`SELECT `+templateColumns+`
		 FROM issue_templates WHERE project_id = $1::uuid
		 ORDER BY created_at`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []IssueTemplate{}
	for rows.Next() {
		t, err := scanTemplate(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// GetTemplate returns one template. Any role may read.
func GetTemplate(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, actorID, templateID string) (*IssueTemplate, error) {
	_, projectID, _, err := resolveTemplateProject(ctx, pool, wsSlug, identifier, actorID, false)
	if err != nil {
		return nil, err
	}
	t, err := resolveTemplate(ctx, pool, projectID, templateID)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// UpdateTemplate applies a partial template update. Member (15)+. Only
// non-nil patch fields are written; a non-nil TemplateData replaces the
// whole document. An empty patch is ErrNothingToUpdate.
func UpdateTemplate(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, actorID, templateID string, patch TemplatePatch) (*IssueTemplate, error) {
	_, projectID, _, err := resolveTemplateProject(ctx, pool, wsSlug, identifier, actorID, true)
	if err != nil {
		return nil, err
	}
	if _, err := resolveTemplate(ctx, pool, projectID, templateID); err != nil {
		return nil, err
	}
	set := []string{}
	setArgs := []any{}
	addSet := func(fragment string, v any) {
		setArgs = append(setArgs, v)
		set = append(set, fragment+"$"+strconv.Itoa(len(setArgs)+2))
	}
	if patch.Name != nil {
		name := strings.TrimSpace(*patch.Name)
		if name == "" {
			return nil, ErrNameRequired
		}
		addSet("name = ", name)
	}
	if patch.Description != nil {
		addSet("description = ", strings.TrimSpace(*patch.Description))
	}
	if patch.TemplateData != nil {
		data, err := normalizeTemplateData(patch.TemplateData)
		if err != nil {
			return nil, err
		}
		addSet("template_data = ", templateDataParam(data))
		set[len(set)-1] += "::jsonb"
	}
	if len(set) == 0 {
		return nil, ErrNothingToUpdate
	}
	set = append(set, "updated_at = now()")
	t, err := scanTemplate(pool.QueryRow(ctx,
		`UPDATE issue_templates SET `+strings.Join(set, ", ")+`
		  WHERE id = $1::uuid AND project_id = $2::uuid
		  RETURNING `+templateColumns,
		append([]any{templateID, projectID}, setArgs...)...))
	if err != nil {
		if isUniqueViolation(err) {
			return nil, ErrTemplateConflict
		}
		return nil, err
	}
	announceTemplateUpdated(ctx, pool, t.ID, projectID)
	return &t, nil
}

// DeleteTemplate removes a template. Member (15)+. Nothing references
// templates (issues are created as copies), so there is no delete guard.
func DeleteTemplate(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, actorID, templateID string) error {
	_, projectID, _, err := resolveTemplateProject(ctx, pool, wsSlug, identifier, actorID, true)
	if err != nil {
		return err
	}
	if _, err := resolveTemplate(ctx, pool, projectID, templateID); err != nil {
		return err
	}
	_, err = pool.Exec(ctx,
		`DELETE FROM issue_templates WHERE id = $1::uuid AND project_id = $2::uuid`,
		templateID, projectID)
	if err != nil {
		return err
	}
	announceTemplateUpdated(ctx, pool, templateID, projectID)
	return nil
}

// checkTemplateLabel validates that labelID names a label of this
// workspace, returning its canonical (lowercase) form. A missing,
// malformed, or foreign label is a TemplateStaleRef, not a generic 400:
// apply must say exactly which ref went stale.
func checkTemplateLabel(ctx context.Context, q queryRower, wsID, labelID string) (string, error) {
	raw := strings.TrimSpace(labelID)
	lid := strings.ToLower(raw)
	if !isUUIDFormat(lid) {
		return "", &TemplateStaleRef{Kind: "label", ID: raw}
	}
	var owner string
	err := q.QueryRow(ctx,
		`SELECT workspace_id::text FROM labels WHERE id = $1::uuid`, lid).Scan(&owner)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", &TemplateStaleRef{Kind: "label", ID: lid}
		}
		return "", err
	}
	if owner != wsID {
		return "", &TemplateStaleRef{Kind: "label", ID: lid}
	}
	return lid, nil
}

// resolveApplyName merges the override/template name defaults: an
// explicit override wins, then template_data.name, then the template's
// own name (a template with no issue-name default still creates issues
// named after itself). Empty is ErrNameRequired.
func resolveApplyName(tmpl IssueTemplate, ov TemplateOverrides) (string, error) {
	name := tmpl.Name
	if tmpl.TemplateData.Name != nil {
		name = *tmpl.TemplateData.Name
	}
	if ov.Name != nil {
		name = *ov.Name
	}
	if strings.TrimSpace(name) == "" {
		return "", ErrNameRequired
	}
	return strings.TrimSpace(name), nil
}

// resolveApplyDescription merges the description: an explicit override
// wins (a JSON null clears to NULL), otherwise the template default. A
// stored/overridden JSON null or empty document means NULL.
func resolveApplyDescription(tmpl IssueTemplate, ov TemplateOverrides) json.RawMessage {
	raw := tmpl.TemplateData.Description
	if ov.Description != nil {
		raw = ov.Description
	}
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return nil
	}
	return raw
}

// ApplyTemplate creates an issue from a template plus optional
// per-call overrides. Member (15)+ (it creates an issue).
//
// Ref validation happens first and loudly: a state_id, label id, or
// estimate_point_id that no longer exists in the project/workspace is a
// *TemplateStaleRef (404 with kind + id) — never a silently wrong issue.
func ApplyTemplate(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, actorID, templateID string, ov TemplateOverrides) (*Issue, error) {
	wsID, projectID, _, err := resolveTemplateProject(ctx, pool, wsSlug, identifier, actorID, true)
	if err != nil {
		return nil, err
	}
	tmpl, err := resolveTemplate(ctx, pool, projectID, templateID)
	if err != nil {
		return nil, err
	}

	name, err := resolveApplyName(tmpl, ov)
	if err != nil {
		return nil, err
	}
	description := resolveApplyDescription(tmpl, ov)

	priority := 0
	if tmpl.TemplateData.Priority != nil {
		priority = *tmpl.TemplateData.Priority
	}
	if ov.Priority != nil {
		priority = *ov.Priority
	}
	if priority < 0 || priority > 4 {
		return nil, ErrInvalidPriority
	}

	// State: override wins, then the template default; unset means the
	// project's backlog state (CreateIssue's own default).
	var stateID *string
	rawState := ""
	if tmpl.TemplateData.StateID != nil {
		rawState = *tmpl.TemplateData.StateID
	}
	if ov.StateID != nil {
		rawState = *ov.StateID
	}
	if strings.TrimSpace(rawState) != "" {
		sid, err := checkStateInProject(ctx, pool, projectID, rawState)
		if err != nil {
			if errors.Is(err, ErrInvalidState) {
				return nil, &TemplateStaleRef{Kind: "state", ID: strings.TrimSpace(rawState)}
			}
			return nil, err
		}
		stateID = &sid
	}

	// Estimate point: same merge, validated against the project.
	var estimatePointID *string
	rawEstimate := ""
	if tmpl.TemplateData.EstimatePointID != nil {
		rawEstimate = *tmpl.TemplateData.EstimatePointID
	}
	if ov.EstimatePointID != nil {
		rawEstimate = *ov.EstimatePointID
	}
	if strings.TrimSpace(rawEstimate) != "" {
		epid, err := checkEstimatePoint(ctx, pool, projectID, rawEstimate)
		if err != nil {
			if errors.Is(err, ErrInvalidEstimatePoint) {
				return nil, &TemplateStaleRef{Kind: "estimate", ID: strings.TrimSpace(rawEstimate)}
			}
			return nil, err
		}
		estimatePointID = &epid
	}

	// Labels: a non-nil override (even empty) replaces the template set.
	labelIDs := tmpl.TemplateData.LabelIDs
	if ov.LabelIDs != nil {
		labelIDs = *ov.LabelIDs
	}
	canonicalLabels := make([]string, 0, len(labelIDs))
	seen := map[string]bool{}
	for _, lid := range labelIDs {
		canon, err := checkTemplateLabel(ctx, pool, wsID, lid)
		if err != nil {
			return nil, err
		}
		if !seen[canon] {
			seen[canon] = true
			canonicalLabels = append(canonicalLabels, canon)
		}
	}

	// All refs are live: create the issue (sequence, activity row, webhook,
	// and the created broadcast are CreateIssue's own job) and attach the
	// labels.
	iss, err := CreateIssue(ctx, pool, wsSlug, identifier, actorID, CreateIssueInput{
		Name:            name,
		Description:     description,
		Priority:        &priority,
		StateID:         stateID,
		EstimatePointID: estimatePointID,
	})
	if err != nil {
		return nil, err
	}
	for _, lid := range canonicalLabels {
		if _, err := pool.Exec(ctx,
			`INSERT INTO issue_labels (issue_id, label_id) VALUES ($1::uuid, $2::uuid)
			 ON CONFLICT DO NOTHING`,
			iss.ID, lid); err != nil {
			return nil, err
		}
	}
	return iss, nil
}
