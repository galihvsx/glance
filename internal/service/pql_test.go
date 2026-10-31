package service

// PQL parser unit tests (C91T0): valid shapes, precedence, and every
// error class — syntax errors from ParsePQL, semantic errors from
// ValidatePQL. No DB: name resolution is covered by the service tests.

import (
	"errors"
	"strings"
	"testing"
)

func parseAndValidatePQL(t *testing.T, q string) *pqlNode {
	t.Helper()
	node, err := ParsePQL(q)
	if err != nil {
		t.Fatalf("ParsePQL(%q): %v", q, err)
	}
	if err := ValidatePQL(node); err != nil {
		t.Fatalf("ValidatePQL(%q): %v", q, err)
	}
	return node
}

func TestPQLParseSimpleComparison(t *testing.T) {
	node := parseAndValidatePQL(t, `priority = High`)
	if node.Kind != pqlCmp || node.Field != "priority" || node.Op != "=" || len(node.Values) != 1 || node.Values[0].Text != "High" {
		t.Fatalf("node = %+v, want priority = High comparison", node)
	}
}

func TestPQLParseQuotedStringWithSpaces(t *testing.T) {
	node := parseAndValidatePQL(t, `state = "In Progress"`)
	if node.Field != "state" || node.Values[0].Text != "In Progress" || !node.Values[0].Quoted {
		t.Fatalf("node = %+v, want quoted state value 'In Progress'", node)
	}
	// Escaped quote inside a single-quoted literal.
	node = parseAndValidatePQL(t, `label = 'it''s'`)
	if node.Values[0].Text != "it's" {
		t.Fatalf("escaped value = %q, want \"it's\"", node.Values[0].Text)
	}
}

func TestPQLParsePrecedence(t *testing.T) {
	// AND binds tighter than OR: a OR (b AND c).
	node := parseAndValidatePQL(t, `state = Todo OR state = Done AND priority >= 3`)
	if node.Kind != pqlOr || node.Left.Kind != pqlCmp || node.Right.Kind != pqlAnd {
		t.Fatalf("precedence: root = %v left = %v right = %v, want or(cmp, and)", node.Kind, node.Left.Kind, node.Right.Kind)
	}
	// NOT binds tightest: (NOT a) AND b.
	node = parseAndValidatePQL(t, `NOT state = Done AND priority = High`)
	if node.Kind != pqlAnd || node.Left.Kind != pqlNot || node.Right.Kind != pqlCmp {
		t.Fatalf("NOT precedence: root = %v left = %v, want and(not, cmp)", node.Kind, node.Left.Kind)
	}
	// Parentheses override: (a OR b) AND c.
	node = parseAndValidatePQL(t, `(state = Todo OR state = Done) AND priority = High`)
	if node.Kind != pqlAnd || node.Left.Kind != pqlOr {
		t.Fatalf("parens: root = %v left = %v, want and(or, ...)", node.Kind, node.Left.Kind)
	}
	// Double NOT is allowed.
	node = parseAndValidatePQL(t, `NOT NOT hasNoAssignee()`)
	if node.Kind != pqlNot || node.Left.Kind != pqlNot || node.Left.Left.Kind != pqlFunc {
		t.Fatalf("double NOT: %+v", node)
	}
}

func TestPQLParseCaseInsensitiveKeywordsAndFields(t *testing.T) {
	node := parseAndValidatePQL(t, `STATE = Todo and NOT LABEL in (frontend, backend) oR hasnoassignee()`)
	if node.Kind != pqlOr || node.Left.Kind != pqlAnd {
		t.Fatalf("case-insensitive parse: root = %v left = %v", node.Kind, node.Left.Kind)
	}
	if node.Left.Left.Field != "state" || node.Left.Right.Kind != pqlNot || node.Left.Right.Left.Field != "label" || node.Left.Right.Left.Op != "in" {
		t.Fatalf("left subtree = %+v, want and(state cmp, not(label in))", node.Left)
	}
	if node.Right.Kind != pqlFunc || node.Right.Func != "hasnoassignee" {
		t.Fatalf("func = %+v, want hasnoassignee", node.Right)
	}
}

func TestPQLParseFunctionsAndOperators(t *testing.T) {
	for _, q := range []string{
		`hasNoAssignee()`,
		`hasAssignee()`,
		`priority in (low, high, urgent)`,
		`priority <> 2`,
		`due < 2026-12-31`,
		`due_date >= 2026-01-01`,
		`target_date != 2026-06-01`,
		`created > 2026-01-01T00:00:00Z`,
		`updated_at <= 2026-10-31T23:59:59Z`,
		`assignee = me`,
		`assignee = none`,
		`assignee != me`,
		`state in (Todo, Done)`,
		`epic = Platform`,
		`epic = none`,
		`epic != Platform`,
		`epic in (Platform, "Mobile App")`,
		`epic in (Platform, none)`,
		`estimate = 3`,
		`estimate = none`,
		`estimate != 5`,
		`estimate in (S, M)`,
		`estimate in (M, none)`,
		`parent = ENG-123`,
		`parent = none`,
		`parent != ENG-123`,
		`parent in (ENG-123, none)`,
		`hasParent()`,
		`hasChildren()`,
	} {
		if _, err := ParsePQL(q); err != nil {
			t.Errorf("ParsePQL(%q): %v", q, err)
			continue
		}
		if err := ValidatePQL(mustParsePQL(t, q)); err != nil {
			t.Errorf("ValidatePQL(%q): %v", q, err)
		}
	}
}

