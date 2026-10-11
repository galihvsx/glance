package service

// Project templates (C17T1): workspace-scoped blueprints for new projects.
// A template carries a payload JSON document with the seeds applied when
// a project is created with template_id:
//
//	{
//	  "states":         [{"name":"Backlog","group":"backlog","color":"#6b7280"}],
//	  "labels":         [{"name":"bug","color":"#ef4444"}],
//	  "modules":        [{"name":"API","description":"..."}],
//	  "estimate_scale": {"name":"Fibonacci","points":[{"key":"1","value":1,"description":"..."}]}
//	}
//
// Distinct from C7T0's issue_templates (project-scoped issue defaults).
//
// Conventions:
//   - Tenancy: every op resolves workspace membership via the workspace
//     slug. Bad slug or non-member caller surfaces ErrNotFound
//     ("workspace not found"); a bad template id surfaces
//     ErrProjectTemplateNotFound. Guests (5) are forbidden on read.
//   - Roles: admin (20) for writes; member (15)+ for reads. The server
//     is the gate.
//   - The payload is validated strictly on WRITE (names non-empty,
//     groups in validStateGroups, hex colors, integer point values), so
//     apply-time cannot fail midway and roll back project creation.
//   - Labels are workspace-scoped rows (spec §4): applying a template
//     reuses an existing same-named label (INSERT ... ON CONFLICT DO
//     NOTHING) instead of failing project creation.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	// ErrProjectTemplateNotFound is returned when the template id matches
	// nothing in the workspace.
	ErrProjectTemplateNotFound = errors.New("service: project template not found")
	// ErrProjectTemplateConflict is returned when the template name is
	// already taken in the workspace (UNIQUE(workspace_id, lower(name))).
	ErrProjectTemplateConflict = errors.New("service: project template name already exists")
	// ErrInvalidProjectTemplate is returned for malformed template input:
	// empty name, or a payload that does not parse / violates the shape
	// contract.
	ErrInvalidProjectTemplate = errors.New("service: invalid project template")
	// ErrUnknownTemplateID is returned by project creation when
	// template_id matches no template in the workspace. It is a 400
	// (client gave a bad id), not a 404 — the workspace and the caller
	// are both confirmed on this path.
	ErrUnknownTemplateID = errors.New("service: unknown template_id")
)

// ProjectTemplate is a workspace-scoped project blueprint.
type ProjectTemplate struct {
	ID          string          `json:"id"`
	WorkspaceID string          `json:"workspace_id"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Payload     json.RawMessage `json:"payload"`
	CreatedBy   *string         `json:"created_by,omitempty"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
}

// TemplateState is one kanban column seeded by a project template.
type TemplateState struct {
	Name  string `json:"name"`
	Group string `json:"group"`
	Color string `json:"color"`
}

// TemplateLabel is one workspace label seeded by a project template.
type TemplateLabel struct {
	Name  string `json:"name"`
	Color string `json:"color"`
}

// TemplateModule is one project module seeded by a project template.
type TemplateModule struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// TemplateEstimatePoint is one selectable value of the template's
// estimate scale.
type TemplateEstimatePoint struct {
	Key         string `json:"key"`
	Value       int    `json:"value"`
	Description string `json:"description"`
}

// TemplateEstimateScale is the default estimate scale a template seeds.
type TemplateEstimateScale struct {
	Name   string                  `json:"name"`
	Points []TemplateEstimatePoint `json:"points"`
}

// TemplatePayload is the validated shape of project_templates.payload.
type TemplatePayload struct {
	States        []TemplateState        `json:"states"`
	Labels        []TemplateLabel        `json:"labels"`
	Modules       []TemplateModule       `json:"modules"`
	EstimateScale *TemplateEstimateScale `json:"estimate_scale,omitempty"`
}

