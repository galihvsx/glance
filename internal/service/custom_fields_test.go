package service

// Custom fields (C7T2): field CRUD + member scoping, bulk value set with
// type validation for all five types, cross-project 404s, required being
// stored-but-not-enforced, cascade on field delete, and type changes
// clearing values. Real test database, no skips.

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func setupCustomFieldTest(t *testing.T) (context.Context, *pgxpool.Pool, string, string, string) {
	t.Helper()
	ctx := context.Background()
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	actor := createTestUser(t, pool, uniqueTestEmail("customfield"))
	slug := uniqueTestSlug("cf-ws")
	createTestWorkspace(t, pool, "Custom Field Co", slug, actor)
	ident := uniqueTestIdentifier()
	createTestProject(t, pool, slug, actor, "Eng", ident)
	return ctx, pool, slug, ident, actor
}

func cfInput(name, fieldType string) CustomFieldInput {
	return CustomFieldInput{Name: name, FieldType: fieldType}
}

func mustCreateField(t *testing.T, ctx context.Context, pool *pgxpool.Pool, slug, ident, actor string, in CustomFieldInput) *CustomField {
	t.Helper()
	f, err := CreateCustomField(ctx, pool, slug, ident, actor, in)
	if err != nil {
		t.Fatalf("CreateCustomField(%q): %v", in.Name, err)
	}
	return f
}

func TestCustomFieldCRUD(t *testing.T) {
	ctx, pool, slug, ident, actor := setupCustomFieldTest(t)

	// Create one of every type.
	textF := mustCreateField(t, ctx, pool, slug, ident, actor, cfInput("Notes", CustomFieldText))
	numF := mustCreateField(t, ctx, pool, slug, ident, actor, cfInput("Story points", CustomFieldNumber))
	dateF := mustCreateField(t, ctx, pool, slug, ident, actor, cfInput("Due", CustomFieldDate))
	selF := mustCreateField(t, ctx, pool, slug, ident, actor, CustomFieldInput{
		Name:      "Severity",
		FieldType: CustomFieldSelect,
		Options:   json.RawMessage(`[{"value":"low"},{"value":"high","color":"#ff0000"}]`),
	})
	req := true
	pos := 3
	checkF := mustCreateField(t, ctx, pool, slug, ident, actor, CustomFieldInput{
		Name: "Flagged", FieldType: CustomFieldCheckbox, Required: &req, Position: &pos,
	})
	if !checkF.Required || checkF.Position != 3 {
		t.Fatalf("required/position not stored: %+v", checkF)
	}
	if len(selF.Options) != 2 || selF.Options[0].Value != "low" || selF.Options[1].Value != "high" {
		t.Fatalf("select options not stored: %+v", selF.Options)
	}

	// List: all five present.
	fields, err := ListCustomFields(ctx, pool, slug, ident, actor)
	if err != nil {
		t.Fatalf("ListCustomFields: %v", err)
	}
	if len(fields) != 5 {
		t.Fatalf("ListCustomFields: got %d fields, want 5", len(fields))
	}

	// Get one.
	got, err := GetCustomField(ctx, pool, slug, ident, actor, numF.ID)
	if err != nil {
		t.Fatalf("GetCustomField: %v", err)
	}
	if got.Name != "Story points" || got.FieldType != CustomFieldNumber {
		t.Fatalf("GetCustomField: %+v", got)
	}

	// Duplicate name → conflict.
	if _, err := CreateCustomField(ctx, pool, slug, ident, actor, cfInput("Notes", CustomFieldText)); !errors.Is(err, ErrCustomFieldConflict) {
		t.Fatalf("duplicate name: err = %v, want ErrCustomFieldConflict", err)
	}

	// Bad inputs.
	for _, in := range []CustomFieldInput{
		{Name: "", FieldType: CustomFieldText},
		{Name: "X", FieldType: "bogus"},
		{Name: "X", FieldType: CustomFieldSelect}, // select needs options
		{Name: "X", FieldType: CustomFieldSelect, Options: json.RawMessage(`[{"value":"a"},{"value":"a"}]`)},
		{Name: "X", FieldType: CustomFieldText, Options: json.RawMessage(`[{"value":"a"}]`)}, // options on non-select
	} {
		if _, err := CreateCustomField(ctx, pool, slug, ident, actor, in); !errors.Is(err, ErrInvalidCustomField) && !errors.Is(err, ErrNameRequired) {
			t.Fatalf("bad input %+v: err = %v, want invalid", in, err)
		}
	}

	// Patch: rename + reorder.
	newName := "Story Points (est)"
	newPos := 1
	patched, err := UpdateCustomField(ctx, pool, slug, ident, actor, numF.ID, CustomFieldPatch{Name: &newName, Position: &newPos})
	if err != nil {
		t.Fatalf("UpdateCustomField: %v", err)
	}
	if patched.Name != newName || patched.Position != 1 {
		t.Fatalf("patch not applied: %+v", patched)
	}
	// Empty patch → ErrNothingToUpdate.
	if _, err := UpdateCustomField(ctx, pool, slug, ident, actor, numF.ID, CustomFieldPatch{}); !errors.Is(err, ErrNothingToUpdate) {
		t.Fatalf("empty patch: err = %v, want ErrNothingToUpdate", err)
	}

	// Delete.
	if err := DeleteCustomField(ctx, pool, slug, ident, actor, dateF.ID); err != nil {
		t.Fatalf("DeleteCustomField: %v", err)
	}
	if _, err := GetCustomField(ctx, pool, slug, ident, actor, dateF.ID); !errors.Is(err, ErrCustomFieldNotFound) {
		t.Fatalf("get after delete: err = %v, want ErrCustomFieldNotFound", err)
	}
	_ = textF
	_ = checkF
}

