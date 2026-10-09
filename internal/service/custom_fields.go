package service

// Custom fields (C7T2): project-scoped typed keys an issue can carry a
// value for. Field types: text | number | date | select | checkbox.
// Select options are [{value, color?}] stored in custom_fields.options.
//
// Conventions (mirror templates/releases):
//   - Tenancy: every op resolves workspace membership + project via
//     resolveIssueProject. Bad slug or non-member caller surfaces
//     ErrNotFound ("workspace not found"); bad project identifier
//     surfaces ErrProjectNotFound; bad field id surfaces
//     ErrCustomFieldNotFound; bad issue id surfaces ErrIssueNotFound.
//   - Roles: any member (guest 5+) may read fields and values; every
//     mutation — field CRUD and value set/clear — needs member (15)+.
//   - Values are type-checked against the field at write time (400 with
//     an honest message on mismatch). A JSON null in the values map
//     clears that field's value (deletes the row), same as the explicit
//     DELETE .../custom-values/{fieldID}.
//   - `required` is stored and exposed so clients (C7T3) can render it,
//     but it is NOT enforced server-side on value writes: values may be
//     set, changed, and cleared regardless of required. Documented here
//     on purpose — enforcement would need issue-lifecycle integration
//     (e.g. state transitions), which is out of scope for C7T2.
//   - Changing a field's type deletes its existing values in the same
//     statement: values are only ever valid for the field's current
//     type. Changing select options is NOT retroactive — existing values
//     keep their old option value and are type-checked again only on the
//     next write.

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

// Custom field types.
const (
	CustomFieldText     = "text"
	CustomFieldNumber   = "number"
	CustomFieldDate     = "date"
	CustomFieldSelect   = "select"
	CustomFieldCheckbox = "checkbox"
)

var (
	// ErrCustomFieldNotFound is returned when the field id matches nothing
	// in the project — including when the field exists but belongs to a
	// different project. The distinction is deliberate: on this path the
	// caller has no business reading another project's fields (404 per
	// the C7T2 spec for cross-project field references).
	ErrCustomFieldNotFound = errors.New("service: custom field not found")
	// ErrCustomFieldConflict is returned when the field name is already
	// taken in the project (UNIQUE(project_id, name)).
	ErrCustomFieldConflict = errors.New("service: custom field name already exists")
	// ErrInvalidCustomField is returned for malformed field input: empty
	// name, unknown field_type, or options that don't fit the type.
	ErrInvalidCustomField = errors.New("service: invalid custom field")
	// ErrInvalidCustomFieldID is returned when a field id is not a
	// syntactically valid UUID. The handler maps it to 400 bad_request.
	ErrInvalidCustomFieldID = errors.New("service: invalid custom field id")
	// ErrCustomValuesEmpty is returned when the values map of a bulk set
	// is missing or empty.
	ErrCustomValuesEmpty = errors.New("service: values is required")
)

// CustomValueError is returned when a submitted value fails the field's
// type check. The handler maps it to 400 with the honest message: it
// names the field and exactly why the value was rejected.
type CustomValueError struct {
	FieldID   string
	FieldName string
	Message   string
}

func (e *CustomValueError) Error() string {
	return fmt.Sprintf("service: invalid value for custom field %q (%s): %s",
		e.FieldName, e.FieldID, e.Message)
}

// SelectOption is one entry of a select field's options array.
type SelectOption struct {
	Value string  `json:"value"`
	Color *string `json:"color,omitempty"`
}