// validateTemplatePayload parses and strictly validates a template
// payload document. Empty/absent sections are fine (the project then
// gets the ordinary defaults for that section). Every present entry is
// checked so that apply-time can never fail midway.
func validateTemplatePayload(raw json.RawMessage) (TemplatePayload, error) {
	var p TemplatePayload
	if len(bytes.TrimSpace(raw)) == 0 {
		return p, nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		return p, ErrInvalidProjectTemplate
	}

	normalize := func(s string) string { return strings.TrimSpace(s) }

	seenStates := map[string]bool{}
	for i := range p.States {
		st := &p.States[i]
		st.Name = normalize(st.Name)
		st.Group = strings.ToLower(normalize(st.Group))
		st.Color = normalize(st.Color)
		if st.Name == "" || len(st.Name) > 120 {
			return p, ErrInvalidProjectTemplate
		}
		if !validStateGroups[st.Group] {
			return p, ErrInvalidProjectTemplate
		}
		if st.Color == "" {
			st.Color = defaultLabelColor
		} else if !labelColorRe.MatchString(st.Color) {
			return p, ErrInvalidProjectTemplate
		}
		lower := strings.ToLower(st.Name)
		if seenStates[lower] {
			return p, ErrInvalidProjectTemplate
		}
		seenStates[lower] = true
	}

	seenLabels := map[string]bool{}
	for i := range p.Labels {
		l := &p.Labels[i]
		l.Name = normalize(l.Name)
		l.Color = normalize(l.Color)
		if l.Name == "" || len(l.Name) > 120 {
			return p, ErrInvalidProjectTemplate
		}
		if l.Color == "" {
			l.Color = defaultLabelColor
		} else if !labelColorRe.MatchString(l.Color) {
			return p, ErrInvalidProjectTemplate
		}
		lower := strings.ToLower(l.Name)
		if seenLabels[lower] {
			return p, ErrInvalidProjectTemplate
		}
		seenLabels[lower] = true
	}

	seenModules := map[string]bool{}
	for i := range p.Modules {
		m := &p.Modules[i]
		m.Name = normalize(m.Name)
		m.Description = normalize(m.Description)
		if m.Name == "" || len(m.Name) > 120 {
			return p, ErrInvalidProjectTemplate
		}
		lower := strings.ToLower(m.Name)
		if seenModules[lower] {
			return p, ErrInvalidProjectTemplate
		}
		seenModules[lower] = true
	}

	if p.EstimateScale != nil {
		es := p.EstimateScale
		es.Name = normalize(es.Name)
		if es.Name == "" || len(es.Name) > 120 {
			return p, ErrInvalidProjectTemplate
		}
		seenKeys := map[string]bool{}
		for i := range es.Points {
			pt := &es.Points[i]
			pt.Key = normalize(pt.Key)
			pt.Description = normalize(pt.Description)
			if pt.Key == "" || len(pt.Key) > 40 {
				return p, ErrInvalidProjectTemplate
			}
			if seenKeys[pt.Key] {
				return p, ErrInvalidProjectTemplate
			}
			seenKeys[pt.Key] = true
		}
	}

	return p, nil
}

// ProjectTemplateInput carries template creation fields.
type ProjectTemplateInput struct {
	Name        string
	Description string
	Payload     json.RawMessage
}

// ProjectTemplatePatch is a partial template update: nil = untouched.
type ProjectTemplatePatch struct {
	Name        *string
	Description *string
	Payload     json.RawMessage // nil = untouched
}

const projectTemplateColumns = `id::text, workspace_id::text, name, description,
	payload, created_by::text, created_at, updated_at`

func scanProjectTemplate(row pgx.Row) (*ProjectTemplate, error) {
	var t ProjectTemplate
	var payload []byte
	var createdBy *string
	err := row.Scan(&t.ID, &t.WorkspaceID, &t.Name, &t.Description,
		&payload, &createdBy, &t.CreatedAt, &t.UpdatedAt)
	if err != nil {
		return nil, err
	}
	t.Payload = json.RawMessage(payload)
	t.CreatedBy = createdBy
	return &t, nil
}

// requireTemplateAdmin enforces admin (20) for template writes.
func requireTemplateAdmin(role int) error {
	if role != RoleAdmin {
		return ErrForbidden
	}
	return nil
}

// requireTemplateRead enforces member (15)+ for template reads; guests
// get ErrForbidden, non-members already surfaced ErrNotFound above.
func requireTemplateRead(role int) error {
	if role < RoleMember {
		return ErrForbidden
	}
	return nil
}

