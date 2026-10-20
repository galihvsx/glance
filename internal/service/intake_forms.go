package service

// Intake forms (C82T0): per-project shareable forms whose public,
// unauthenticated submissions create pending intake issues.
//
// Conventions:
//   - Tenancy: management (CRUD + regenerate) resolves workspace
//     membership + project via resolveIntakeFormProject and requires the
//     workspace admin role (20) — forms are a settings surface, strictly
//     above the member (15) bar used by issue creation and above the guest
//     (5) bar of the intake inbox read.
//   - The share token is the ONLY capability for the public endpoints:
//     GetPublicIntakeForm / SubmitIntakeForm look up by token alone, and
//     treat unknown / disabled tokens identically as ErrIntakeFormNotFound
//     (404, never 403 — existence must not leak).
//   - Public submissions are attributed to SystemUserID (the dedicated,
//     inactive users row seeded by migration 000073), never to a random
//     member. issues.created_by and issue_activities.actor_id are
//     NOT NULL FKs to users(id), so anonymous creation needs a real row.
//   - Submission creates the issue through createIssueTx — the SAME
//     in-transaction creation the in-app path uses — so the _created
//     activity row, the issue.created webhook/Slack outbox fan-out, the
//     created-rule automations, and the v1 version snapshot behave
//     identically to an in-app create.

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// SystemUserID is the dedicated, inactive users row that public
// intake-form submissions are attributed to (created_by / activity
// actor_id). Seeded by migration 000073 with email
// system@glance.internal and is_active = false, so it can never
// authenticate — it is an attribution marker, not an account. The id is
// a fixed reserved UUID (not the nil UUID, which the test suite uses as
// its guaranteed-unknown-user sentinel).
const SystemUserID = "4451ef68-06f5-4bc7-80db-dc0aec4c9d67"

var (
	// ErrIntakeFormNotFound is returned for an unknown form id, a form id
	// from another project, or an unknown/disabled share token. Handlers
	// map it to 404 in all cases.
	ErrIntakeFormNotFound = errors.New("service: intake form not found")
	// ErrInvalidIntakeForm is returned for a malformed form definition:
	// blank name, unknown/duplicate field keys, a missing or optional
	// title, or a custom:<uuid> that is not one of the project's custom
	// fields. Handlers map it to an honest 400.
	ErrInvalidIntakeForm = errors.New("service: invalid intake form")
	// ErrInvalidFormSubmission is returned for a malformed public
	// submission: missing title, a missing required field, or a value
	// that fails type validation. The message names the offending
	// field's label. Handlers map it to an honest 400.
	ErrInvalidFormSubmission = errors.New("service: invalid form submission")
)

// FormDefinitionError is an honest 400 for a malformed form definition.
// It unwraps to ErrInvalidIntakeForm so errors.Is keeps working while the
// handler serves Message without the service-internal prefix.
type FormDefinitionError struct{ Message string }

func (e *FormDefinitionError) Error() string { return "service: invalid intake form: " + e.Message }
func (e *FormDefinitionError) Unwrap() error { return ErrInvalidIntakeForm }

// FormSubmissionError is an honest 400 for a malformed public submission.
// It unwraps to ErrInvalidFormSubmission; the handler serves Message.
type FormSubmissionError struct{ Message string }

func (e *FormSubmissionError) Error() string { return "service: invalid form submission: " + e.Message }
func (e *FormSubmissionError) Unwrap() error { return ErrInvalidFormSubmission }

// formDefinitionErr builds a FormDefinitionError from a format string.
func formDefinitionErr(format string, args ...any) error {
	return &FormDefinitionError{Message: fmt.Sprintf(format, args...)}
}

// Intake form field keys. title is always present exactly once and always
// required; custom keys address project custom fields by id.
const (
	FormFieldTitle        = "title"
	FormFieldDescription  = "description"
	FormFieldPriority     = "priority"
	FormFieldCustomPrefix = "custom:"
)

// IntakeFormField is one ordered field descriptor of a form: the key
// selects the issue attribute, label is the free-text display label, and
// required marks the field mandatory on submission.
type IntakeFormField struct {
	Key      string `json:"key"`
	Label    string `json:"label"`
	Required bool   `json:"required"`
}