func mustParsePQL(t *testing.T, q string) *pqlNode {
	t.Helper()
	node, err := ParsePQL(q)
	if err != nil {
		t.Fatalf("ParsePQL(%q): %v", q, err)
	}
	return node
}

func TestPQLParseErrors(t *testing.T) {
	cases := []struct {
		query string
		want  string // substring of the error message
	}{
		{``, "empty query"},
		{`   `, "empty query"},
		{`state = "unterminated`, "unterminated string"},
		{`state ! Todo`, "unexpected character"},
		{`state == Todo`, "expected a value"},
		{`state =`, "expected a value"},
		{`state Todo`, "expected an operator"},
		{`state = Todo extra`, "unexpected"},
		{`(state = Todo`, "expected )"},
		{`state = Todo)`, "unexpected"},
		{`state in Todo`, "expected ("},
		{`state in ()`, "expected a value"},
		{`state in (Todo`, "expected )"},
		{`hasNoAssignee(x)`, "takes no arguments"},
		{`state = AND`, "unexpected keyword"},
		{`state = Todo AND`, "unexpected"},
		{`AND state = Todo`, "unexpected keyword"},
		{`state = Todo OR`, "unexpected"},
		{`NOT`, "unexpected"},
		{`()`, "unexpected"},
		{`priority >> 3`, "expected a value"},
		{`state @ Todo`, "expected an operator"},
	}
	for _, tc := range cases {
		_, err := ParsePQL(tc.query)
		if err == nil {
			t.Errorf("ParsePQL(%q): want error containing %q, got nil", tc.query, tc.want)
			continue
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("ParsePQL(%q): error = %v, want substring %q", tc.query, err, tc.want)
		}
		var pqlErr *PQLQueryError
		if !errors.As(err, &pqlErr) {
			t.Errorf("ParsePQL(%q): error type = %T, want *PQLQueryError", tc.query, err)
		}
	}
}

func TestPQLValidateErrors(t *testing.T) {
	cases := []struct {
		query string
		want  string
	}{
		{`foo = Bar`, "unknown field"},
		{`hasGrandchildren()`, "unknown function"},
		{`parent > ENG-1`, "not supported for field"},
		{`state > Todo`, "not supported for field"},
		{`state < Todo`, "not supported for field"},
		{`label >= x`, "not supported for field"},
		{`assignee <= me`, "not supported for field"},
		{`epic > Platform`, "not supported for field"},
		{`estimate < 3`, "not supported for field"},
		{`priority = banana`, "unknown priority"},
		{`priority = 5`, "invalid priority"},
		{`priority = -1`, "invalid priority"},
		{`priority in (low, banana)`, "unknown priority"},
		{`due = yesterday`, "invalid date"},
		{`due = 31-12-2026`, "invalid date"},
		{`created = 2026-13-01`, "invalid date"},
		{`state = ""`, "empty value"},
	}
	for _, tc := range cases {
		node, err := ParsePQL(tc.query)
		if err != nil {
			t.Errorf("ParsePQL(%q): unexpected parse error: %v", tc.query, err)
			continue
		}
		if verr := ValidatePQL(node); verr == nil {
			t.Errorf("ValidatePQL(%q): want error containing %q, got nil", tc.query, tc.want)
		} else if !strings.Contains(verr.Error(), tc.want) {
			t.Errorf("ValidatePQL(%q): error = %v, want substring %q", tc.query, verr, tc.want)
		}
	}
}

func TestPQLParseErrorPositions(t *testing.T) {
	// The bad token's column is reported 1-based: "state =" is 7
	// chars, so the missing value (EOF) sits at column 8.
	_, err := ParsePQL(`state =`)
	pqlErr, ok := err.(*PQLQueryError)
	if !ok || pqlErr.Pos != 8 {
		t.Fatalf("state = : err = %v (pos ok=%v), want position 8", err, ok)
	}
	// Unknown field position: "foo" starts at column 1.
	node, _ := ParsePQL(`foo = Bar`)
	verr := ValidatePQL(node)
	if verr == nil || verr.Pos != 1 {
		t.Fatalf("foo = Bar: validation err = %v, want position 1", verr)
	}
}