// ListProjectTemplates returns the workspace's templates ordered by name.
// Member (15)+; guests get ErrForbidden, non-members ErrNotFound.
func ListProjectTemplates(ctx context.Context, pool *pgxpool.Pool, wsSlug, actorID string) ([]ProjectTemplate, error) {
	wsID, role, err := workspaceIDForActor(pool.QueryRow(ctx,
		`SELECT w.id::text, m.role
		 FROM workspaces w
		 JOIN workspace_members m ON m.workspace_id = w.id
		 WHERE w.slug = $1 AND m.user_id = $2::uuid`,
		wsSlug, actorID))
	if err != nil {
		return nil, err
	}
	if err := requireTemplateRead(role); err != nil {
		return nil, err
	}

	rows, err := pool.Query(ctx,
		`SELECT `+projectTemplateColumns+`
		 FROM project_templates WHERE workspace_id = $1::uuid ORDER BY name`,
		wsID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []ProjectTemplate{}
	for rows.Next() {
		t, err := scanProjectTemplate(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *t)
	}
	return out, rows.Err()
}

// CreateProjectTemplate inserts a template. Admin (20) only.
func CreateProjectTemplate(ctx context.Context, pool *pgxpool.Pool, wsSlug, actorID string, in ProjectTemplateInput) (*ProjectTemplate, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" || len(name) > 200 {
		return nil, ErrNameRequired
	}
	if _, err := validateTemplatePayload(in.Payload); err != nil {
		return nil, err
	}
	payload := in.Payload
	if len(bytes.TrimSpace(payload)) == 0 {
		payload = json.RawMessage(`{}`)
	}

	wsID, role, err := workspaceIDForActor(pool.QueryRow(ctx,
		`SELECT w.id::text, m.role
		 FROM workspaces w
		 JOIN workspace_members m ON m.workspace_id = w.id
		 WHERE w.slug = $1 AND m.user_id = $2::uuid`,
		wsSlug, actorID))
	if err != nil {
		return nil, err
	}
	if err := requireTemplateAdmin(role); err != nil {
		return nil, err
	}

	t, err := scanProjectTemplate(pool.QueryRow(ctx,
		`INSERT INTO project_templates (workspace_id, name, description, payload, created_by)
		 VALUES ($1::uuid, $2, $3, $4::jsonb, $5::uuid)
		 RETURNING `+projectTemplateColumns,
		wsID, name, strings.TrimSpace(in.Description), string(payload), actorID))
	if err != nil {
		if isUniqueViolation(err) {
			return nil, ErrProjectTemplateConflict
		}
		return nil, err
	}
	return t, nil
}

// GetProjectTemplate fetches one template. Member (15)+.
func GetProjectTemplate(ctx context.Context, pool *pgxpool.Pool, wsSlug, templateID, actorID string) (*ProjectTemplate, error) {
	wsID, role, err := workspaceIDForActor(pool.QueryRow(ctx,
		`SELECT w.id::text, m.role
		 FROM workspaces w
		 JOIN workspace_members m ON m.workspace_id = w.id
		 WHERE w.slug = $1 AND m.user_id = $2::uuid`,
		wsSlug, actorID))
	if err != nil {
		return nil, err
	}
	if err := requireTemplateRead(role); err != nil {
		return nil, err
	}

	t, err := scanProjectTemplate(pool.QueryRow(ctx,
		`SELECT `+projectTemplateColumns+`
		 FROM project_templates WHERE id = $1::uuid AND workspace_id = $2::uuid`,
		templateID, wsID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrProjectTemplateNotFound
		}
		return nil, err
	}
	return t, nil
}

