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
	if parsed.OrderBy == nil || len(parsed.OrderBy.Keys) != 1 || parsed.OrderBy.Keys[0].Field != "priority" || !parsed.OrderBy.Keys[0].Desc || parsed.OrderBy.OrderKey() != "-priority" {
		t.Fatalf("orderBy = %+v, want priority DESC (-priority)", parsed.OrderBy)
	}

	// Direction defaults to ASC; keywords are case-insensitive.
	parsed, err = ParsePQLWithOrder(`priority >= 3 order by created_at`)
	if err != nil {
		t.Fatalf("ParsePQLWithOrder ASC default: %v", err)
	}
	if parsed.OrderBy == nil || len(parsed.OrderBy.Keys) != 1 || parsed.OrderBy.Keys[0].Field != "created_at" || parsed.OrderBy.Keys[0].Desc || parsed.OrderBy.OrderKey() != "created_at" {
		t.Fatalf("orderBy = %+v, want created_at ASC", parsed.OrderBy)
	}
	parsed, err = ParsePQLWithOrder(`priority >= 3 OrDeR bY SEQUENCE_ID asc`)
	if err != nil {
		t.Fatalf("ParsePQLWithOrder mixed case: %v", err)
	}
	if parsed.OrderBy == nil || len(parsed.OrderBy.Keys) != 1 || parsed.OrderBy.Keys[0].Field != "sequence_id" || parsed.OrderBy.Keys[0].Desc {
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
		// Bare second field (no comma) and an unknown direction word
		// are trailing junk, never a silently dropped key. (Comma-
		// separated multi-key clauses are valid since C99T0; see
		// TestPQLMultiKeyOrderBy.)
		{`priority >= 3 ORDER BY priority created_at`, "unexpected"},
		{`priority >= 3 ORDER priority`, "expected BY"},
		{`priority >= 3 ORDER BY`, "expected a sort field"},
		{`priority >= 3 ORDER BY priority sideways`, "unexpected"},
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

// PQL LIMIT tests (C98T1): standalone and after ORDER BY, bounds,
// case-insensitive keyword, and every 400 shape.
func TestPQLLimit(t *testing.T) {
	// Standalone after the expression.
	parsed, err := ParsePQLWithOrder(`priority >= 3 LIMIT 10`)
	if err != nil {
		t.Fatalf("ParsePQLWithOrder: %v", err)
	}
	if parsed.OrderBy != nil {
		t.Fatalf("orderBy = %+v, want nil for standalone LIMIT", parsed.OrderBy)
	}
	if parsed.Limit == nil || parsed.Limit.Value != 10 {
		t.Fatalf("limit = %+v, want 10", parsed.Limit)
	}
	if parsed.Predicate.Kind != pqlCmp || parsed.Predicate.Field != "priority" {
		t.Fatalf("predicate = %+v, want priority comparison", parsed.Predicate)
	}

	// After ORDER BY (both orders compose); keywords case-insensitive.
	parsed, err = ParsePQLWithOrder(`priority >= 3 order by priority desc limit 5`)
	if err != nil {
		t.Fatalf("ParsePQLWithOrder ORDER BY + LIMIT: %v", err)
	}
	if parsed.OrderBy == nil || len(parsed.OrderBy.Keys) != 1 || parsed.OrderBy.Keys[0].Field != "priority" || !parsed.OrderBy.Keys[0].Desc {
		t.Fatalf("orderBy = %+v, want priority DESC", parsed.OrderBy)
	}
	if parsed.Limit == nil || parsed.Limit.Value != 5 {
		t.Fatalf("limit = %+v, want 5", parsed.Limit)
	}

	// Bounds are inclusive: 1 and 100 both parse.
	for _, n := range []int{1, 100} {
		parsed, err = ParsePQLWithOrder(`priority >= 0 LIMIT ` + itoa(n))
		if err != nil || parsed.Limit == nil || parsed.Limit.Value != n {
			t.Fatalf("LIMIT %d: parsed = %+v err = %v", n, parsed, err)
		}
	}

	// ParsePQL still returns just the predicate with LIMIT present.
	node, err := ParsePQL(`priority >= 3 LIMIT 10`)
	if err != nil || node.Kind != pqlCmp {
		t.Fatalf("ParsePQL with LIMIT: node = %+v err = %v", node, err)
	}

	// No LIMIT: nil, so per_page applies exactly as today.
	parsed, err = ParsePQLWithOrder(`priority >= 3 ORDER BY priority DESC`)
	if err != nil || parsed.Limit != nil {
		t.Fatalf("no LIMIT: parsed = %+v err = %v", parsed, err)
	}
}

func TestPQLLimitErrors(t *testing.T) {
	cases := []struct {
		query string
		want  string // substring of the error message
	}{
		// Bounds: 0 and 101 are 400, never a silent clamp.
		{`priority >= 3 LIMIT 0`, "out of range"},
		{`priority >= 3 LIMIT 101`, "out of range"},
		{`priority >= 3 LIMIT -1`, "out of range"},
		// Non-integer values are 400.
		{`priority >= 3 LIMIT abc`, "invalid LIMIT value"},
		{`priority >= 3 LIMIT 10.5`, "invalid LIMIT value"},
		{`priority >= 3 LIMIT "10"`, "expected a limit value"},
		{`priority >= 3 LIMIT`, "expected a limit value"},
		// Single-clause discipline, mirroring ORDER BY: a second
		// LIMIT, or LIMIT before ORDER BY, is a 400.
		{`priority >= 3 LIMIT 10 LIMIT 5`, "multiple LIMIT clauses"},
		{`priority >= 3 LIMIT 10 ORDER BY priority`, "before LIMIT"},
		{`priority >= 3 ORDER BY priority LIMIT 10 LIMIT 5`, "multiple LIMIT clauses"},
		// Trailing junk after LIMIT is a 400, not a silent drop.
		{`priority >= 3 LIMIT 10 foo`, "unexpected"},
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
	// Out-of-range position points at the value token.
	q := `priority >= 3 LIMIT 101`
	_, err := ParsePQLWithOrder(q)
	pqlErr, ok := err.(*PQLQueryError)
	if !ok || pqlErr.Pos != strings.Index(q, "101")+1 {
		t.Fatalf("LIMIT value position: err = %v, want position %d", err, strings.Index(q, "101")+1)
	}
}

// PQL multi-key ORDER BY tests (C99T0): comma-separated keys, per-key
// direction defaults, mixed directions, the composite OrderKey, and
// every multi-key 400 shape.
func TestPQLMultiKeyOrderBy(t *testing.T) {
	// Per-key direction defaults: DESC on the first key, ASC default
	// on the second; the composite key is comma-joined.
	parsed, err := ParsePQLWithOrder(`priority >= 3 ORDER BY priority DESC, updated_at`)
	if err != nil {
		t.Fatalf("ParsePQLWithOrder: %v", err)
	}
	if parsed.OrderBy == nil || len(parsed.OrderBy.Keys) != 2 {
		t.Fatalf("orderBy = %+v, want 2 keys", parsed.OrderBy)
	}
	k0, k1 := parsed.OrderBy.Keys[0], parsed.OrderBy.Keys[1]
	if k0.Field != "priority" || !k0.Desc || k1.Field != "updated_at" || k1.Desc {
		t.Fatalf("keys = %+v, want priority DESC + updated_at ASC", parsed.OrderBy.Keys)
	}
	if parsed.OrderBy.OrderKey() != "-priority,updated_at" {
		t.Fatalf("OrderKey = %q, want %q", parsed.OrderBy.OrderKey(), "-priority,updated_at")
	}

	// Mixed directions across three keys, explicit ASC included.
	parsed, err = ParsePQLWithOrder(`priority >= 0 ORDER BY priority DESC, sequence_id ASC, sort_order DESC`)
	if err != nil {
		t.Fatalf("ParsePQLWithOrder mixed: %v", err)
	}
	if parsed.OrderBy == nil || parsed.OrderBy.OrderKey() != "-priority,sequence_id,-sort_order" {
		t.Fatalf("OrderKey = %q, want %q", parsed.OrderBy.OrderKey(), "-priority,sequence_id,-sort_order")
	}

	// Keywords and field names are case-insensitive.
	parsed, err = ParsePQLWithOrder(`priority >= 0 order by PRIORITY desc, UPDATED_AT asc`)
	if err != nil {
		t.Fatalf("ParsePQLWithOrder mixed case: %v", err)
	}
	if parsed.OrderBy == nil || parsed.OrderBy.OrderKey() != "-priority,updated_at" {
		t.Fatalf("OrderKey = %q, want %q", parsed.OrderBy.OrderKey(), "-priority,updated_at")
	}

	// A single key still renders exactly as before (no comma).
	parsed, err = ParsePQLWithOrder(`priority >= 3 ORDER BY priority DESC`)
	if err != nil {
		t.Fatalf("ParsePQLWithOrder single: %v", err)
	}
	if parsed.OrderBy == nil || parsed.OrderBy.OrderKey() != "-priority" {
		t.Fatalf("OrderKey = %q, want %q", parsed.OrderBy.OrderKey(), "-priority")
	}

	// LIMIT composes after a multi-key ORDER BY.
	parsed, err = ParsePQLWithOrder(`priority >= 3 ORDER BY priority DESC, updated_at LIMIT 10`)
	if err != nil {
		t.Fatalf("ParsePQLWithOrder multi + LIMIT: %v", err)
	}
	if parsed.OrderBy == nil || len(parsed.OrderBy.Keys) != 2 || parsed.Limit == nil || parsed.Limit.Value != 10 {
		t.Fatalf("parsed = %+v, want 2 keys + LIMIT 10", parsed)
	}

	// ParsePQL still returns just the predicate with a multi-key
	// ORDER BY present (the clause is parsed and discarded safely).
	node, err := ParsePQL(`priority >= 3 ORDER BY priority DESC, updated_at`)
	if err != nil || node.Kind != pqlCmp {
		t.Fatalf("ParsePQL with multi-key ORDER BY: node = %+v err = %v", node, err)
	}
}

func TestPQLMultiKeyOrderByErrors(t *testing.T) {
	cases := []struct {
		query string
		want  string // substring of the error message
	}{
		// Unknown field in a later key is still an unknown-field 400.
		{`priority >= 3 ORDER BY priority, bogus`, "unknown sort field"},
		{`priority >= 3 ORDER BY priority DESC, created`, "unknown sort field"},
		// Duplicate field in one clause, any direction, any case.
		{`priority >= 3 ORDER BY priority, priority`, "duplicate sort field"},
		{`priority >= 3 ORDER BY priority DESC, PRIORITY ASC`, "duplicate sort field"},
		{`priority >= 3 ORDER BY updated_at, created_at, updated_at DESC`, "duplicate sort field"},
		// Trailing comma, missing field after a comma, comma before
		// LIMIT — all 400 with position, never a dropped key.
		{`priority >= 3 ORDER BY priority,`, "expected a sort field after ','"},
		{`priority >= 3 ORDER BY priority, LIMIT 10`, "expected a sort field after ','"},
		{`priority >= 3 ORDER BY priority,, updated_at`, "expected a sort field after ','"},
		// Direction without a following key is a trailing comma too.
		{`priority >= 3 ORDER BY priority DESC,`, "expected a sort field after ','"},
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
	// Duplicate-field position points at the second field token.
	q := `priority >= 3 ORDER BY priority, priority`
	_, err := ParsePQLWithOrder(q)
	pqlErr, ok := err.(*PQLQueryError)
	if !ok || pqlErr.Pos != strings.LastIndex(q, "priority")+1 {
		t.Fatalf("duplicate sort field position: err = %v, want position %d", err, strings.LastIndex(q, "priority")+1)
	}
	// Trailing-comma position points at the end of the query (EOF).
	q = `priority >= 3 ORDER BY priority,`
	_, err = ParsePQLWithOrder(q)
	pqlErr, ok = err.(*PQLQueryError)
	if !ok || pqlErr.Pos < 1 {
		t.Fatalf("trailing comma position: err = %v, want a 1-based position", err)
	}
}

// Multi-value cursor round-trip (C99T0): the new Values shape encodes
// and decodes, while the legacy single-Value shape still decodes for
// a single-key order.
func TestListCursorMultiValue(t *testing.T) {
	id := "00000000-0000-0000-0000-000000000000"
	enc := encodeListCursorMulti("-priority,updated_at", []string{"3", "2026-01-01T00:00:00Z"}, id)
	c, err := decodeListCursor(enc, "-priority,updated_at")
	if err != nil {
		t.Fatalf("decode multi-value cursor: %v", err)
	}
	if len(c.Values) != 2 || c.Values[0] != "3" || c.Values[1] != "2026-01-01T00:00:00Z" || c.ID != id {
		t.Fatalf("cursor = %+v, want Values [3 2026-01-01T00:00:00Z] + id", c)
	}
	// Order mismatch is rejected even for the multi-value shape.
	if _, err := decodeListCursor(enc, "-priority"); err == nil {
		t.Fatalf("decode under a different order: want ErrInvalidCursor, got nil")
	}
	// Legacy single-value shape still decodes for a single-key order.
	legacy := encodeListCursor("-priority", "3", id)
	c, err = decodeListCursor(legacy, "-priority")
	if err != nil || c.Value != "3" || c.ID != id {
		t.Fatalf("legacy cursor decode: cursor = %+v err = %v, want Value 3", c, err)
	}
	// A cursor with neither Value nor Values is rejected.
	bad := encodeListCursorMulti("-priority,updated_at", nil, id)
	if _, err := decodeListCursor(bad, "-priority,updated_at"); err == nil {
		t.Fatalf("empty multi-value cursor: want ErrInvalidCursor, got nil")
	}
}