// CustomField is one project custom field.
type CustomField struct {
	ID        string         `json:"id"`
	ProjectID string         `json:"project_id"`
	Name      string         `json:"name"`
	FieldType string         `json:"field_type"`
	Options   []SelectOption `json:"options"`
	Required  bool           `json:"required"`
	Position  int            `json:"position"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
}

// CustomFieldInput carries field creation fields. Options is the raw
// options document; nil/empty stores '[]'. Required and Position are
// optional (default false / 0).
type CustomFieldInput struct {
	Name      string
	FieldType string
	Options   json.RawMessage
	Required  *bool
	Position  *int
}

// CustomFieldPatch is a partial field update: nil fields are untouched.
// Options, when non-nil, replaces the whole options array; FieldType
// changes delete the field's existing values (same statement).
type CustomFieldPatch struct {
	Name      *string
	FieldType *string
	Options   *json.RawMessage
	Required  *bool
	Position  *int
}

// CustomValue is one set custom value on an issue, with the field's
// identity attached. Value is typed by field_type: string (text, date
// as "YYYY-MM-DD", select), json.Number (number), bool (checkbox).
type CustomValue struct {
	FieldID   string `json:"field_id"`
	Name      string `json:"name"`
	FieldType string `json:"field_type"`
	Value     any    `json:"value"`
}

// resolveCustomFieldProject resolves (workspaceID, projectID, role) for
// the caller. Guests may read; callers pass needMember to enforce member
// (15)+.
func resolveCustomFieldProject(ctx context.Context, q queryRower, wsSlug, identifier, actorID string, needMember bool) (wsID, projectID string, role int, err error) {
	wsID, projectID, role, err = resolveIssueProject(ctx, q, wsSlug, identifier, actorID)
	if err != nil {
		return "", "", 0, err
	}
	if needMember && role < RoleMember {
		return "", "", 0, ErrForbidden
	}
	return wsID, projectID, role, nil
}

const customFieldColumns = `id::text, project_id::text, name, field_type,
	options, required, position, created_at, updated_at`

func scanCustomField(row pgx.Row) (CustomField, error) {
	var f CustomField
	var options []byte
	err := row.Scan(
		&f.ID, &f.ProjectID, &f.Name, &f.FieldType,
		&options, &f.Required, &f.Position, &f.CreatedAt, &f.UpdatedAt,
	)
	if err != nil {
		return CustomField{}, err
	}
	f.Options = []SelectOption{}
	if len(options) > 0 {
		if err := json.Unmarshal(options, &f.Options); err != nil {
			// The column only ever holds what normalizeSelectOptions
			// accepted, so a parse failure is data corruption — surface
			// it loudly rather than serving a half field.
			return CustomField{}, fmt.Errorf("service: corrupt options for custom field %s: %w", f.ID, err)
		}
		if f.Options == nil {
			f.Options = []SelectOption{}
		}
	}
	return f, nil
}

// validCustomFieldType reports whether t is a known custom field type.
func validCustomFieldType(t string) bool {
	switch t {
	case CustomFieldText, CustomFieldNumber, CustomFieldDate, CustomFieldSelect, CustomFieldCheckbox:
		return true
	}
	return false
}

// normalizeSelectOptions parses and shape-checks the options document
// for the given field type. Non-select types must carry an empty/absent
// document; select types need at least one {value} entry with unique
// non-empty values. Returns the canonical JSON text to store.
func normalizeSelectOptions(fieldType string, raw json.RawMessage) (string, error) {
	trimmed := strings.TrimSpace(string(raw))
	empty := trimmed == "" || trimmed == "null" || trimmed == "[]"
	if fieldType != CustomFieldSelect {
		if !empty {
			return "", ErrInvalidCustomField
		}
		return "[]", nil
	}
	var opts []SelectOption
	if !empty {
		if err := json.Unmarshal(raw, &opts); err != nil {
			return "", ErrInvalidCustomField
		}
	}
	seen := map[string]bool{}
	for _, o := range opts {
		v := strings.TrimSpace(o.Value)
		if v == "" {
			return "", ErrInvalidCustomField
		}
		if seen[v] {
			return "", ErrInvalidCustomField
		}
		seen[v] = true
	}
	if len(opts) == 0 {
		return "", ErrInvalidCustomField
	}
	out, err := json.Marshal(opts)
	if err != nil {
		return "", ErrInvalidCustomField
	}
	return string(out), nil
}

// resolveCustomField loads a field of the project or
// ErrCustomFieldNotFound.
func resolveCustomField(ctx context.Context, q queryRower, projectID, fieldID string) (CustomField, error) {
	if !isUUIDFormat(fieldID) {
		return CustomField{}, ErrInvalidCustomFieldID
	}
	f, err := scanCustomField(q.QueryRow(ctx,
		`SELECT `+customFieldColumns+` FROM custom_fields
		  WHERE id = $1::uuid AND project_id = $2::uuid`,
		fieldID, projectID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return CustomField{}, ErrCustomFieldNotFound
		}
		return CustomField{}, err
	}
	return f, nil
}

// resolveCustomIssue confirms the issue is a live issue of the project.
// A bad UUID or a foreign/missing issue surfaces ErrIssueNotFound.
func resolveCustomIssue(ctx context.Context, q queryRower, projectID, issueID string) error {
	if !isUUIDFormat(issueID) {
		return ErrIssueNotFound
	}
	var one int
	err := q.QueryRow(ctx,
		`SELECT 1 FROM issues
		  WHERE id = $1::uuid AND project_id = $2::uuid AND deleted_at IS NULL`,
		issueID, projectID).Scan(&one)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrIssueNotFound
		}
		return err
	}
	return nil
}

// CreateCustomField creates a project custom field. Member (15)+.
func CreateCustomField(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, actorID string, in CustomFieldInput) (*CustomField, error) {
	_, projectID, _, err := resolveCustomFieldProject(ctx, pool, wsSlug, identifier, actorID, true)
	if err != nil {
		return nil, err
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return nil, ErrNameRequired
	}
	fieldType := strings.TrimSpace(in.FieldType)
	if !validCustomFieldType(fieldType) {
		return nil, ErrInvalidCustomField
	}
	options, err := normalizeSelectOptions(fieldType, in.Options)
	if err != nil {
		return nil, err
	}
	required := in.Required != nil && *in.Required
	position := 0
	if in.Position != nil {
		position = *in.Position
	}
	f, err := scanCustomField(pool.QueryRow(ctx,
		`INSERT INTO custom_fields (project_id, name, field_type, options, required, position)
		 VALUES ($1::uuid, $2, $3, $4::jsonb, $5, $6)
		 RETURNING `+customFieldColumns,
		projectID, name, fieldType, options, required, position))
	if err != nil {
		if isUniqueViolation(err) {
			return nil, ErrCustomFieldConflict
		}
		return nil, err
	}
	announceCustomFieldUpdated(ctx, pool, projectID, f.ID)
	return &f, nil
}

// ListCustomFields returns the project's custom fields ordered by
// position, then creation order. Any role may read.
func ListCustomFields(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, actorID string) ([]CustomField, error) {
	_, projectID, _, err := resolveCustomFieldProject(ctx, pool, wsSlug, identifier, actorID, false)
	if err != nil {
		return nil, err
	}
	rows, err := pool.Query(ctx,
		`SELECT `+customFieldColumns+`
		 FROM custom_fields WHERE project_id = $1::uuid
		 ORDER BY position, created_at`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CustomField{}
	for rows.Next() {
		f, err := scanCustomField(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// GetCustomField returns one custom field. Any role may read.
func GetCustomField(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, actorID, fieldID string) (*CustomField, error) {
	_, projectID, _, err := resolveCustomFieldProject(ctx, pool, wsSlug, identifier, actorID, false)
	if err != nil {
		return nil, err
	}
	f, err := resolveCustomField(ctx, pool, projectID, fieldID)
	if err != nil {
		return nil, err
	}
	return &f, nil
}

// UpdateCustomField applies a partial field update. Member (15)+. An
// empty patch is ErrNothingToUpdate. Changing field_type deletes the
// field's existing values in the same statement (values are only ever
// valid for the field's current type).
func UpdateCustomField(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, actorID, fieldID string, patch CustomFieldPatch) (*CustomField, error) {
	_, projectID, _, err := resolveCustomFieldProject(ctx, pool, wsSlug, identifier, actorID, true)
	if err != nil {
		return nil, err
	}
	current, err := resolveCustomField(ctx, pool, projectID, fieldID)
	if err != nil {
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
	effectiveType := current.FieldType
	if patch.FieldType != nil {
		t := strings.TrimSpace(*patch.FieldType)
		if !validCustomFieldType(t) {
			return nil, ErrInvalidCustomField
		}
		effectiveType = t
		addSet("field_type = ", t)
	}
	// Options: an explicit patch replaces the whole array; a type change
	// without an options patch resets to '[]' (validated below — select
	// without options is rejected, honestly).
	if patch.Options != nil || (patch.FieldType != nil && effectiveType != current.FieldType) {
		var raw json.RawMessage
		if patch.Options != nil {
			raw = *patch.Options
		}
		options, err := normalizeSelectOptions(effectiveType, raw)
		if err != nil {
			return nil, err
		}
		addSet("options = ", options)
		set[len(set)-1] += "::jsonb"
	}
	if patch.Required != nil {
		addSet("required = ", *patch.Required)
	}
	if patch.Position != nil {
		addSet("position = ", *patch.Position)
	}
	if len(set) == 0 {
		return nil, ErrNothingToUpdate
	}
	typeChanged := effectiveType != current.FieldType
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if typeChanged {
		if _, err := tx.Exec(ctx,
			`DELETE FROM issue_custom_values WHERE field_id = $1::uuid`, fieldID); err != nil {
			return nil, err
		}
	}
	set = append(set, "updated_at = now()")
	f, err := scanCustomField(tx.QueryRow(ctx,
		`UPDATE custom_fields SET `+strings.Join(set, ", ")+`
		  WHERE id = $1::uuid AND project_id = $2::uuid
		  RETURNING `+customFieldColumns,
		append([]any{fieldID, projectID}, setArgs...)...))
	if err != nil {
		if isUniqueViolation(err) {
			return nil, ErrCustomFieldConflict
		}
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	announceCustomFieldUpdated(ctx, pool, projectID, f.ID)
	return &f, nil
}

// DeleteCustomField removes a field. Member (15)+. Values die with the
// field via the ON DELETE CASCADE FK.
func DeleteCustomField(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, actorID, fieldID string) error {
	_, projectID, _, err := resolveCustomFieldProject(ctx, pool, wsSlug, identifier, actorID, true)
	if err != nil {
		return err
	}
	if _, err := resolveCustomField(ctx, pool, projectID, fieldID); err != nil {
		return err
	}
	if _, err := pool.Exec(ctx,
		`DELETE FROM custom_fields WHERE id = $1::uuid AND project_id = $2::uuid`,
		fieldID, projectID); err != nil {
		return err
	}
	announceCustomFieldUpdated(ctx, pool, projectID, fieldID)
	return nil
}

// customNumberString extracts a parseable number literal from a raw JSON
// value: a JSON number verbatim, or a quoted string that parses as a
// float. Anything else (bool, object, unparseable string) is rejected.
func customNumberString(raw json.RawMessage) (string, bool) {
	var num json.Number
	if err := json.Unmarshal(raw, &num); err == nil {
		s := strings.TrimSpace(num.String())
		if _, err := strconv.ParseFloat(s, 64); err == nil {
			return s, true
		}
		return "", false
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", false
	}
	s = strings.TrimSpace(s)
	if _, err := strconv.ParseFloat(s, 64); err != nil {
		return "", false
	}
	return s, true
}

// checkCustomValue type-checks one submitted value against the field,
// returning the column name and the bind value to write. A JSON null
// means "clear": it returns no column, and the caller deletes the row.
func checkCustomValue(field CustomField, raw json.RawMessage) (col string, val any, clear bool, err error) {
	fail := func(msg string) (string, any, bool, error) {
		return "", nil, false, &CustomValueError{FieldID: field.ID, FieldName: field.Name, Message: msg}
	}
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return "", nil, true, nil
	}
	switch field.FieldType {
	case CustomFieldText:
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return fail("must be a string")
		}
		return "value_text", s, false, nil
	case CustomFieldNumber:
		// Accept a JSON number verbatim or a string that parses.
		s, ok := customNumberString(raw)
		if !ok {
			return fail("must be a number")
		}
		return "value_number", s, false, nil
	case CustomFieldDate:
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return fail("must be a date string in YYYY-MM-DD format")
		}
		s = strings.TrimSpace(s)
		if _, err := time.Parse("2006-01-02", s); err != nil {
			return fail("must be a date string in YYYY-MM-DD format")
		}
		return "value_date", s, false, nil
	case CustomFieldSelect:
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return fail("must be one of the field's options")
		}
		s = strings.TrimSpace(s)
		for _, o := range field.Options {
			if o.Value == s {
				return "value_text", s, false, nil
			}
		}
		return fail("must be one of the field's options")
	case CustomFieldCheckbox:
		var b bool
		if err := json.Unmarshal(raw, &b); err != nil {
			return fail("must be a boolean")
		}
		return "value_bool", b, false, nil
	default:
		return fail("unknown field type")
	}
}

// SetCustomValues bulk-sets the issue's custom values in one transaction.
// values maps field_id → raw JSON value; a JSON null clears that field.
// Every field is resolved in the issue's project first: a field from
// another project (or nowhere) is ErrCustomFieldNotFound (404), a
// type-mismatched value is a *CustomValueError (400). Member (15)+.
func SetCustomValues(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, actorID, issueID string, values map[string]json.RawMessage) (map[string]CustomValue, error) {
	_, projectID, _, err := resolveCustomFieldProject(ctx, pool, wsSlug, identifier, actorID, true)
	if err != nil {
		return nil, err
	}
	if len(values) == 0 {
		return nil, ErrCustomValuesEmpty
	}
	if err := resolveCustomIssue(ctx, pool, projectID, issueID); err != nil {
		return nil, err
	}
	type write struct {
		field CustomField
		col   string
		val   any
		clear bool
	}
	writes := make([]write, 0, len(values))
	for fieldID, raw := range values {
		field, err := resolveCustomField(ctx, pool, projectID, fieldID)
		if err != nil {
			return nil, err
		}
		col, val, clear, err := checkCustomValue(field, raw)
		if err != nil {
			return nil, err
		}
		writes = append(writes, write{field: field, col: col, val: val, clear: clear})
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for _, w := range writes {
		if w.clear {
			if _, err := tx.Exec(ctx,
				`DELETE FROM issue_custom_values
				  WHERE issue_id = $1::uuid AND field_id = $2::uuid`,
				issueID, w.field.ID); err != nil {
				return nil, err
			}
			continue
		}
		// Exactly one value_* column is non-NULL per row; the upsert
		// resets the others to NULL so a type change of the stored value
		// (via a field type change it was deleted anyway) can never mix.
		if _, err := tx.Exec(ctx,
			`INSERT INTO issue_custom_values
			     (issue_id, field_id, value_text, value_number, value_date, value_bool)
			 VALUES ($1::uuid, $2::uuid,
			         CASE WHEN $3 = 'value_text' THEN $4::text END,
			         CASE WHEN $3 = 'value_number' THEN $4::numeric END,
			         CASE WHEN $3 = 'value_date' THEN $4::date END,
			         CASE WHEN $3 = 'value_bool' THEN ($4::text)::boolean END)
			 ON CONFLICT (issue_id, field_id) DO UPDATE SET
			     value_text = EXCLUDED.value_text,
			     value_number = EXCLUDED.value_number,
			     value_date = EXCLUDED.value_date,
			     value_bool = EXCLUDED.value_bool`,
			issueID, w.field.ID, w.col, customValueParam(w.val)); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	announceCustomFieldUpdated(ctx, pool, projectID, issueID)
	return GetIssueCustomValuesByProject(ctx, pool, projectID, issueID)
}

// customValueParam renders a typed value as a text bind param for the
// CASE-based upsert: numbers/dates/bools travel as text and are cast in
// SQL, so pgx never has to infer the param type.
func customValueParam(v any) any {
	switch t := v.(type) {
	case string:
		return t
	case bool:
		return strconv.FormatBool(t)
	default:
		return v
	}
}

// ClearCustomValue deletes one custom value. A field from another
// project is ErrCustomFieldNotFound (404). Member (15)+. Clearing an
// unset value is a silent no-op.
func ClearCustomValue(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, actorID, issueID, fieldID string) error {
	_, projectID, _, err := resolveCustomFieldProject(ctx, pool, wsSlug, identifier, actorID, true)
	if err != nil {
		return err
	}
	if err := resolveCustomIssue(ctx, pool, projectID, issueID); err != nil {
		return err
	}
	field, err := resolveCustomField(ctx, pool, projectID, fieldID)
	if err != nil {
		return err
	}
	if _, err := pool.Exec(ctx,
		`DELETE FROM issue_custom_values
		  WHERE issue_id = $1::uuid AND field_id = $2::uuid`,
		issueID, field.ID); err != nil {
		return err
	}
	announceCustomFieldUpdated(ctx, pool, projectID, issueID)
	return nil
}

// GetIssueCustomValuesByProject returns the issue's set custom values.
// Fields without a value are omitted. Internal: callers own the tenancy
// check (projectID is trusted).
func GetIssueCustomValuesByProject(ctx context.Context, pool *pgxpool.Pool, projectID, issueID string) (map[string]CustomValue, error) {
	rows, err := pool.Query(ctx,
		`SELECT f.id::text, f.name, f.field_type,
		        v.value_text, v.value_number::text, v.value_date::text, v.value_bool
		   FROM custom_fields f
		   LEFT JOIN issue_custom_values v
		     ON v.field_id = f.id AND v.issue_id = $1::uuid
		  WHERE f.project_id = $2::uuid
		  ORDER BY f.position, f.created_at`,
		issueID, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]CustomValue{}
	for rows.Next() {
		var cv CustomValue
		var text, number, date *string
		var b *bool
		if err := rows.Scan(&cv.FieldID, &cv.Name, &cv.FieldType, &text, &number, &date, &b); err != nil {
			return nil, err
		}
		switch {
		case text != nil:
			cv.Value = *text
		case number != nil:
			cv.Value = json.Number(*number)
		case date != nil:
			cv.Value = *date
		case b != nil:
			cv.Value = *b
		default:
			// No value set for this field — omit it.
			continue
		}
		out[cv.FieldID] = cv
	}
	return out, rows.Err()
}

// GetIssueCustomValues returns the issue's set custom values keyed by
// field id. Any role (guest 5+) may read.
func GetIssueCustomValues(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, actorID, issueID string) (map[string]CustomValue, error) {
	_, projectID, _, err := resolveCustomFieldProject(ctx, pool, wsSlug, identifier, actorID, false)
	if err != nil {
		return nil, err
	}
	if err := resolveCustomIssue(ctx, pool, projectID, issueID); err != nil {
		return nil, err
	}
	return GetIssueCustomValuesByProject(ctx, pool, projectID, issueID)
}

// announceCustomFieldUpdated fans out a custom_field.updated event on the
// project and workspace channels after field CRUD and value changes,
// mirroring the template/release pattern. subjectID is the field id for
// field mutations, the issue id for value mutations.
func announceCustomFieldUpdated(ctx context.Context, pool *pgxpool.Pool, projectID, subjectID string) {
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
		EventCustomFieldUpdated,
		map[string]string{"id": subjectID},
	)
}