// IntakeForm is one intake form (management view — never served publicly).
type IntakeForm struct {
	ID          string            `json:"id"`
	ProjectID   string            `json:"project_id"`
	IntakeID    string            `json:"intake_id"`
	Name        string            `json:"name"`
	Description *string           `json:"description,omitempty"`
	IsEnabled   bool              `json:"is_enabled"`
	ShareToken  string            `json:"share_token"`
	Fields      []IntakeFormField `json:"fields"`
	CreatedBy   string            `json:"created_by"`
	CreatedAt   time.Time         `json:"created_at"`
	UpdatedAt   time.Time         `json:"updated_at"`
}

// IntakeFormInput carries form creation fields.
type IntakeFormInput struct {
	Name        string
	Description *string
	IsEnabled   *bool
	Fields      []IntakeFormField
}

// IntakeFormPatch is a partial form update: nil / Set=false fields are
// left untouched, so PATCH carries true partial-update semantics. An
// explicit null (or empty string) description clears it.
type IntakeFormPatch struct {
	Name        *string
	Description PatchField[string]
	IsEnabled   *bool
	Fields      *[]IntakeFormField
}

// PublicIntakeFormField is the sanitized field descriptor served to
// anonymous submitters: no internal ids, type resolved, select options
// included. options is empty for every non-select type.
type PublicIntakeFormField struct {
	Key      string         `json:"key"`
	Label    string         `json:"label"`
	Required bool           `json:"required"`
	Type     string         `json:"type"`
	Options  []SelectOption `json:"options"`
}

// PublicIntakeForm is the sanitized envelope for GET /api/v1/forms/:token.
type PublicIntakeForm struct {
	Name        string                  `json:"name"`
	Description *string                 `json:"description,omitempty"`
	ProjectName string                  `json:"project_name"`
	Fields      []PublicIntakeFormField `json:"fields"`
}

const intakeFormColumns = `id::text, project_id::text, intake_id::text, name, description,
	is_enabled, share_token, fields, created_by::text, created_at, updated_at`

func scanIntakeForm(row pgx.Row) (*IntakeForm, error) {
	var f IntakeForm
	var fieldsRaw json.RawMessage
	if err := row.Scan(&f.ID, &f.ProjectID, &f.IntakeID, &f.Name, &f.Description,
		&f.IsEnabled, &f.ShareToken, &fieldsRaw, &f.CreatedBy, &f.CreatedAt, &f.UpdatedAt); err != nil {
		return nil, err
	}
	f.Fields = []IntakeFormField{}
	if len(fieldsRaw) > 0 {
		if err := json.Unmarshal(fieldsRaw, &f.Fields); err != nil {
			return nil, fmt.Errorf("service: corrupt intake form fields: %w", err)
		}
	}
	return &f, nil
}

// generateIntakeFormToken returns a url-safe 43-char token from 32 random
// bytes (spec: crypto-random ≥ 32 bytes, base64url).
func generateIntakeFormToken() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}

// resolveIntakeFormProject resolves workspace membership + project for the
// form management surface. Forms are a settings surface: only the
// workspace admin (20) may manage them — strictly above the member (15)
// bar for issue creation and the guest (5) bar for the intake inbox read.
func resolveIntakeFormProject(ctx context.Context, q queryRower, wsSlug, identifier, actorID string) (wsID, projectID string, err error) {
	ident, err := normalizeIdentifier(identifier)
	if err != nil {
		return "", "", err
	}
	wsID, projectID, role, err := resolveIssueProject(ctx, q, wsSlug, ident, actorID)
	if err != nil {
		return "", "", err
	}
	if role != RoleAdmin {
		return "", "", ErrForbidden
	}
	return wsID, projectID, nil
}

// projectCustomFieldsByID loads the project's custom fields keyed by id,
// for form field validation and submission type resolution.
func projectCustomFieldsByID(ctx context.Context, q queryQuerier, projectID string) (map[string]CustomField, error) {
	rows, err := q.Query(ctx,
		`SELECT `+customFieldColumns+` FROM custom_fields WHERE project_id = $1::uuid`,
		projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]CustomField{}
	for rows.Next() {
		f, err := scanCustomField(rows)
		if err != nil {
			return nil, err
		}
		out[f.ID] = f
	}
	return out, rows.Err()
}

