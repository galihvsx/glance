package service

// Required custom-field enforcement (C18T1): issue create demands a
// non-empty value for every required field of the project (drafts
// exempt); custom-value updates enforce partial semantics — only fields
// the update touches AND that were previously empty. Empty string / null
// counts as missing; checkbox false counts as present (explicit choice).
// Real test database, no skips.

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func mustRequiredField(t *testing.T, ctx context.Context, pool *pgxpool.Pool, slug, ident, actor, name, fieldType string) *CustomField {
	t.Helper()
	req := true
	return mustCreateField(t, ctx, pool, slug, ident, actor, CustomFieldInput{
		Name: name, FieldType: fieldType, Required: &req,
	})
}

func rawValues(pairs ...any) map[string]json.RawMessage {
	out := map[string]json.RawMessage{}
	for i := 0; i < len(pairs); i += 2 {
		out[pairs[i].(string)] = json.RawMessage(pairs[i+1].(string))
	}
	return out
}

func requireRequiredError(t *testing.T, err error, wantNames ...string) {
	t.Helper()
	if !errors.Is(err, ErrCustomFieldRequired) {
		t.Fatalf("err = %v (%T), want ErrCustomFieldRequired", err, err)
	}
	var re *CustomFieldRequiredError
	if !errors.As(err, &re) {
		t.Fatalf("err = %v (%T), want *CustomFieldRequiredError", err, err)
	}
	if len(re.FieldNames) != len(wantNames) {
		t.Fatalf("missing fields = %v, want %v", re.FieldNames, wantNames)
	}
	for i, n := range wantNames {
		if re.FieldNames[i] != n {
			t.Fatalf("missing fields = %v, want %v", re.FieldNames, wantNames)
		}
	}
}

func TestCreateIssueRequiredCustomFields(t *testing.T) {
	ctx, pool, slug, ident, actor := setupCustomFieldTest(t)
	textF := mustRequiredField(t, ctx, pool, slug, ident, actor, "Summary", CustomFieldText)
	checkF := mustRequiredField(t, ctx, pool, slug, ident, actor, "Reviewed", CustomFieldCheckbox)
	// Non-required control: never blocks.
	_ = mustCreateField(t, ctx, pool, slug, ident, actor, cfInput("Notes", CustomFieldText))

	cases := []struct {
		name      string
		values    map[string]json.RawMessage
		wantNames []string // nil = expect success
	}{
		{"no values at all", nil, []string{"Summary", "Reviewed"}},
		{"empty map", map[string]json.RawMessage{}, []string{"Summary", "Reviewed"}},
		{"empty string", rawValues(textF.ID, `""`, checkF.ID, `false`), []string{"Summary"}},
		{"whitespace string", rawValues(textF.ID, `"   "`, checkF.ID, `false`), []string{"Summary"}},
		{"null", rawValues(textF.ID, `null`, checkF.ID, `false`), []string{"Summary"}},
		{"checkbox absent", rawValues(textF.ID, `"ok"`), []string{"Reviewed"}},
		{"checkbox false is present", rawValues(textF.ID, `"ok"`, checkF.ID, `false`), nil},
		{"checkbox true", rawValues(textF.ID, `"ok"`, checkF.ID, `true`), nil},
		{"all filled", rawValues(textF.ID, `"launch notes"`, checkF.ID, `true`), nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			iss, err := CreateIssue(ctx, pool, slug, ident, actor, CreateIssueInput{
				Name:         "required matrix",
				CustomValues: tc.values,
			})
			if tc.wantNames == nil {
				if err != nil {
					t.Fatalf("CreateIssue: %v", err)
				}
				// Values persisted atomically with the issue.
				vals, err := GetIssueCustomValues(ctx, pool, slug, ident, actor, iss.ID)
				if err != nil {
					t.Fatalf("GetIssueCustomValues: %v", err)
				}
				if _, ok := vals[textF.ID]; !ok {
					t.Fatalf("text value not persisted: %v", vals)
				}
				if _, ok := vals[checkF.ID]; !ok {
					t.Fatalf("checkbox value not persisted: %v", vals)
				}
				return
			}
			requireRequiredError(t, err, tc.wantNames...)
		})
	}
}