// UpdateProjectTemplate applies a partial update. Admin (20) only.
func UpdateProjectTemplate(ctx context.Context, pool *pgxpool.Pool, wsSlug, templateID, actorID string, patch ProjectTemplatePatch) (*ProjectTemplate, error) {
	wsID, role, err := workspaceIDForActor(pool.QueryRow(ctx,
		`SELECT w.id::text, m.role
		 FROM workspaces w
		 JOIN workspace_members m ON m.workspace_id = w.id
		 WHERE w.slug = $1 AND m.user_id = $2::uuid`,
		wsSlug, actorID))
	if err != nil {
		return nil, err
	}
	if err := requireTemplateAdmin(role); err != nil {
		return nil, err
	}

	setClauses := []string{}
	args := []any{templateID, wsID}
	if patch.Name != nil {
		name := strings.TrimSpace(*patch.Name)
		if name == "" || len(name) > 200 {
			return nil, ErrNameRequired
		}
		args = append(args, name)
		setClauses = append(setClauses, "name = $"+strconv.Itoa(len(args)))
	}
	if patch.Description != nil {
		args = append(args, strings.TrimSpace(*patch.Description))
		setClauses = append(setClauses, "description = $"+strconv.Itoa(len(args)))
	}
	if patch.Payload != nil {
		if _, err := validateTemplatePayload(patch.Payload); err != nil {
			return nil, err
		}
		args = append(args, string(patch.Payload))
		setClauses = append(setClauses, "payload = $"+strconv.Itoa(len(args))+"::jsonb")
	}
	if len(setClauses) == 0 {
		return nil, ErrNothingToUpdate
	}
	setClauses = append(setClauses, "updated_at = now()")

	t, err := scanProjectTemplate(pool.QueryRow(ctx,
		`UPDATE project_templates SET `+strings.Join(setClauses, ", ")+`
		 WHERE id = $1::uuid AND workspace_id = $2::uuid
		 RETURNING `+projectTemplateColumns,
		args...))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrProjectTemplateNotFound
		}
		if isUniqueViolation(err) {
			return nil, ErrProjectTemplateConflict
		}
		return nil, err
	}
	return t, nil
}

// DeleteProjectTemplate removes a template. Admin (20) only; missing id
// is ErrProjectTemplateNotFound.
func DeleteProjectTemplate(ctx context.Context, pool *pgxpool.Pool, wsSlug, templateID, actorID string) error {
	wsID, role, err := workspaceIDForActor(pool.QueryRow(ctx,
		`SELECT w.id::text, m.role
		 FROM workspaces w
		 JOIN workspace_members m ON m.workspace_id = w.id
		 WHERE w.slug = $1 AND m.user_id = $2::uuid`,
		wsSlug, actorID))
	if err != nil {
		return err
	}
	if err := requireTemplateAdmin(role); err != nil {
		return err
	}

	ct, err := pool.Exec(ctx,
		`DELETE FROM project_templates WHERE id = $1::uuid AND workspace_id = $2::uuid`,
		templateID, wsID)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrProjectTemplateNotFound
	}
	return nil
}

// defaultStatesAsTemplate adapts the defaultStates seed to TemplateState
// so template and default seeding share one insert loop.
func defaultStatesAsTemplate() []TemplateState {
	out := make([]TemplateState, 0, len(defaultStates))
	for _, s := range defaultStates {
		out = append(out, TemplateState{Name: s.name, Group: s.group, Color: s.color})
	}
	return out
}

// applyTemplateSeedsTx applies a template's labels, modules, and default
// estimate scale inside the project creation transaction. State seeding is
// handled by the caller (template states replace the default seed). The
// payload was validated on write, so every entry here is trusted.
func applyTemplateSeedsTx(ctx context.Context, tx pgx.Tx, wsID, projectID string, p TemplatePayload) error {
	// Labels are workspace-scoped: reuse an existing same-named label
	// instead of failing the whole project creation.
	for _, l := range p.Labels {
		if _, err := tx.Exec(ctx,
			`INSERT INTO labels (workspace_id, name, color)
			 VALUES ($1::uuid, $2, $3)
			 ON CONFLICT (workspace_id, name) DO NOTHING`,
			wsID, l.Name, l.Color); err != nil {
			return err
		}
	}

	// Modules are project-scoped; the project is brand new, so a plain
	// insert is safe.
	for _, m := range p.Modules {
		desc := m.Description
		if _, err := tx.Exec(ctx,
			`INSERT INTO modules (project_id, name, description)
			 VALUES ($1::uuid, $2, NULLIF($3, ''))`,
			projectID, m.Name, desc); err != nil {
			return err
		}
	}

	// Estimate scale: one scale row plus its points.
	if p.EstimateScale != nil {
		es := p.EstimateScale
		var estID string
		if err := tx.QueryRow(ctx,
			`INSERT INTO estimates (project_id, name)
			 VALUES ($1::uuid, $2) RETURNING id::text`,
			projectID, es.Name).Scan(&estID); err != nil {
			return err
		}
		for _, pt := range es.Points {
			if _, err := tx.Exec(ctx,
				`INSERT INTO estimate_points (estimate_id, key, value, description)
				 VALUES ($1::uuid, $2, $3, NULLIF($4, ''))`,
				estID, pt.Key, pt.Value, pt.Description); err != nil {
				return err
			}
		}
	}
	return nil
}