// parseFormFieldKey splits a form field key into its kind: "title",
// "description", "priority", or "custom" with the custom field id. An
// unknown key or a custom: key that is not a canonical UUID is an invalid
// form field.
func parseFormFieldKey(key string) (kind string, customID string, err error) {
	switch key {
	case FormFieldTitle, FormFieldDescription, FormFieldPriority:
		return key, "", nil
	}
	if strings.HasPrefix(key, FormFieldCustomPrefix) {
		id := strings.TrimPrefix(key, FormFieldCustomPrefix)
		if !validServiceUUID(id) {
			return "", "", formDefinitionErr("unknown field key %q", key)
		}
		return FormFieldCustomPrefix, strings.ToLower(id), nil
	}
	return "", "", formDefinitionErr("unknown field key %q", key)
}

// validServiceUUID reports whether s is a canonical 8-4-4-4-12 hex UUID so
// a client-controlled custom:<uuid> key never reaches a ::uuid cast.
func validServiceUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i := 0; i < 36; i++ {
		ch := s[i]
		switch i {
		case 8, 13, 18, 23:
			if ch != '-' {
				return false
			}
		default:
			if !(ch >= '0' && ch <= '9' || ch >= 'a' && ch <= 'f' || ch >= 'A' && ch <= 'F') {
				return false
			}
		}
	}
	return true
}

// canonicalFieldLabel is the default display label for a form field when
// the author leaves the label blank: the core fields' canonical names,
// the custom field's own name.
func canonicalFieldLabel(key, customID string, customs map[string]CustomField) string {
	if key == FormFieldCustomPrefix {
		if f, ok := customs[customID]; ok {
			return f.Name
		}
		return "Custom field"
	}
	switch key {
	case FormFieldTitle:
		return "Title"
	case FormFieldDescription:
		return "Description"
	case FormFieldPriority:
		return "Priority"
	}
	return key
}

// validateIntakeFormFields enforces the form-definition contract at write
// time (honest 400s): every key known, no duplicates, title present
// exactly once and required, every custom:<uuid> one of the project's
// custom fields. Blank labels are defaulted to the canonical name. The
// returned slice is the normalized field list in the author's order.
func validateIntakeFormFields(customs map[string]CustomField, fields []IntakeFormField) ([]IntakeFormField, error) {
	if len(fields) == 0 {
		return nil, formDefinitionErr("fields must include %q", FormFieldTitle)
	}
	seen := map[string]bool{}
	titleCount := 0
	out := make([]IntakeFormField, 0, len(fields))
	for _, f := range fields {
		kind, customID, err := parseFormFieldKey(f.Key)
		if err != nil {
			return nil, err
		}
		normKey := f.Key
		if kind == FormFieldCustomPrefix {
			cf, ok := customs[customID]
			if !ok {
				return nil, formDefinitionErr("custom field %q does not belong to this project", customID)
			}
			normKey = FormFieldCustomPrefix + cf.ID
		}
		if seen[normKey] {
			return nil, formDefinitionErr("duplicate field key %q", normKey)
		}
		seen[normKey] = true
		if kind == FormFieldTitle {
			titleCount++
		}
		label := strings.TrimSpace(f.Label)
		if label == "" {
			label = canonicalFieldLabel(kind, customID, customs)
		}
		out = append(out, IntakeFormField{Key: normKey, Label: label, Required: f.Required})
	}
	if titleCount != 1 {
		return nil, formDefinitionErr("fields must contain %q exactly once", FormFieldTitle)
	}
	for _, f := range out {
		if f.Key == FormFieldTitle && !f.Required {
			return nil, formDefinitionErr("the title field is always required")
		}
	}
	return out, nil
}