func TestCustomFieldScoping(t *testing.T) {
	ctx, pool, slug, ident, actor := setupCustomFieldTest(t)
	guest := addGuestMember(t, ctx, pool, slug, actor)
	outsider := createTestUser(t, pool, uniqueTestEmail("cf-outsider"))

	f := mustCreateField(t, ctx, pool, slug, ident, actor, cfInput("Notes", CustomFieldText))

	// Guest may read.
	if _, err := ListCustomFields(ctx, pool, slug, ident, guest); err != nil {
		t.Fatalf("guest list: %v", err)
	}
	if _, err := GetCustomField(ctx, pool, slug, ident, guest, f.ID); err != nil {
		t.Fatalf("guest get: %v", err)
	}
	// Guest may not mutate.
	if _, err := CreateCustomField(ctx, pool, slug, ident, guest, cfInput("X", CustomFieldText)); !errors.Is(err, ErrForbidden) {
		t.Fatalf("guest create: err = %v, want ErrForbidden", err)
	}
	if err := DeleteCustomField(ctx, pool, slug, ident, guest, f.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("guest delete: err = %v, want ErrForbidden", err)
	}
	// Non-member sees the workspace as not found.
	if _, err := ListCustomFields(ctx, pool, slug, ident, outsider); !errors.Is(err, ErrNotFound) {
		t.Fatalf("outsider list: err = %v, want ErrNotFound", err)
	}
	// Malformed id → invalid, not 404.
	if _, err := GetCustomField(ctx, pool, slug, ident, actor, "nope"); !errors.Is(err, ErrInvalidCustomFieldID) {
		t.Fatalf("bad id: err = %v, want ErrInvalidCustomFieldID", err)
	}

	// A field from another project is 404 here.
	slug2 := uniqueTestSlug("cf-ws2")
	createTestWorkspace(t, pool, "Other Co", slug2, actor)
	ident2 := uniqueTestIdentifier()
	createTestProject(t, pool, slug2, actor, "Other", ident2)
	other := mustCreateField(t, ctx, pool, slug2, ident2, actor, cfInput("Notes", CustomFieldText))
	if _, err := GetCustomField(ctx, pool, slug, ident, actor, other.ID); !errors.Is(err, ErrCustomFieldNotFound) {
		t.Fatalf("cross-project get: err = %v, want ErrCustomFieldNotFound", err)
	}
}

func setValues(t *testing.T, ctx context.Context, pool *pgxpool.Pool, slug, ident, actor, issueID string, doc string) map[string]CustomValue {
	t.Helper()
	var values map[string]json.RawMessage
	if err := json.Unmarshal([]byte(doc), &values); err != nil {
		t.Fatalf("bad test doc: %v", err)
	}
	out, err := SetCustomValues(ctx, pool, slug, ident, actor, issueID, values)
	if err != nil {
		t.Fatalf("SetCustomValues: %v", err)
	}
	return out
}