// CreateProjectWithTemplate is CreateProject plus an optional project
// template: the template's states/labels/modules/estimate scale are
// seeded in the SAME transaction as the project row. A template_id that
// matches no template in this workspace is ErrUnknownTemplateID (400).
// The optional icon is validated as a single emoji (C18T4); empty
// normalizes to NULL (unset). The caller must be a workspace member (15)
// or admin (20), like CreateProject.
func CreateProjectWithTemplate(ctx context.Context, pool *pgxpool.Pool, wsSlug, actorID, name, identifier string, templateID *string, icon *string) (*Project, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, ErrNameRequired
	}
	ident, err := normalizeIdentifier(identifier)
	if err != nil {
		return nil, err
	}
	var iconVal *string
	if icon != nil {
		iconVal, err = normalizeProjectIcon(*icon)
		if err != nil {
			return nil, err
		}
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	wsID, role, err := workspaceIDForActor(tx.QueryRow(ctx,
		`SELECT w.id::text, m.role
		 FROM workspaces w
		 JOIN workspace_members m ON m.workspace_id = w.id
		 WHERE w.slug = $1 AND m.user_id = $2::uuid`,
		wsSlug, actorID))
	if err != nil {
		return nil, err
	}
	if role < RoleMember {
		return nil, ErrForbidden
	}

	// Resolve the template inside the tx when requested. Unknown id →
	// 400, never a partial project.
	var payload TemplatePayload
	if templateID != nil && strings.TrimSpace(*templateID) != "" {
		var raw []byte
		err := tx.QueryRow(ctx,
			`SELECT payload FROM project_templates
			 WHERE id = $1::uuid AND workspace_id = $2::uuid`,
			strings.TrimSpace(*templateID), wsID).Scan(&raw)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, ErrUnknownTemplateID
			}
			return nil, err
		}
		// Re-validate defensively: the row was validated on write, but
		// a defensive parse keeps apply from ever trusting garbage.
		payload, err = validateTemplatePayload(raw)
		if err != nil {
			return nil, err
		}
	}

	p, err := scanProject(tx.QueryRow(ctx,
		`INSERT INTO projects (workspace_id, identifier, name, icon)
		 VALUES ($1::uuid, $2, $3, $4)
		 RETURNING `+projectColumns,
		wsID, ident, name, iconVal))
	if err != nil {
		if isUniqueViolation(err) {
			return nil, ErrIdentifierConflict
		}
		return nil, err
	}

	// Seed states: the template's when it defines any, else the default
	// seed. Sequences are spaced 10000 apart (same convention as
	// defaultStates) so future reorders have room without renumbering.
	seedStates := defaultStatesAsTemplate()
	if len(payload.States) > 0 {
		seedStates = payload.States
	}
	seq := 10000
	for _, s := range seedStates {
		if _, err := tx.Exec(ctx,
			`INSERT INTO states (project_id, name, "group", color, sequence)
			 VALUES ($1::uuid, $2, $3, $4, $5)`,
			p.ID, s.Name, s.Group, s.Color, seq); err != nil {
			return nil, err
		}
		seq += 10000
	}

	// Labels, modules, and the estimate scale seed in the same tx —
	// atomic with the project row.
	if err := applyTemplateSeedsTx(ctx, tx, wsID, p.ID, payload); err != nil {
		return nil, err
	}

	// The per-project issue sequence counter (Task 14) is born with the
	// project, so the atomic UPDATE..RETURNING in CreateIssue is
	// unconditional — no "row missing" branch in the hot path.
	if _, err := tx.Exec(ctx,
		`INSERT INTO issue_sequences (project_id) VALUES ($1::uuid)`,
		p.ID); err != nil {
		return nil, err
	}

	if err := seedIntakeTx(ctx, tx, p.ID); err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return p, nil
}