func TestCreateIssueRequiredFieldsDraftExempt(t *testing.T) {
	ctx, pool, slug, ident, actor := setupCustomFieldTest(t)
	mustRequiredField(t, ctx, pool, slug, ident, actor, "Summary", CustomFieldText)

	// A draft is explicitly unfinished work: required fields don't block it.
	iss, err := CreateIssue(ctx, pool, slug, ident, actor, CreateIssueInput{
		Name:    "draft with gaps",
		IsDraft: true,
	})
	if err != nil {
		t.Fatalf("draft CreateIssue: %v", err)
	}
	if !iss.IsDraft {
		t.Fatalf("expected draft issue, got %+v", iss)
	}

	// But the same body as a real issue is rejected.
	_, err = CreateIssue(ctx, pool, slug, ident, actor, CreateIssueInput{Name: "real with gaps"})
	requireRequiredError(t, err, "Summary")
}

func TestSetCustomValuesRequiredPartialSemantics(t *testing.T) {
	ctx, pool, slug, ident, actor := setupCustomFieldTest(t)
	// The issue predates the required fields (created before enforcement
	// could apply) — mirrors issues created before C18T1.
	iss := createTestIssue(t, pool, slug, ident, actor, "partial semantics")
	textF := mustRequiredField(t, ctx, pool, slug, ident, actor, "Summary", CustomFieldText)
	otherF := mustRequiredField(t, ctx, pool, slug, ident, actor, "Owner", CustomFieldText)
	optF := mustCreateField(t, ctx, pool, slug, ident, actor, cfInput("Notes", CustomFieldText))

	// Touch a previously-empty required field with an empty value → 400.
	_, err := SetCustomValues(ctx, pool, slug, ident, actor, iss.ID, rawValues(textF.ID, `""`))
	requireRequiredError(t, err, "Summary")

	// Null on a previously-empty required field → 400 as well.
	_, err = SetCustomValues(ctx, pool, slug, ident, actor, iss.ID, rawValues(textF.ID, `null`))
	requireRequiredError(t, err, "Summary")

	// Fill it: now the field is no longer "previously empty".
	if _, err := SetCustomValues(ctx, pool, slug, ident, actor, iss.ID, rawValues(textF.ID, `"filled"`)); err != nil {
		t.Fatalf("fill required field: %v", err)
	}

	// A partial update that doesn't touch the other (still empty) required
	// field must NOT fail — unrelated edits stay usable.
	if _, err := SetCustomValues(ctx, pool, slug, ident, actor, iss.ID, rawValues(optF.ID, `"note"`)); err != nil {
		t.Fatalf("unrelated partial update: %v", err)
	}

	// Clearing a required field that already holds a value is an explicit
	// action, not an accidental gap: permitted by the partial-update rule.
	if _, err := SetCustomValues(ctx, pool, slug, ident, actor, iss.ID, rawValues(textF.ID, `null`)); err != nil {
		t.Fatalf("clear previously-set required field: %v", err)
	}

	// Re-touching the now-empty field with an empty value → 400 again.
	_, err = SetCustomValues(ctx, pool, slug, ident, actor, iss.ID, rawValues(textF.ID, `""`))
	requireRequiredError(t, err, "Summary")

	// And with a value → 200.
	if _, err := SetCustomValues(ctx, pool, slug, ident, actor, iss.ID, rawValues(textF.ID, `"again"`)); err != nil {
		t.Fatalf("refill required field: %v", err)
	}

	// The untouched, still-empty required field never blocks partial writes.
	vals, err := GetIssueCustomValues(ctx, pool, slug, ident, actor, iss.ID)
	if err != nil {
		t.Fatalf("GetIssueCustomValues: %v", err)
	}
	if _, ok := vals[otherF.ID]; ok {
		t.Fatalf("untouched required field unexpectedly has a value")
	}
}

func TestSetCustomValuesRequiredCheckboxFalse(t *testing.T) {
	ctx, pool, slug, ident, actor := setupCustomFieldTest(t)
	iss := createTestIssue(t, pool, slug, ident, actor, "checkbox required")
	checkF := mustRequiredField(t, ctx, pool, slug, ident, actor, "Reviewed", CustomFieldCheckbox)

	// false is an explicit choice: satisfies the required field.
	vals, err := SetCustomValues(ctx, pool, slug, ident, actor, iss.ID, rawValues(checkF.ID, `false`))
	if err != nil {
		t.Fatalf("checkbox false: %v", err)
	}
	cv, ok := vals[checkF.ID]
	if !ok || cv.Value != false {
		t.Fatalf("checkbox false not stored: %v", vals)
	}
}