// insertIntakeFormTx inserts one form row; on the astronomically unlikely
// share_token collision it retries with a fresh token (same convention as
// share links — the UNIQUE constraint is the backstop).
func insertIntakeFormTx(ctx context.Context, tx pgx.Tx, projectID, intakeID, actorID string, in IntakeFormInput, fields []IntakeFormField) (*IntakeForm, error) {
	fieldsJSON, err := json.Marshal(fields)
	if err != nil {
		return nil, err
	}
	isEnabled := true
	if in.IsEnabled != nil {
		isEnabled = *in.IsEnabled
	}
	var descParam any
	if in.Description != nil {
		descParam = *in.Description
	}
	for i := 0; i < 3; i++ {
		token, err := generateIntakeFormToken()
		if err != nil {
			return nil, err
		}
		f, err := scanIntakeForm(tx.QueryRow(ctx,
			`INSERT INTO intake_forms (project_id, intake_id, name, description,
				is_enabled, share_token, fields, created_by)
			 VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6, $7::jsonb, $8::uuid)
			 RETURNING `+intakeFormColumns,
			projectID, intakeID, in.Name, descParam, isEnabled, token, string(fieldsJSON), actorID))
		if err == nil {
			return f, nil
		}
		if isUniqueViolation(err) {
			continue
		}
		return nil, err
	}
	return nil, errors.New("service: intake form token collision after retries")
}