func TestSetCustomValuesAllTypes(t *testing.T) {
	ctx, pool, slug, ident, actor := setupCustomFieldTest(t)
	iss := createTestIssue(t, pool, slug, ident, actor, "valued issue")

	textF := mustCreateField(t, ctx, pool, slug, ident, actor, cfInput("Notes", CustomFieldText))
	numF := mustCreateField(t, ctx, pool, slug, ident, actor, cfInput("Points", CustomFieldNumber))
	dateF := mustCreateField(t, ctx, pool, slug, ident, actor, cfInput("Due", CustomFieldDate))
	selF := mustCreateField(t, ctx, pool, slug, ident, actor, CustomFieldInput{
		Name: "Severity", FieldType: CustomFieldSelect,
		Options: json.RawMessage(`[{"value":"low"},{"value":"high"}]`),
	})
	checkF := mustCreateField(t, ctx, pool, slug, ident, actor, cfInput("Flagged", CustomFieldCheckbox))

	doc := `{
		"` + textF.ID + `": "hello",
		"` + numF.ID + `": 42.5,
		"` + dateF.ID + `": "2026-10-15",
		"` + selF.ID + `": "high",
		"` + checkF.ID + `": true
	}`
	vals := setValues(t, ctx, pool, slug, ident, actor, iss.ID, doc)
	if len(vals) != 5 {
		t.Fatalf("SetCustomValues: got %d values, want 5", len(vals))
	}
	if vals[textF.ID].Value != "hello" {
		t.Fatalf("text value: %v", vals[textF.ID].Value)
	}
	if n, ok := vals[numF.ID].Value.(json.Number); !ok || n.String() != "42.5" {
		t.Fatalf("number value: %v (%T)", vals[numF.ID].Value, vals[numF.ID].Value)
	}
	if vals[dateF.ID].Value != "2026-10-15" {
		t.Fatalf("date value: %v", vals[dateF.ID].Value)
	}
	if vals[selF.ID].Value != "high" {
		t.Fatalf("select value: %v", vals[selF.ID].Value)
	}
	if vals[checkF.ID].Value != true {
		t.Fatalf("checkbox value: %v", vals[checkF.ID].Value)
	}

	// Read-back path agrees.
	vals2, err := GetIssueCustomValues(ctx, pool, slug, ident, actor, iss.ID)
	if err != nil {
		t.Fatalf("GetIssueCustomValues: %v", err)
	}
	if len(vals2) != 5 {
		t.Fatalf("GetIssueCustomValues: got %d values, want 5", len(vals2))
	}

	// A string that parses is accepted as a number; overwrite sticks.
	setValues(t, ctx, pool, slug, ident, actor, iss.ID, `{"`+numF.ID+`": "7"}`)
	vals3, _ := GetIssueCustomValues(ctx, pool, slug, ident, actor, iss.ID)
	if n, ok := vals3[numF.ID].Value.(json.Number); !ok || n.String() != "7" {
		t.Fatalf("string number overwrite: %v", vals3[numF.ID].Value)
	}

	// JSON null clears the value (row deleted).
	setValues(t, ctx, pool, slug, ident, actor, iss.ID, `{"`+textF.ID+`": null}`)
	vals4, _ := GetIssueCustomValues(ctx, pool, slug, ident, actor, iss.ID)
	if _, ok := vals4[textF.ID]; ok {
		t.Fatalf("null did not clear the text value")
	}

	// ClearCustomValue removes one value; clearing an unset value is a
	// silent no-op.
	if err := ClearCustomValue(ctx, pool, slug, ident, actor, iss.ID, checkF.ID); err != nil {
		t.Fatalf("ClearCustomValue: %v", err)
	}
	if err := ClearCustomValue(ctx, pool, slug, ident, actor, iss.ID, checkF.ID); err != nil {
		t.Fatalf("ClearCustomValue idempotent: %v", err)
	}
	vals5, _ := GetIssueCustomValues(ctx, pool, slug, ident, actor, iss.ID)
	if _, ok := vals5[checkF.ID]; ok {
		t.Fatalf("clear did not remove the checkbox value")
	}
}