// PQL ORDER BY tests (C98T0): single sort key, ASC default, DESC,
// case-insensitive keywords, and every 400 shape.
func TestPQLOrderBy(t *testing.T) {
	parsed, err := ParsePQLWithOrder(`priority >= 3 ORDER BY priority DESC`)
	if err != nil {
		t.Fatalf("ParsePQLWithOrder: %v", err)
	}
	if parsed.Predicate.Kind != pqlCmp || parsed.Predicate.Field != "priority" {
		t.Fatalf("predicate = %+v, want priority comparison", parsed.Predicate)
	}
	if parsed.OrderBy == nil || parsed.OrderBy.Field != "priority" || !parsed.OrderBy.Desc || parsed.OrderBy.OrderKey() != "-priority" {
		t.Fatalf("orderBy = %+v, want priority DESC (-priority)", parsed.OrderBy)
	}

	// Direction defaults to ASC; keywords are case-insensitive.
	parsed, err = ParsePQLWithOrder(`priority >= 3 order by created_at`)
	if err != nil {
		t.Fatalf("ParsePQLWithOrder ASC default: %v", err)
	}
	if parsed.OrderBy == nil || parsed.OrderBy.Field != "created_at" || parsed.OrderBy.Desc || parsed.OrderBy.OrderKey() != "created_at" {
		t.Fatalf("orderBy = %+v, want created_at ASC", parsed.OrderBy)
	}
	parsed, err = ParsePQLWithOrder(`priority >= 3 OrDeR bY SEQUENCE_ID asc`)
	if err != nil {
		t.Fatalf("ParsePQLWithOrder mixed case: %v", err)
	}
	if parsed.OrderBy == nil || parsed.OrderBy.Field != "sequence_id" || parsed.OrderBy.Desc {
		t.Fatalf("orderBy = %+v, want sequence_id ASC", parsed.OrderBy)
	}

	// Every sortable field parses.
	for _, f := range []string{"updated_at", "created_at", "sequence_id", "sort_order", "priority"} {
		if _, err := ParsePQLWithOrder(`priority >= 0 ORDER BY ` + f); err != nil {
			t.Errorf("ORDER BY %s: %v", f, err)
		}
	}

	// ParsePQL still returns just the predicate with ORDER BY present.
	node, err := ParsePQL(`priority >= 3 ORDER BY priority DESC`)
	if err != nil || node.Kind != pqlCmp {
		t.Fatalf("ParsePQL with ORDER BY: node = %+v err = %v", node, err)
	}

	// No ORDER BY: nil, so the ?order_by= param applies as today.
	parsed, err = ParsePQLWithOrder(`priority >= 3`)
	if err != nil || parsed.OrderBy != nil {
		t.Fatalf("no ORDER BY: parsed = %+v err = %v", parsed, err)
	}
}

func TestPQLOrderByErrors(t *testing.T) {
	cases := []struct {
		query string
		want  string // substring of the error message
	}{
		{`priority >= 3 ORDER BY bogus`, "unknown sort field"},
		// Predicate aliases are NOT sort keys.
		{`priority >= 3 ORDER BY created`, "unknown sort field"},
		{`priority >= 3 ORDER BY due`, "unknown sort field"},
		{`priority >= 3 ORDER BY state`, "unknown sort field"},
		// Multi-key: comma and bare second field are both 400, never
		// a silently dropped second key.
		{`priority >= 3 ORDER BY priority, created_at`, "multiple sort keys"},
		{`priority >= 3 ORDER BY priority DESC, created_at`, "multiple sort keys"},
		{`priority >= 3 ORDER BY priority created_at`, "single sort key"},
		{`priority >= 3 ORDER priority`, "expected BY"},
		{`priority >= 3 ORDER BY`, "expected a sort field"},
		{`priority >= 3 ORDER BY priority sideways`, "single sort key"},
	}
	for _, tc := range cases {
		_, err := ParsePQLWithOrder(tc.query)
		if err == nil {
			t.Errorf("ParsePQLWithOrder(%q): want error containing %q, got nil", tc.query, tc.want)
			continue
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("ParsePQLWithOrder(%q): error = %v, want substring %q", tc.query, err, tc.want)
		}
		var pqlErr *PQLQueryError
		if !errors.As(err, &pqlErr) {
			t.Errorf("ParsePQLWithOrder(%q): error type = %T, want *PQLQueryError", tc.query, err)
		} else if pqlErr.Pos < 1 {
			t.Errorf("ParsePQLWithOrder(%q): position = %d, want a 1-based position", tc.query, pqlErr.Pos)
		}
	}
	// Unknown sort-field position points at the field token.
	q := `priority >= 3 ORDER BY bogus`
	_, err := ParsePQLWithOrder(q)
	pqlErr, ok := err.(*PQLQueryError)
	if !ok || pqlErr.Pos != strings.Index(q, "bogus")+1 {
		t.Fatalf("unknown sort field position: err = %v, want position %d", err, strings.Index(q, "bogus")+1)
	}
}