// CreateIntakeForm creates a form on the project's default intake.
// Workspace admin (20) only.
func CreateIntakeForm(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, actorID string, in IntakeFormInput) (*IntakeForm, error) {
	if strings.TrimSpace(in.Name) == "" {
		return nil, formDefinitionErr("name is required")
	}
	_, projectID, err := resolveIntakeFormProject(ctx, pool, wsSlug, identifier, actorID)
	if err != nil {
		return nil, err
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	customs, err := projectCustomFieldsByID(ctx, tx, projectID)
	if err != nil {
		return nil, err
	}
	fields, err := validateIntakeFormFields(customs, in.Fields)
	if err != nil {
		return nil, err
	}
	// The form always submits into the project's default intake — reuse
	// the existing default-intake resolution, never a new concept.
	intakeID, err := defaultIntakeIDTx(ctx, tx, projectID)
	if err != nil {
		return nil, err
	}
	f, err := insertIntakeFormTx(ctx, tx, projectID, intakeID, actorID, in, fields)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return f, nil
}

// ListIntakeForms lists the project's forms, oldest first. Admin (20) only.
func ListIntakeForms(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, actorID string) ([]*IntakeForm, error) {
	_, projectID, err := resolveIntakeFormProject(ctx, pool, wsSlug, identifier, actorID)
	if err != nil {
		return nil, err
	}
	rows, err := pool.Query(ctx,
		`SELECT `+intakeFormColumns+` FROM intake_forms
		 WHERE project_id = $1::uuid ORDER BY created_at, id`,
		projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	forms := []*IntakeForm{}
	for rows.Next() {
		f, err := scanIntakeForm(rows)
		if err != nil {
			return nil, err
		}
		forms = append(forms, f)
	}
	return forms, rows.Err()
}

// getIntakeFormTx loads one form scoped to the project inside the caller's
// tx/pool; a miss (or another project's id) reads as not found — the form
// id space is not enumerable from the outside.
func getIntakeFormTx(ctx context.Context, q queryRower, projectID, id string) (*IntakeForm, error) {
	f, err := scanIntakeForm(q.QueryRow(ctx,
		`SELECT `+intakeFormColumns+` FROM intake_forms
		 WHERE id = $1::uuid AND project_id = $2::uuid`,
		id, projectID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) || isInvalidUUID(err) {
			return nil, ErrIntakeFormNotFound
		}
		return nil, err
	}
	return f, nil
}

// GetIntakeForm returns one form. Admin (20) only.
func GetIntakeForm(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, actorID, id string) (*IntakeForm, error) {
	_, projectID, err := resolveIntakeFormProject(ctx, pool, wsSlug, identifier, actorID)
	if err != nil {
		return nil, err
	}
	return getIntakeFormTx(ctx, pool, projectID, id)
}

// UpdateIntakeForm patches a form; fields are re-validated when present.
// Admin (20) only.
func UpdateIntakeForm(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, actorID, id string, patch IntakeFormPatch) (*IntakeForm, error) {
	_, projectID, err := resolveIntakeFormProject(ctx, pool, wsSlug, identifier, actorID)
	if err != nil {
		return nil, err
	}
	if patch.Name != nil && strings.TrimSpace(*patch.Name) == "" {
		return nil, formDefinitionErr("name is required")
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	f, err := getIntakeFormTx(ctx, tx, projectID, id)
	if err != nil {
		return nil, err
	}
	name := f.Name
	if patch.Name != nil {
		name = strings.TrimSpace(*patch.Name)
	}
	var descParam any = f.Description
	if patch.Description.Set {
		if patch.Description.Value == nil || strings.TrimSpace(*patch.Description.Value) == "" {
			descParam = nil
		} else {
			descParam = *patch.Description.Value
		}
	}
	isEnabled := f.IsEnabled
	if patch.IsEnabled != nil {
		isEnabled = *patch.IsEnabled
	}
	fields := f.Fields
	if patch.Fields != nil {
		customs, err := projectCustomFieldsByID(ctx, tx, projectID)
		if err != nil {
			return nil, err
		}
		fields, err = validateIntakeFormFields(customs, *patch.Fields)
		if err != nil {
			return nil, err
		}
	}
	fieldsJSON, err := json.Marshal(fields)
	if err != nil {
		return nil, err
	}
	updated, err := scanIntakeForm(tx.QueryRow(ctx,
		`UPDATE intake_forms
		 SET name = $2, description = $3, is_enabled = $4, fields = $5::jsonb,
		     updated_at = now()
		 WHERE id = $1::uuid
		 RETURNING `+intakeFormColumns,
		id, name, descParam, isEnabled, string(fieldsJSON)))
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return updated, nil
}

// DeleteIntakeForm deletes a form; its share token dies with it.
// Admin (20) only.
func DeleteIntakeForm(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, actorID, id string) error {
	_, projectID, err := resolveIntakeFormProject(ctx, pool, wsSlug, identifier, actorID)
	if err != nil {
		return err
	}
	ct, err := pool.Exec(ctx,
		`DELETE FROM intake_forms WHERE id = $1::uuid AND project_id = $2::uuid`,
		id, projectID)
	if err != nil {
		if isInvalidUUID(err) {
			return ErrIntakeFormNotFound
		}
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrIntakeFormNotFound
	}
	return nil
}

// RegenerateIntakeFormToken rotates a form's share token: the new token is
// returned and the old one is immediately invalid (unknown → 404).
// Admin (20) only.
func RegenerateIntakeFormToken(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, actorID, id string) (*IntakeForm, error) {
	_, projectID, err := resolveIntakeFormProject(ctx, pool, wsSlug, identifier, actorID)
	if err != nil {
		return nil, err
	}
	for i := 0; i < 3; i++ {
		token, err := generateIntakeFormToken()
		if err != nil {
			return nil, err
		}
		f, err := scanIntakeForm(pool.QueryRow(ctx,
			`UPDATE intake_forms
			 SET share_token = $3, updated_at = now()
			 WHERE id = $1::uuid AND project_id = $2::uuid
			 RETURNING `+intakeFormColumns,
			id, projectID, token))
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) || isInvalidUUID(err) {
				return nil, ErrIntakeFormNotFound
			}
			if isUniqueViolation(err) {
				continue
			}
			return nil, err
		}
		return f, nil
	}
	return nil, errors.New("service: intake form token collision after retries")
}

// formLookupRow is the token-scoped form read shared by the two public
// endpoints: the form, its project's workspace slug + identifier + name,
// all in one indexed query.
type formLookupRow struct {
	form        *IntakeForm
	wsID        string
	wsSlug      string
	identifier  string
	projectName string
}

// lookupFormByToken resolves the public capability: the form plus its
// project's workspace slug + identifier + name, in one indexed query.
// Unknown and disabled tokens read identically as ErrIntakeFormNotFound —
// never 404-vs-403, never distinguishing disabled from nonexistent.
func lookupFormByToken(ctx context.Context, pool *pgxpool.Pool, token string) (*formLookupRow, error) {
	r := &formLookupRow{form: &IntakeForm{}}
	var fieldsRaw json.RawMessage
	err := pool.QueryRow(ctx,
		`SELECT f.id::text, f.project_id::text, f.intake_id::text, f.name,
		        f.description, f.is_enabled, f.share_token, f.fields,
		        f.created_by::text, f.created_at, f.updated_at,
		        p.workspace_id::text, w.slug, p.identifier, p.name
		 FROM intake_forms f
		 JOIN projects p ON p.id = f.project_id
		 JOIN workspaces w ON w.id = p.workspace_id
		 WHERE f.share_token = $1 AND f.is_enabled`,
		token).Scan(
		&r.form.ID, &r.form.ProjectID, &r.form.IntakeID, &r.form.Name,
		&r.form.Description, &r.form.IsEnabled, &r.form.ShareToken, &fieldsRaw,
		&r.form.CreatedBy, &r.form.CreatedAt, &r.form.UpdatedAt,
		&r.wsID, &r.wsSlug, &r.identifier, &r.projectName)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrIntakeFormNotFound
		}
		return nil, err
	}
	r.form.Fields = []IntakeFormField{}
	if len(fieldsRaw) > 0 {
		if err := json.Unmarshal(fieldsRaw, &r.form.Fields); err != nil {
			return nil, fmt.Errorf("service: corrupt intake form fields: %w", err)
		}
	}
	return r, nil
}