func TestSetCustomValuesValidation(t *testing.T) {
	ctx, pool, slug, ident, actor := setupCustomFieldTest(t)
	iss := createTestIssue(t, pool, slug, ident, actor, "validation issue")

	numF := mustCreateField(t, ctx, pool, slug, ident, actor, cfInput("Points", CustomFieldNumber))
	dateF := mustCreateField(t, ctx, pool, slug, ident, actor, cfInput("Due", CustomFieldDate))
	selF := mustCreateField(t, ctx, pool, slug, ident, actor, CustomFieldInput{
		Name: "Severity", FieldType: CustomFieldSelect,
		Options: json.RawMessage(`[{"value":"low"}]`),
	})
	checkF := mustCreateField(t, ctx, pool, slug, ident, actor, cfInput("Flagged", CustomFieldCheckbox))
	textF := mustCreateField(t, ctx, pool, slug, ident, actor, cfInput("Notes", CustomFieldText))

	cases := []struct {
		name  string
		field *CustomField
		value string
	}{
		{"bad number", numF, `"abc"`},
		{"bool as number", numF, `true`},
		{"bad date shape", dateF, `"15/10/2026"`},
		{"bad date month", dateF, `"2026-13-01"`},
		{"date as number", dateF, `20261015`},
		{"invalid select option", selF, `"critical"`},
		{"checkbox as string", checkF, `"true"`},
		{"text as number", textF, `42`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var values map[string]json.RawMessage
			if err := json.Unmarshal([]byte(`{"`+tc.field.ID+`": `+tc.value+`}`), &values); err != nil {
				t.Fatalf("bad test doc: %v", err)
			}
			_, err := SetCustomValues(ctx, pool, slug, ident, actor, iss.ID, values)
			var ve *CustomValueError
			if !errors.As(err, &ve) {
				t.Fatalf("err = %v (%T), want *CustomValueError", err, err)
			}
			if ve.FieldID != tc.field.ID || ve.FieldName != tc.field.Name {
				t.Fatalf("CustomValueError names wrong field: %+v", ve)
			}
		})
	}

	// The failed bulk write wrote nothing (atomic).
	vals, err := GetIssueCustomValues(ctx, pool, slug, ident, actor, iss.ID)
	if err != nil {
		t.Fatalf("GetIssueCustomValues: %v", err)
	}
	if len(vals) != 0 {
		t.Fatalf("failed bulk set left %d values", len(vals))
	}

	// Empty values map → ErrCustomValuesEmpty.
	if _, err := SetCustomValues(ctx, pool, slug, ident, actor, iss.ID, map[string]json.RawMessage{}); !errors.Is(err, ErrCustomValuesEmpty) {
		t.Fatalf("empty values: err = %v, want ErrCustomValuesEmpty", err)
	}

	// Guest may read values but not set them.
	guest := addGuestMember(t, ctx, pool, slug, actor)
	if _, err := GetIssueCustomValues(ctx, pool, slug, ident, guest, iss.ID); err != nil {
		t.Fatalf("guest read values: %v", err)
	}
	var values map[string]json.RawMessage
	_ = json.Unmarshal([]byte(`{"`+textF.ID+`": "x"}`), &values)
	if _, err := SetCustomValues(ctx, pool, slug, ident, guest, iss.ID, values); !errors.Is(err, ErrForbidden) {
		t.Fatalf("guest set: err = %v, want ErrForbidden", err)
	}

	// A field from another project is 404.
	slug2 := uniqueTestSlug("cf-ws2")
	createTestWorkspace(t, pool, "Other Co", slug2, actor)
	ident2 := uniqueTestIdentifier()
	createTestProject(t, pool, slug2, actor, "Other", ident2)
	other := mustCreateField(t, ctx, pool, slug2, ident2, actor, cfInput("Notes", CustomFieldText))
	var cross map[string]json.RawMessage
	_ = json.Unmarshal([]byte(`{"`+other.ID+`": "x"}`), &cross)
	if _, err := SetCustomValues(ctx, pool, slug, ident, actor, iss.ID, cross); !errors.Is(err, ErrCustomFieldNotFound) {
		t.Fatalf("cross-project field: err = %v, want ErrCustomFieldNotFound", err)
	}

	// An issue from another project is 404.
	iss2 := createTestIssue(t, pool, slug2, ident2, actor, "other issue")
	var own map[string]json.RawMessage
	_ = json.Unmarshal([]byte(`{"`+textF.ID+`": "x"}`), &own)
	if _, err := SetCustomValues(ctx, pool, slug, ident, actor, iss2.ID, own); !errors.Is(err, ErrIssueNotFound) {
		t.Fatalf("cross-project issue: err = %v, want ErrIssueNotFound", err)
	}
}