// submissionErrf builds an honest 400 for a public submission, naming the
// offending field's display label.
func submissionErrf(format string, args ...any) error {
	return &FormSubmissionError{Message: fmt.Sprintf(format, args...)}
}

// GetPublicIntakeForm returns the sanitized form definition for anonymous
// submitters. No auth — the token is the capability; unknown and disabled
// tokens both read as ErrIntakeFormNotFound (404, never 403). Internal
// ids, created_by, and other forms are never exposed.
func GetPublicIntakeForm(ctx context.Context, pool *pgxpool.Pool, token string) (*PublicIntakeForm, error) {
	r, err := lookupFormByToken(ctx, pool, token)
	if err != nil {
		return nil, err
	}
	customs, err := projectCustomFieldsByID(ctx, pool, r.form.ProjectID)
	if err != nil {
		return nil, err
	}
	fields := make([]PublicIntakeFormField, 0, len(r.form.Fields))
	for _, f := range r.form.Fields {
		kind, customID, err := parseFormFieldKey(f.Key)
		if err != nil {
			// The definition was valid at write time; this is data
			// corruption — surface it, never a half-rendered form.
			return nil, err
		}
		pf := PublicIntakeFormField{
			Key:      f.Key,
			Label:    f.Label,
			Required: f.Required,
			Options:  []SelectOption{},
		}
		switch kind {
		case FormFieldTitle:
			pf.Type = FormFieldTitle
		case FormFieldDescription:
			pf.Type = FormFieldDescription
		case FormFieldPriority:
			pf.Type = FormFieldPriority
		default: // custom:
			cf, ok := customs[customID]
			if !ok {
				// The custom field was deleted from the project: the
				// form keeps its descriptor (never cascade-deleted),
				// the public view drops the unrenderable field, and
				// submit ignores its values warning-free.
				continue
			}
			pf.Type = cf.FieldType
			if cf.FieldType == CustomFieldSelect {
				pf.Options = cf.Options
			}
		}
		fields = append(fields, pf)
	}
	return &PublicIntakeForm{
		Name:        r.form.Name,
		Description: r.form.Description,
		ProjectName: r.projectName,
		Fields:      fields,
	}, nil
}

// parsePriorityValue accepts the app's priority values on submission: a
// JSON number or numeric string in 0-4 (the values issue creation
// accepts — see ErrInvalidPriority).
func parsePriorityValue(raw json.RawMessage) (int, error) {
	var i int
	if err := json.Unmarshal(raw, &i); err == nil {
		if i >= 0 && i <= 4 {
			return i, nil
		}
		return 0, errors.New("out of range")
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		if n, aerr := strconv.Atoi(strings.TrimSpace(s)); aerr == nil && n >= 0 && n <= 4 {
			return n, nil
		}
	}
	return 0, errors.New("not a priority")
}

// descriptionDoc wraps plain submitted text in the app's rich-text doc
// shape (the same {"type":"doc","content":[...]} envelope in-app creates
// carry), so the issue detail page renders it as a normal paragraph.
func descriptionDoc(text string) json.RawMessage {
	doc := map[string]any{
		"type": "doc",
		"content": []any{
			map[string]any{
				"type": "paragraph",
				"content": []any{
					map[string]any{"type": "text", "text": text},
				},
			},
		},
	}
	raw, _ := json.Marshal(doc)
	return json.RawMessage(raw)
}

// validateFormSubmission checks a public submission against the form's
// field contract and converts the values into issue-creation inputs:
// name, description doc, priority, and custom-field values keyed by
// field id. Unknown value keys are ignored; values for deleted custom
// fields are dropped warning-free.
func validateFormSubmission(form *IntakeForm, customs map[string]CustomField, values map[string]json.RawMessage) (name string, desc json.RawMessage, priority *int, customValues map[string]json.RawMessage, err error) {
	if values == nil {
		values = map[string]json.RawMessage{}
	}
	// Title is always present and always required (also enforced at
	// form-write time).
	raw, ok := values[FormFieldTitle]
	var title string
	if ok {
		if uerr := json.Unmarshal(raw, &title); uerr != nil {
			return "", nil, nil, nil, submissionErrf("title must be a string")
		}
	}
	if strings.TrimSpace(title) == "" {
		return "", nil, nil, nil, submissionErrf("title is required")
	}
	name = strings.TrimSpace(title)

	customValues = map[string]json.RawMessage{}
	for _, f := range form.Fields {
		kind, customID, perr := parseFormFieldKey(f.Key)
		if perr != nil {
			return "", nil, nil, nil, perr // corrupt definition — internal
		}
		raw, present := values[f.Key]
		blank := !present || strings.TrimSpace(string(raw)) == "" || strings.TrimSpace(string(raw)) == "null"

		switch kind {
		case FormFieldTitle:
			continue // handled above
		case FormFieldDescription:
			if f.Required && blank {
				return "", nil, nil, nil, submissionErrf("missing required field %q", f.Label)
			}
			if blank {
				continue
			}
			var s string
			if uerr := json.Unmarshal(raw, &s); uerr != nil {
				return "", nil, nil, nil, submissionErrf("field %q must be a string", f.Label)
			}
			if strings.TrimSpace(s) == "" {
				continue
			}
			desc = descriptionDoc(s)
		case FormFieldPriority:
			if blank {
				if f.Required {
					return "", nil, nil, nil, submissionErrf("missing required field %q", f.Label)
				}
				continue
			}
			p, perr := parsePriorityValue(raw)
			if perr != nil {
				return "", nil, nil, nil, submissionErrf("field %q must be a priority between 0 and 4", f.Label)
			}
			priority = &p
		default: // custom:
			cf, ok := customs[customID]
			if !ok {
				continue // deleted custom field — dropped warning-free
			}
			if !present || customRawIsEmpty(cf, raw) {
				if f.Required {
					return "", nil, nil, nil, submissionErrf("missing required field %q", f.Label)
				}
				continue
			}
			_, _, clear, verr := checkCustomValue(cf, raw)
			if verr != nil {
				var ve *CustomValueError
				msg := "has an invalid value"
				if errors.As(verr, &ve) {
					msg = ve.Message
				}
				return "", nil, nil, nil, submissionErrf("field %q %s", f.Label, msg)
			}
			if clear {
				continue
			}
			customValues[cf.ID] = raw
		}
	}
	return name, desc, priority, customValues, nil
}

// SubmitIntakeForm validates a public, unauthenticated submission and
// creates the issue through createIssueTx — the same in-transaction
// creation the in-app path uses — with Intake set so the issue lands in
// the project's intake inbox as pending. Attribution is SystemUserID
// (never a member). Returns the created issue.
func SubmitIntakeForm(ctx context.Context, pool *pgxpool.Pool, token string, values map[string]json.RawMessage) (*Issue, error) {
	r, err := lookupFormByToken(ctx, pool, token)
	if err != nil {
		return nil, err
	}
	customs, err := projectCustomFieldsByID(ctx, pool, r.form.ProjectID)
	if err != nil {
		return nil, err
	}
	name, desc, priority, customValues, err := validateFormSubmission(r.form, customs, values)
	if err != nil {
		return nil, err
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	iss, notified, err := createIssueTx(ctx, tx, r.wsID, r.form.ProjectID, r.identifier, SystemUserID, CreateIssueInput{
		Name:         name,
		Description:  desc,
		Priority:     priority,
		Intake:       true,
		IntakeSource: IntakeSourceForm,
		CustomValues: customValues,
	})
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return finishIssueCreate(ctx, pool, r.wsSlug, r.identifier, r.form.ProjectID, iss, notified)
}