func TestCustomFieldDeleteCascadesValues(t *testing.T) {
	ctx, pool, slug, ident, actor := setupCustomFieldTest(t)
	iss := createTestIssue(t, pool, slug, ident, actor, "cascade issue")
	f := mustCreateField(t, ctx, pool, slug, ident, actor, cfInput("Notes", CustomFieldText))
	setValues(t, ctx, pool, slug, ident, actor, iss.ID, `{"`+f.ID+`": "doomed"}`)

	if err := DeleteCustomField(ctx, pool, slug, ident, actor, f.ID); err != nil {
		t.Fatalf("DeleteCustomField: %v", err)
	}
	vals, err := GetIssueCustomValues(ctx, pool, slug, ident, actor, iss.ID)
	if err != nil {
		t.Fatalf("GetIssueCustomValues: %v", err)
	}
	if len(vals) != 0 {
		t.Fatalf("field delete left %d values", len(vals))
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM issue_custom_values WHERE field_id = $1::uuid`, f.ID).Scan(&n); err != nil {
		t.Fatalf("count values: %v", err)
	}
	if n != 0 {
		t.Fatalf("issue_custom_values still holds %d rows for the deleted field", n)
	}
}

func TestCustomFieldTypeChangeClearsValues(t *testing.T) {
	ctx, pool, slug, ident, actor := setupCustomFieldTest(t)
	iss := createTestIssue(t, pool, slug, ident, actor, "retype issue")
	f := mustCreateField(t, ctx, pool, slug, ident, actor, cfInput("Mood", CustomFieldText))
	setValues(t, ctx, pool, slug, ident, actor, iss.ID, `{"`+f.ID+`": "happy"}`)

	// Changing the type wipes the now-invalid values in the same
	// statement (documented in custom_fields.go).
	newType := CustomFieldCheckbox
	updated, err := UpdateCustomField(ctx, pool, slug, ident, actor, f.ID, CustomFieldPatch{FieldType: &newType})
	if err != nil {
		t.Fatalf("UpdateCustomField type change: %v", err)
	}
	if updated.FieldType != CustomFieldCheckbox {
		t.Fatalf("type not changed: %+v", updated)
	}
	vals, err := GetIssueCustomValues(ctx, pool, slug, ident, actor, iss.ID)
	if err != nil {
		t.Fatalf("GetIssueCustomValues: %v", err)
	}
	if len(vals) != 0 {
		t.Fatalf("type change left %d values", len(vals))
	}
	// And the field is usable again with the new type.
	setValues(t, ctx, pool, slug, ident, actor, iss.ID, `{"`+f.ID+`": true}`)
	vals2, _ := GetIssueCustomValues(ctx, pool, slug, ident, actor, iss.ID)
	if vals2[f.ID].Value != true {
		t.Fatalf("value after retype: %v", vals2[f.ID].Value)
	}
}

func TestCustomFieldRequiredIsStoredNotEnforced(t *testing.T) {
	ctx, pool, slug, ident, actor := setupCustomFieldTest(t)
	iss := createTestIssue(t, pool, slug, ident, actor, "required issue")
	req := true
	f := mustCreateField(t, ctx, pool, slug, ident, actor, CustomFieldInput{
		Name: "Required notes", FieldType: CustomFieldText, Required: &req,
	})
	// `required` is exposed on the field...
	got, err := GetCustomField(ctx, pool, slug, ident, actor, f.ID)
	if err != nil {
		t.Fatalf("GetCustomField: %v", err)
	}
	if !got.Required {
		t.Fatalf("required flag not stored")
	}
	// ...but it is NOT enforced on writes: a required field may stay
	// unset and may be cleared. This is the documented C7T2 choice
	// (enforcement would need issue-lifecycle integration).
	setValues(t, ctx, pool, slug, ident, actor, iss.ID, `{"`+f.ID+`": "set"}`)
	if err := ClearCustomValue(ctx, pool, slug, ident, actor, iss.ID, f.ID); err != nil {
		t.Fatalf("clearing a required field must be allowed: %v", err)
	}
	vals, _ := GetIssueCustomValues(ctx, pool, slug, ident, actor, iss.ID)
	if _, ok := vals[f.ID]; ok {
		t.Fatalf("clear did not remove the required field's value")
	}
}
