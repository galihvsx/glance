package service

// PQL — Plane Query Language subset (C91T0).
//
// A deterministic text filter for the project issue list, exposed as
// ?pql= on GET /api/v1/workspaces/:slug/projects/:identifier/issues.
// Grammar:
//
//	query      := expr [orderBy] [limit]
//	orderBy    := ORDER BY sortKey (',' sortKey)*
//	sortKey    := sortField [ASC | DESC]
//	limit      := LIMIT int (1..100)
//	sortField  := updated_at | created_at | sequence_id | sort_order | priority
//	expr       := orExpr
//	orExpr     := andExpr (OR andExpr)*
//	andExpr    := notExpr (AND notExpr)*
//	notExpr    := NOT notExpr | primary
//	primary    := '(' expr ')' | function | comparison
//	function   := hasNoAssignee() | hasAssignee() | hasParent() | hasChildren()
//	comparison := field op value | field IN '(' value (',' value)* ')'
//	op         := '=' | '!=' | '<>' (alias for !=) | '<' | '>' | '<=' | '>='
//
// Keywords (AND/OR/NOT/IN/ORDER/BY/ASC/DESC), field names, priority
// names, and function names are case-insensitive. String literals are
// single- or double-quoted ('' / "" escape the quote); bare values are
// unquoted words (names, numbers, YYYY-MM-DD dates, RFC3339 datetimes).
//
// ORDER BY (C98T0, multi-key C99T0) is a result-shape clause, not a
// filter: it sorts the list by one or more keys, each with its own
// optional ASC/DESC direction (default ASC per key). The list query
// sorts by those keys plus an id tiebreak in the LAST key's direction,
// and its cursor is minted for the composite order (comma-joined keys,
// e.g. priority,-updated_at). The sort fields map onto the
// existing listOrders whitelist in issue.go — the same five names,
// no aliases (created/updated are predicate aliases, not sort keys).
// An unknown sort field, a duplicate sort field, a trailing comma, or
// a missing field after a comma is a PQLQueryError (400) with position,
// never a silently dropped key. Precedence: a PQL ORDER BY wins over the ?order_by=
// param (the query is self-contained); without one, ?order_by=
// applies exactly as today. The EFFECTIVE order (PQL ORDER BY if
// present, else the param) is what cursors are validated and minted
// against, so a cursor minted under a different order is rejected.
//
// LIMIT (C98T1) is a result-shape clause, not a filter: it caps the
// response to at most n rows IN A SINGLE PAGE. Keyset cursors carry
// no row count, so a total-across-pages cap cannot be enforced
// server-side without a second query — instead the endpoint returns
// at most min(per_page, n) rows AND next_cursor is always null when
// LIMIT is present (no further paging; a client that wants more does
// not use LIMIT). A cursor param combined with LIMIT is a
// PQLQueryError (400), never a silently ignored cursor. LIMIT without
// ORDER BY uses the endpoint's default order. LIMIT comes after the
// optional ORDER BY (or standalone after the expression); a second
// LIMIT, or LIMIT before ORDER BY, is a 400. Out-of-range (0, >100)
// or non-integer LIMIT values are 400 with position, never a silent
// clamp or an empty list.
//
// Field table (all map to existing issue-model columns; no new columns):
//
//	field            column            values
//	state            i.state_id        state name (project-scoped)
//	priority         i.priority        name (none/low/medium/high/urgent) or 0-4
//	assignee         issue_assignees   member name, email, `me`, or `none`
//	label            issue_labels      label name (workspace-scoped)
//	epic             epic_issues       epic name (project-scoped) or `none`
//	estimate         i.estimate_point_id  estimate-point key within the project's
//	                                     estimate system, or `none`
//	parent           i.parent_id       parent issue key
//	                                     {IDENTIFIER}-{sequence_id} (e.g. ENG-123,
//	                                     project-scoped) or `none` (top-level issues)
//
// Functions: hasNoAssignee() (no assignees), hasAssignee() (at least
// one assignee), hasParent() (parent_id IS NOT NULL), hasChildren()
// (at least one live child: child.parent_id = i.id with the list
// query's default liveness — not deleted, not archived, not a draft —
// so archived/deleted/draft children never count).
//	due, due_date,   i.target_date     YYYY-MM-DD or RFC3339 (date part)
//	  target_date
//	start,           i.start_date      YYYY-MM-DD or RFC3339 (date part)
//	  start_date
//	created,         i.created_at      YYYY-MM-DD (whole day) or RFC3339
//	  created_at
//	updated,         i.updated_at      YYYY-MM-DD (whole day) or RFC3339
//	  updated_at
//
// Operators: name fields accept =/!=/in; priority and dates accept all
// operators plus in. A bare YYYY-MM-DD value against created/updated
// (timestamptz) means the whole UTC day: = becomes a [day, day+1)
// range, <= becomes < day+1, > becomes >= day+1, so date-only queries
// match the day users mean instead of a single midnight instant.
//
// Name resolution is scoped (states, epics, and estimate-point keys
// to the project; labels and assignees to the workspace). An unknown
// or ambiguous name is a
// PQLQueryError (400), never a silent empty list. All values are
// parameterized through the caller's arg() placeholder function —
// user input is never interpolated into SQL; only the operator and
// column (both from fixed whitelists) appear literally.
//
// Scope-survives contract (C95T0, extended C108T0, C109T0): Cycle,
// Module, and Epics are scope containers, not filter dimensions, and
// the date windows (DueAfter, DueBefore, StartAfter, StartBefore,
// Undated) are view windows, not filter dimensions. When PQL is
// present, withoutVisualFilters zeroes every visual filter dimension
// (state, labels, assignees, priorities, created/updated dates,
// parents, text, due_date_empty) but leaves Cycle, Module, Epics,
// and the date windows untouched, so the translated PQL predicate
// ANDs with the scope/window conditions: a query run inside a cycle,
// module, or epic stays inside it, and a query run inside a date
// window stays inside it. A foreign cycle/module/epic UUID
// therefore yields an empty list by construction (the project
// predicate in the list query confines the scope, same as the cycle
// scope today). In PQL mode the ?epic= scope is addressed by UUID,
// while a PQL `epic = "Name"` predicate resolves by name — the two
// AND, they do not conflict; likewise a PQL date predicate ANDs with
// the window, it does not replace it.
//
// OUT of scope (documented, not silently cut): AI text-to-PQL
// generation, and PQL on dashboards lists.
// Cycle/module/epic detail lists are queryable through the scope params:
// ?cycle= / ?module= / ?epic= survive PQL mode (this contract); the
// detail-page PQL UI lands separately.

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PQLQueryError is a client error in a PQL query: a parse error, an
// unknown field/operator/value, or a name that resolves to nothing (or
// ambiguously). Pos is the 1-based byte column of the offending token,
// or 0 when no single token is at fault. Handlers map it to 400 with
// the message and, when Pos > 0, details {"position": Pos}; it must
// never surface as a 500.
type PQLQueryError struct {
	Msg string
	Pos int
}

func (e *PQLQueryError) Error() string {
	if e.Pos > 0 {
		return fmt.Sprintf("pql: %s (at column %d)", e.Msg, e.Pos)
	}
	return "pql: " + e.Msg
}

func pqlErr(pos int, format string, args ...any) *PQLQueryError {
	return &PQLQueryError{Msg: fmt.Sprintf(format, args...), Pos: pos}
}

// ---------------------------------------------------------------------------
// Lexer
// ---------------------------------------------------------------------------

type pqlTokKind int

const (
	pqlEOF pqlTokKind = iota
	pqlWord
	pqlString
	pqlOp
	pqlLParen
	pqlRParen
	pqlComma
)

type pqlToken struct {
	kind   pqlTokKind
	text   string // word text, unescaped string value, or operator
	quoted bool   // true for string literals (quoted values)
	pos    int    // 1-based byte column of the token start
}

func isPQLWordStart(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '@'
}

func isPQLWordChar(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) ||
		r == '_' || r == '@' || r == '.' || r == '+' || r == '-' || r == ':'
}

// lexPQL tokenizes the whole query up front so the parser reports the
// first lexical error (e.g. an unterminated string) before any parse
// error, with a stable position.
func lexPQL(src string) ([]pqlToken, *PQLQueryError) {
	var toks []pqlToken
	i := 0
	for i < len(src) {
		r := rune(src[i])
		switch {
		case r == ' ' || r == '\t' || r == '\n' || r == '\r':
			i++
		case r == '(':
			toks = append(toks, pqlToken{kind: pqlLParen, text: "(", pos: i + 1})
			i++
		case r == ')':
			toks = append(toks, pqlToken{kind: pqlRParen, text: ")", pos: i + 1})
			i++
		case r == ',':
			toks = append(toks, pqlToken{kind: pqlComma, text: ",", pos: i + 1})
			i++
		case r == '\'' || r == '"':
			start := i
			quote := src[i]
			i++
			var sb strings.Builder
			closed := false
			for i < len(src) {
				if src[i] == quote {
					// Doubled quote escapes to one literal quote.
					if i+1 < len(src) && src[i+1] == quote {
						sb.WriteByte(quote)
						i += 2
						continue
					}
					closed = true
					i++
					break
				}
				sb.WriteByte(src[i])
				i++
			}
			if !closed {
				return nil, pqlErr(start+1, "unterminated string literal")
			}
			toks = append(toks, pqlToken{kind: pqlString, text: sb.String(), quoted: true, pos: start + 1})
		case r == '=' || r == '<' || r == '>':
			start := i
			op := string(r)
			// Only <=, >=, and <> are two-character operators: a
			// second = after = (i.e. ==) is two separate tokens, so
			// `state == Todo` fails as a missing value, not a valid
			// comparison.
			if i+1 < len(src) && ((r == '<' || r == '>') && src[i+1] == '=' || (r == '<' && src[i+1] == '>')) {
				op += string(src[i+1])
				i += 2
			} else {
				i++
			}
			if op == "<>" {
				op = "!="
			}
			toks = append(toks, pqlToken{kind: pqlOp, text: op, pos: start + 1})
		case r == '!':
			if i+1 < len(src) && src[i+1] == '=' {
				toks = append(toks, pqlToken{kind: pqlOp, text: "!=", pos: i + 1})
				i += 2
			} else {
				return nil, pqlErr(i+1, "unexpected character %q (want != for not-equal)", string(r))
			}
		case isPQLWordStart(r) || ((r == '-' || r == '+') && i+1 < len(src) && src[i+1] >= '0' && src[i+1] <= '9'):
			// A leading sign on a number (priority = -1) lexes as one
			// word so validation can reject it as an invalid priority
			// rather than an unexpected character.
			start := i
			for i < len(src) && isPQLWordChar(rune(src[i])) {
				i++
			}
			toks = append(toks, pqlToken{kind: pqlWord, text: src[start:i], pos: start + 1})
		default:
			return nil, pqlErr(i+1, "unexpected character %q", string(r))
		}
	}
	toks = append(toks, pqlToken{kind: pqlEOF, pos: len(src) + 1})
	return toks, nil
}

// ---------------------------------------------------------------------------
// Parser (recursive descent; NOT binds tightest, then AND, then OR)
// ---------------------------------------------------------------------------

type pqlNodeKind int

const (
	pqlAnd pqlNodeKind = iota
	pqlOr
	pqlNot
	pqlCmp
	pqlFunc
)

type pqlValue struct {
	Text   string
	Quoted bool
	Pos    int // 1-based column of the value token
}

type pqlNode struct {
	Kind  pqlNodeKind
	Left  *pqlNode // and/or: left; not: operand
	Right *pqlNode // and/or: right
	// Comparison nodes:
	Field  string // lowercased field name
	Op     string // "=", "!=", "in", "<", ">", "<=", ">="
	Values []pqlValue
	Pos    int // 1-based column of the field token
	// Function nodes:
	Func string // lowercased function name
}

type pqlParser struct {
	toks []pqlToken
	i    int
}

func (p *pqlParser) peek() pqlToken { return p.toks[p.i] }
func (p *pqlParser) next() pqlToken { t := p.toks[p.i]; p.i++; return t }
func (p *pqlParser) isKeyword(t pqlToken, kw string) bool {
	return t.kind == pqlWord && strings.EqualFold(t.text, kw)
}

// pqlOrderKey is one key of a parsed ORDER BY clause: Field is a bare
// listOrders name (no "-" prefix); Desc selects the "-" order key.
// Pos is the 1-based column of the sort-field token.
type pqlOrderKey struct {
	Field string
	Desc  bool
	Pos   int
}

// pqlOrderBy is a parsed ORDER BY clause (C98T0 single-key, C99T0
// multi-key): one or more sort keys in clause order. Pos is the
// 1-based column of the first sort-field token.
type pqlOrderBy struct {
	Keys []pqlOrderKey
	Pos  int
}

// OrderKey renders the composite order_by key the list query
// understands: the per-key keys comma-joined, each the bare field for
// ASC or "-" + field for DESC — the same keys as the listOrders
// whitelist in issue.go. A single key renders exactly as before
// ("-priority"), so single-key queries and their cursors are
// unchanged, and a cursor minted under one key list is never honored
// under another (the composite string differs).
func (o *pqlOrderBy) OrderKey() string {
	parts := make([]string, 0, len(o.Keys))
	for _, k := range o.Keys {
		if k.Desc {
			parts = append(parts, "-"+k.Field)
		} else {
			parts = append(parts, k.Field)
		}
	}
	return strings.Join(parts, ",")
}

// pqlLimit is a parsed LIMIT clause (C98T1): the row cap n (1..100,
// maxListPerPage). Pos is the 1-based column of the value token, so
// a cursor-combined-with-LIMIT rejection can point at the clause.
type pqlLimit struct {
	Value int
	Pos   int
}

// pqlParsedQuery is a fully parsed PQL query: the boolean predicate
// plus its optional ORDER BY and LIMIT clauses (nil when absent).
type pqlParsedQuery struct {
	Predicate *pqlNode
	OrderBy   *pqlOrderBy
	Limit     *pqlLimit
}

// pqlSortableFields is the ORDER BY whitelist (C98T0): the bare
// listOrders names in issue.go. Predicate aliases (created, updated,
// due, start, ...) are NOT sort keys — an ORDER BY naming one is an
// unknown-sort-field 400, not a silent alias.
var pqlSortableFields = map[string]bool{
	"updated_at": true, "created_at": true, "sequence_id": true,
	"sort_order": true, "priority": true,
}

const pqlKnownSortFields = "updated_at, created_at, sequence_id, sort_order, priority"

// ParsePQL parses a PQL query into a predicate tree. Syntax errors are
// *PQLQueryError; semantic checks (field/operator/value shapes) live in
// ValidatePQL, and name resolution in translatePQL. An empty query is
// an error here — callers treat a blank ?pql= as "no PQL" before
// calling, so an empty string reaching this function is a bug. The
// trailing ORDER BY / LIMIT clauses are parsed and discarded here;
// callers that need them (ListIssues) use ParsePQLWithOrder instead.
func ParsePQL(q string) (*pqlNode, error) {
	parsed, err := ParsePQLWithOrder(q)
	if err != nil {
		return nil, err
	}
	return parsed.Predicate, nil
}

// ParsePQLWithOrder parses a PQL query into its predicate plus its
// optional ORDER BY and LIMIT clauses. The clauses are validated here
// (unknown sort field, duplicate sort field, trailing comma, missing
// BY/field/direction, a non-integer or out-of-range LIMIT, a second
// LIMIT, or LIMIT before ORDER BY are all *PQLQueryError with position)
// so a bad clause never reaches the list query, where it could
// otherwise be mistaken for a param.
func ParsePQLWithOrder(q string) (*pqlParsedQuery, error) {
	if strings.TrimSpace(q) == "" {
		return nil, pqlErr(1, "empty query")
	}
	toks, lerr := lexPQL(q)
	if lerr != nil {
		return nil, lerr
	}
	p := &pqlParser{toks: toks}
	node, err := p.parseOr()
	if err != nil {
		return nil, err
	}
	orderBy, err := p.parseOrderBy()
	if err != nil {
		return nil, err
	}
	limit, err := p.parseLimit()
	if err != nil {
		return nil, err
	}
	if p.peek().kind != pqlEOF {
		// A second LIMIT, or an ORDER BY trailing a LIMIT (i.e. LIMIT
		// before ORDER BY), each get their own message — the generic
		// trailing-token error would hide which single-clause rule
		// was broken.
		switch t := p.peek(); {
		case p.isKeyword(t, "LIMIT"):
			return nil, pqlErr(t.pos, "multiple LIMIT clauses are not supported (only one LIMIT clause)")
		case p.isKeyword(t, "ORDER"):
			return nil, pqlErr(t.pos, "ORDER BY must come before LIMIT (got LIMIT before ORDER BY)")
		default:
			return nil, pqlErr(t.pos, "unexpected %q after expression", t.text)
		}
	}
	return &pqlParsedQuery{Predicate: node, OrderBy: orderBy, Limit: limit}, nil
}

// parseOrderBy consumes an optional trailing ORDER BY clause after the
// boolean expression, returning nil when the next token is not ORDER.
// Multi-key (C99T0): comma-separated sort keys, each <field>
// [ASC|DESC] with ASC the per-key default. A duplicate field (any
// direction, case-insensitive), a trailing comma, a missing field
// after a comma, or any token after the clause other than LIMIT/EOF
// is a 400 with position, never a silently dropped key — except a
// LIMIT keyword, which terminates the clause for parseLimit (C98T1).
func (p *pqlParser) parseOrderBy() (*pqlOrderBy, error) {
	if !p.isKeyword(p.peek(), "ORDER") {
		return nil, nil
	}
	p.next() // ORDER
	if !p.isKeyword(p.peek(), "BY") {
		return nil, pqlErr(p.peek().pos, "expected BY after ORDER")
	}
	p.next() // BY
	orderBy := &pqlOrderBy{}
	seen := map[string]bool{}
	for i := 0; ; i++ {
		fieldTok := p.peek()
		if fieldTok.kind != pqlWord || p.isKeyword(fieldTok, "LIMIT") {
			if i == 0 {
				return nil, pqlErr(fieldTok.pos, "expected a sort field after ORDER BY (sortable fields: %s)", pqlKnownSortFields)
			}
			return nil, pqlErr(fieldTok.pos, "expected a sort field after ',' in ORDER BY (sortable fields: %s)", pqlKnownSortFields)
		}
		p.next()
		field := strings.ToLower(fieldTok.text)
		if !pqlSortableFields[field] {
			return nil, pqlErr(fieldTok.pos, "unknown sort field %q (sortable fields: %s)", fieldTok.text, pqlKnownSortFields)
		}
		if seen[field] {
			return nil, pqlErr(fieldTok.pos, "duplicate sort field %q in ORDER BY", fieldTok.text)
		}
		seen[field] = true
		key := pqlOrderKey{Field: field, Pos: fieldTok.pos}
		if i == 0 {
			orderBy.Pos = fieldTok.pos
		}
		if p.isKeyword(p.peek(), "DESC") {
			key.Desc = true
			p.next()
		} else if p.isKeyword(p.peek(), "ASC") {
			p.next()
		}
		orderBy.Keys = append(orderBy.Keys, key)
		if p.peek().kind != pqlComma {
			break
		}
		p.next() // ',': the next loop iteration requires a field,
		// so a trailing comma (EOF, LIMIT, or another comma next)
		// is a 400 with the next token's position.
	}
	// A LIMIT keyword terminates the clause for parseLimit; EOF ends
	// the query. Anything else is trailing junk (a bare second field
	// without a comma, an unknown direction word, ...), a 400 here
	// (not the generic trailing-token error) so the message names
	// the ORDER BY clause.
	switch t := p.peek(); {
	case p.isKeyword(t, "LIMIT"):
		return orderBy, nil
	case t.kind == pqlEOF:
		return orderBy, nil
	default:
		return nil, pqlErr(t.pos, "unexpected %q after ORDER BY clause", t.text)
	}
}

// parseLimit consumes an optional trailing LIMIT clause after the
// boolean expression (or after ORDER BY), returning nil when the next
// token is not LIMIT. The value must be an integer 1..maxListPerPage:
// out-of-range or non-integer values are 400 with the value token's
// position, never a silent clamp. A second LIMIT, or an ORDER BY
// after a LIMIT, is rejected by ParsePQLWithOrder's trailing check.
func (p *pqlParser) parseLimit() (*pqlLimit, error) {
	if !p.isKeyword(p.peek(), "LIMIT") {
		return nil, nil
	}
	p.next() // LIMIT
	valTok := p.peek()
	if valTok.kind != pqlWord {
		return nil, pqlErr(valTok.pos, "expected a limit value after LIMIT (want an integer 1..%d)", maxListPerPage)
	}
	p.next()
	n, err := atoiStrict(valTok.text)
	if err != nil {
		return nil, pqlErr(valTok.pos, "invalid LIMIT value %q (want an integer 1..%d)", valTok.text, maxListPerPage)
	}
	if n < 1 || n > maxListPerPage {
		return nil, pqlErr(valTok.pos, "LIMIT value %d out of range (want 1..%d)", n, maxListPerPage)
	}
	return &pqlLimit{Value: n, Pos: valTok.pos}, nil
}

func (p *pqlParser) parseOr() (*pqlNode, error) {
	left, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	for p.isKeyword(p.peek(), "OR") {
		p.next()
		right, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		left = &pqlNode{Kind: pqlOr, Left: left, Right: right}
	}
	return left, nil
}

func (p *pqlParser) parseAnd() (*pqlNode, error) {
	left, err := p.parseNot()
	if err != nil {
		return nil, err
	}
	for p.isKeyword(p.peek(), "AND") {
		p.next()
		right, err := p.parseNot()
		if err != nil {
			return nil, err
		}
		left = &pqlNode{Kind: pqlAnd, Left: left, Right: right}
	}
	return left, nil
}

func (p *pqlParser) parseNot() (*pqlNode, error) {
	if p.isKeyword(p.peek(), "NOT") {
		p.next()
		operand, err := p.parseNot()
		if err != nil {
			return nil, err
		}
		return &pqlNode{Kind: pqlNot, Left: operand}, nil
	}
	return p.parsePrimary()
}

func (p *pqlParser) parsePrimary() (*pqlNode, error) {
	t := p.peek()
	switch t.kind {
	case pqlLParen:
		p.next()
		node, err := p.parseOr()
		if err != nil {
			return nil, err
		}
		if p.peek().kind != pqlRParen {
			return nil, pqlErr(p.peek().pos, "expected ) to close group opened earlier")
		}
		p.next()
		return node, nil
	case pqlWord:
		// Keywords are never field or function names: a leading AND /
		// OR / NOT / IN is a syntax error, not a comparison against a
		// field called "and".
		if p.isKeyword(t, "AND") || p.isKeyword(t, "OR") || p.isKeyword(t, "NOT") || p.isKeyword(t, "IN") {
			return nil, pqlErr(t.pos, "unexpected keyword %q", t.text)
		}
		// Function call: word immediately followed by ().
		if p.toks[p.i+1].kind == pqlLParen {
			name := p.next()
			p.next() // (
			if p.peek().kind != pqlRParen {
				return nil, pqlErr(p.peek().pos, "function %s() takes no arguments", name.text)
			}
			p.next() // )
			return &pqlNode{Kind: pqlFunc, Func: strings.ToLower(name.text), Pos: name.pos}, nil
		}
		// Comparison: field op value | field IN (values).
		field := p.next()
		opTok := p.peek()
		if opTok.kind == pqlOp {
			p.next()
			v, err := p.parseValue()
			if err != nil {
				return nil, err
			}
			return &pqlNode{Kind: pqlCmp, Field: strings.ToLower(field.text),
				Op: opTok.text, Values: []pqlValue{v}, Pos: field.pos}, nil
		}
		if p.isKeyword(opTok, "IN") {
			p.next()
			if p.peek().kind != pqlLParen {
				return nil, pqlErr(p.peek().pos, "expected ( after in")
			}
			p.next()
			var values []pqlValue
			v, err := p.parseValue()
			if err != nil {
				return nil, err
			}
			values = append(values, v)
			for p.peek().kind == pqlComma {
				p.next()
				v, err := p.parseValue()
				if err != nil {
					return nil, err
				}
				values = append(values, v)
			}
			if p.peek().kind != pqlRParen {
				return nil, pqlErr(p.peek().pos, "expected ) to close in list")
			}
			p.next()
			return &pqlNode{Kind: pqlCmp, Field: strings.ToLower(field.text),
				Op: "in", Values: values, Pos: field.pos}, nil
		}
		return nil, pqlErr(opTok.pos, "expected an operator (=, !=, <, >, <=, >=, in) after field %q", field.text)
	default:
		return nil, pqlErr(t.pos, "unexpected %q, want a field, function, or (", t.text)
	}
}

func (p *pqlParser) parseValue() (pqlValue, error) {
	t := p.peek()
	switch t.kind {
	case pqlString:
		p.next()
		return pqlValue{Text: t.text, Quoted: true, Pos: t.pos}, nil
	case pqlWord:
		if p.isKeyword(t, "AND") || p.isKeyword(t, "OR") || p.isKeyword(t, "NOT") || p.isKeyword(t, "IN") {
			return pqlValue{}, pqlErr(t.pos, "unexpected keyword %q as a value", t.text)
		}
		p.next()
		return pqlValue{Text: t.text, Quoted: false, Pos: t.pos}, nil
	default:
		return pqlValue{}, pqlErr(t.pos, "expected a value")
	}
}

// ---------------------------------------------------------------------------
// Semantic validation (no DB): field names, operator support, value shapes
// ---------------------------------------------------------------------------

type pqlFieldKind int

const (
	pqlNameField pqlFieldKind = iota // state, assignee, label, epic, estimate, parent
	pqlPriorityField
	pqlDateField // target_date / start_date (date columns)
	pqlTsField   // created_at / updated_at (timestamptz columns)
)

var pqlFieldKinds = map[string]pqlFieldKind{
	"state":       pqlNameField,
	"priority":    pqlPriorityField,
	"assignee":    pqlNameField,
	"label":       pqlNameField,
	"epic":        pqlNameField,
	"estimate":    pqlNameField,
	"parent":      pqlNameField,
	"due":         pqlDateField,
	"due_date":    pqlDateField,
	"target_date": pqlDateField,
	"start":       pqlDateField,
	"start_date":  pqlDateField,
	"created":     pqlTsField,
	"created_at":  pqlTsField,
	"updated":     pqlTsField,
	"updated_at":  pqlTsField,
}

const pqlKnownFields = "state, priority, assignee, label, epic, estimate, parent, due, due_date, target_date, start, start_date, created, created_at, updated, updated_at"

// pqlPriorityNames mirrors the Plane vocabulary used by the importer
// (0=none, 1=low, 2=medium, 3=high, 4=urgent).
var pqlPriorityNames = map[string]int{
	"none": 0, "low": 1, "medium": 2, "high": 3, "urgent": 4,
}

// parsePQLPriority maps a priority value token to its int: a number
// 0-4, or a name (case-insensitive); a quoted number counts as a number.
func parsePQLPriority(v pqlValue) (int, *PQLQueryError) {
	if n, err := atoiStrict(v.Text); err == nil {
		if n < 0 || n > 4 {
			return 0, pqlErr(v.Pos, "invalid priority %q (want 0-4 or none/low/medium/high/urgent)", v.Text)
		}
		return n, nil
	}
	if n, ok := pqlPriorityNames[strings.ToLower(v.Text)]; ok {
		return n, nil
	}
	return 0, pqlErr(v.Pos, "unknown priority %q (want 0-4 or none/low/medium/high/urgent)", v.Text)
}

// atoiStrict parses an all-digit (optionally sign-prefixed) integer.
func atoiStrict(s string) (int, error) {
	if s == "" {
		return 0, fmt.Errorf("empty")
	}
	neg := false
	rest := s
	if rest[0] == '-' || rest[0] == '+' {
		neg = rest[0] == '-'
		rest = rest[1:]
	}
	if rest == "" {
		return 0, fmt.Errorf("sign only")
	}
	n := 0
	for i := 0; i < len(rest); i++ {
		if rest[i] < '0' || rest[i] > '9' {
			return 0, fmt.Errorf("not a number")
		}
		n = n*10 + int(rest[i]-'0')
	}
	if neg {
		n = -n
	}
	return n, nil
}

type pqlDate struct {
	t        time.Time // midnight UTC for date-only values
	dateOnly bool
}

// parsePQLDate accepts YYYY-MM-DD or RFC3339.
func parsePQLDate(s string) (pqlDate, *PQLQueryError) {
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return pqlDate{t: t, dateOnly: true}, nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return pqlDate{t: t.UTC(), dateOnly: false}, nil
	}
	return pqlDate{}, pqlErr(0, "invalid date %q (want YYYY-MM-DD or RFC3339)", s)
}

// ValidatePQL checks field names, per-field operator support, and value
// shapes without touching the DB. TranslatePQL calls it first so a
// malformed query never reaches name resolution.
func ValidatePQL(n *pqlNode) *PQLQueryError {
	switch n.Kind {
	case pqlAnd, pqlOr:
		if err := ValidatePQL(n.Left); err != nil {
			return err
		}
		return ValidatePQL(n.Right)
	case pqlNot:
		return ValidatePQL(n.Left)
	case pqlFunc:
		switch n.Func {
		case "hasnoassignee", "hasassignee", "hasparent", "haschildren":
			return nil
		default:
			return pqlErr(n.Pos, "unknown function %q() (want hasNoAssignee(), hasAssignee(), hasParent(), or hasChildren())", n.Func)
		}
	case pqlCmp:
		kind, ok := pqlFieldKinds[n.Field]
		if !ok {
			return pqlErr(n.Pos, "unknown field %q (known fields: %s)", n.Field, pqlKnownFields)
		}
		switch kind {
		case pqlNameField:
			if n.Op != "=" && n.Op != "!=" && n.Op != "in" {
				return pqlErr(n.Pos, "operator %s is not supported for field %q (want =, !=, in)", n.Op, n.Field)
			}
		case pqlPriorityField, pqlDateField, pqlTsField:
			// All operators supported.
		}
		for _, v := range n.Values {
			if v.Quoted && v.Text == "" {
				return pqlErr(v.Pos, "empty value for field %q", n.Field)
			}
			switch kind {
			case pqlPriorityField:
				if _, err := parsePQLPriority(v); err != nil {
					return err
				}
			case pqlDateField, pqlTsField:
				if _, err := parsePQLDate(v.Text); err != nil {
					err.Pos = v.Pos
					return err
				}
			}
		}
		return nil
	default:
		return pqlErr(0, "invalid query node")
	}
}

// ---------------------------------------------------------------------------
// Translation to SQL (parameterized; reuses issueListConds shapes)
// ---------------------------------------------------------------------------

// withoutVisualFilters returns a copy of the input with every visual
// filter zeroed. ListIssues applies it whenever PQL is present so the
// "PQL wins, visual filters ignored" contract holds for direct service
// callers too, not just the HTTP handler. Scope containers (Cycle,
// Module, and — C108T0 — Epics) are NOT zeroed — they survive PQL
// mode and AND with the PQL predicate (C95T0 scope-survives contract,
// see the package header). Semantic (C108T0): in PQL mode the ?epic=
// param is a scope container addressed by UUID, while a PQL
// `epic = "Name"` predicate resolves by name — the two AND, they do
// not conflict, so a predicate naming a different epic than the scope
// param yields an empty list by construction. C109T0 semantic: the
// date windows (DueAfter, DueBefore, StartAfter, StartBefore,
// Undated) are view windows, not filter dimensions, so they are NOT
// zeroed either — they survive PQL mode exactly like the scope
// containers and AND with the predicate. A PQL date predicate
// (`due`, `start`, ...) ANDs with the window; it does not replace
// it, so a predicate date outside the window yields an empty list
// by construction. DueDateEmpty stays zeroed: it is a visual filter
// dimension ("target_date IS NULL"), not a window, so in PQL mode a
// ?due_date_empty= param is ignored, consistent with the "visual
// filters ignored" contract — and the visual path's
// due_date_empty + due_after/due_before 400 rule does not fire in
// PQL mode. Sort, pagination, fieldset, and list-mode flags
// (archived, draft, sequence lookup) are kept.
func (in ListIssuesInput) withoutVisualFilters() ListIssuesInput {
	in.State = ""
	in.Q = ""
	in.Priorities = nil
	in.Labels = nil
	in.Assignees = nil
	in.EstimatePoints = nil
	in.Parents = nil
	// Epics deliberately NOT zeroed (C108T0): scope container, see above.
	in.CreatedAfter = nil
	in.CreatedBefore = nil
	in.UpdatedAfter = nil
	in.UpdatedBefore = nil
	// Date windows deliberately NOT zeroed (C109T0): view windows,
	// see above. The query builder (issueListConds) already applies
	// these fields, so no builder change is needed — preservation
	// here is the whole fix.
	in.DueDateEmpty = false
	in.Subscribed = false
	return in
}

// Workspace scope (C112T0): an empty projectID means workspace scope
// (MyWork's /my-issues endpoint, which has no single project). The
// parser, validation, and every workspace-scoped field (label,
// assignee, priority, dates, has*()) are identical; only the
// per-project name fields change, and only in these ways:
//
//   - Multi-match set semantics: a state / epic / estimate-point name
//     resolves to the SET of ids bearing that name across the actor's
//     accessible projects, and translates with ANY — never an
//     ambiguity error. A name meaning "issues in a Todo-like state"
//     across projects is the query's plain reading, not an error to
//     "fix" back to single-id semantics. Zero matches is still an
//     unknown-name 400 with position, never a silent empty list.
//   - Resolution is confined to accessible projects (the same
//     projectAccessibleSQL predicate ListMyWork rows use): a name
//     existing ONLY in a private project the actor cannot reach is
//     unknown (400), never a match — otherwise the 400-vs-match
//     difference would leak the private project's taxonomy.
//   - Parent keys resolve by identifier across the workspace
//     (identifiers are workspace-unique): the project is looked up
//     by identifier, then the sequence inside it, under the same
//     accessibility confinement — a key naming an inaccessible
//     project is unknown (400), never a leak. Malformed-key 400s,
//     deleted-parent-unknown, and archived-parent-resolves are the
//     same as project scope.
//
// Project scope (projectID != "") is byte-for-byte today's behavior;
// its tests are the guard. The dispatch lives in translatePQLCmp,
// which routes only state/epic/estimate/parent to the workspace
// translators below when projectID is empty.
//
// translatePQL compiles a validated predicate tree to one parenthesized
// SQL condition. arg is the caller's placeholder function (same one
// issueListConds uses), so placeholders stay in sequence with the rest
// of the list query. Name resolution runs one query per distinct name;
// the query count is bounded by the query text, never by row count.
func translatePQL(ctx context.Context, pool *pgxpool.Pool, wsID, projectID, actorID string, n *pqlNode, arg func(any) string) (string, error) {
	if err := ValidatePQL(n); err != nil {
		return "", err
	}
	return translatePQLNode(ctx, pool, wsID, projectID, actorID, n, arg)
}

func translatePQLNode(ctx context.Context, pool *pgxpool.Pool, wsID, projectID, actorID string, n *pqlNode, arg func(any) string) (string, error) {
	switch n.Kind {
	case pqlAnd:
		l, err := translatePQLNode(ctx, pool, wsID, projectID, actorID, n.Left, arg)
		if err != nil {
			return "", err
		}
		r, err := translatePQLNode(ctx, pool, wsID, projectID, actorID, n.Right, arg)
		if err != nil {
			return "", err
		}
		return "(" + l + " AND " + r + ")", nil
	case pqlOr:
		l, err := translatePQLNode(ctx, pool, wsID, projectID, actorID, n.Left, arg)
		if err != nil {
			return "", err
		}
		r, err := translatePQLNode(ctx, pool, wsID, projectID, actorID, n.Right, arg)
		if err != nil {
			return "", err
		}
		return "(" + l + " OR " + r + ")", nil
	case pqlNot:
		c, err := translatePQLNode(ctx, pool, wsID, projectID, actorID, n.Left, arg)
		if err != nil {
			return "", err
		}
		return "(NOT " + c + ")", nil
	case pqlFunc:
		switch n.Func {
		case "hasnoassignee":
			return "NOT EXISTS (SELECT 1 FROM issue_assignees ia WHERE ia.issue_id = i.id)", nil
		case "hasassignee":
			return "EXISTS (SELECT 1 FROM issue_assignees ia WHERE ia.issue_id = i.id)", nil
		case "hasparent":
			return "i.parent_id IS NOT NULL", nil
		case "haschildren":
			// Live-child liveness mirrors the list query's default
			// mode (issueListConds: deleted_at IS NULL, and
			// archived_at IS NULL unless the archived list mode is
			// on, plus is_draft = FALSE unless draft mode is on).
			// The PQL translator has no archived/draft mode flag,
			// so hasChildren() always counts default-list-visible
			// children: archived, deleted, and draft children never
			// count, even when the outer list runs archived=1.
			return "EXISTS (SELECT 1 FROM issues c WHERE c.parent_id = i.id AND c.deleted_at IS NULL AND c.archived_at IS NULL AND c.is_draft = FALSE)", nil
		default:
			return "", pqlErr(n.Pos, "unknown function %q()", n.Func)
		}
	case pqlCmp:
		return translatePQLCmp(ctx, pool, wsID, projectID, actorID, n, arg)
	default:
		return "", pqlErr(0, "invalid query node")
	}
}

func translatePQLCmp(ctx context.Context, pool *pgxpool.Pool, wsID, projectID, actorID string, n *pqlNode, arg func(any) string) (string, error) {
	switch pqlFieldKinds[n.Field] {
	case pqlNameField:
		switch n.Field {
		case "state":
			if projectID == "" {
				return translatePQLStateWorkspace(ctx, pool, wsID, actorID, n, arg)
			}
			return translatePQLState(ctx, pool, projectID, n, arg)
		case "label":
			return translatePQLLabel(ctx, pool, wsID, n, arg)
		case "epic":
			if projectID == "" {
				return translatePQLEpicWorkspace(ctx, pool, wsID, actorID, n, arg)
			}
			return translatePQLEpic(ctx, pool, projectID, n, arg)
		case "estimate":
			if projectID == "" {
				return translatePQLEstimateWorkspace(ctx, pool, wsID, actorID, n, arg)
			}
			return translatePQLEstimate(ctx, pool, projectID, n, arg)
		case "parent":
			if projectID == "" {
				return translatePQLParentWorkspace(ctx, pool, wsID, actorID, n, arg)
			}
			return translatePQLParent(ctx, pool, projectID, n, arg)
		default: // assignee
			return translatePQLAssignee(ctx, pool, wsID, actorID, n, arg)
		}
	case pqlPriorityField:
		ints := make([]int, 0, len(n.Values))
		for _, v := range n.Values {
			p, err := parsePQLPriority(v)
			if err != nil {
				return "", err
			}
			ints = append(ints, p)
		}
		switch n.Op {
		case "=":
			return "i.priority = " + arg(ints[0]), nil
		case "!=":
			return "i.priority <> " + arg(ints[0]), nil
		case "in":
			return "i.priority = ANY(" + arg(ints) + ")", nil
		default: // <, >, <=, >= — op comes from the parser whitelist, never raw input
			return "i.priority " + n.Op + " " + arg(ints[0]), nil
		}
	case pqlDateField:
		col := "i.target_date"
		if n.Field == "start" || n.Field == "start_date" {
			col = "i.start_date"
		}
		dates := make([]string, 0, len(n.Values))
		for _, v := range n.Values {
			d, err := parsePQLDate(v.Text)
			if err != nil {
				err.Pos = v.Pos
				return "", err
			}
			dates = append(dates, d.t.Format("2006-01-02"))
		}
		switch n.Op {
		case "=":
			return col + " = " + arg(dates[0]) + "::date", nil
		case "!=":
			return col + " <> " + arg(dates[0]) + "::date", nil
		case "in":
			return col + " = ANY(" + arg(dates) + "::date[])", nil
		default:
			return col + " " + n.Op + " " + arg(dates[0]) + "::date", nil
		}
	case pqlTsField:
		col := "i.created_at"
		if n.Field == "updated" || n.Field == "updated_at" {
			col = "i.updated_at"
		}
		dates := make([]pqlDate, 0, len(n.Values))
		for _, v := range n.Values {
			d, err := parsePQLDate(v.Text)
			if err != nil {
				err.Pos = v.Pos
				return "", err
			}
			dates = append(dates, d)
		}
		// One condition per value for =/!=/in; a date-only value means
		// the whole UTC day (see the package comment).
		dayCond := func(d pqlDate, negate bool) string {
			day := d.t.Format("2006-01-02")
			next := d.t.Add(24 * time.Hour).Format("2006-01-02")
			inner := col + " >= " + arg(day) + "::timestamptz AND " + col + " < " + arg(next) + "::timestamptz"
			if negate {
				return "(NOT (" + inner + "))"
			}
			return "(" + inner + ")"
		}
		switch n.Op {
		case "=":
			if dates[0].dateOnly {
				return dayCond(dates[0], false), nil
			}
			return col + " = " + arg(dates[0].t) + "::timestamptz", nil
		case "!=":
			if dates[0].dateOnly {
				return dayCond(dates[0], true), nil
			}
			return col + " <> " + arg(dates[0].t) + "::timestamptz", nil
		case "in":
			parts := make([]string, 0, len(dates))
			for _, d := range dates {
				if d.dateOnly {
					parts = append(parts, dayCond(d, false))
				} else {
					parts = append(parts, col+" = "+arg(d.t)+"::timestamptz")
				}
			}
			return "(" + strings.Join(parts, " OR ") + ")", nil
		default:
			// Date-only comparisons use the day boundary the operator
			// implies: < day means before midnight; <= day means
			// through the end of the day (< day+1); > day means from
			// day+1 on; >= day means from midnight.
			if dates[0].dateOnly {
				switch n.Op {
				case "<":
					return col + " < " + arg(dates[0].t.Format("2006-01-02")) + "::timestamptz", nil
				case "<=":
					return col + " < " + arg(dates[0].t.Add(24*time.Hour).Format("2006-01-02")) + "::timestamptz", nil
				case ">":
					return col + " >= " + arg(dates[0].t.Add(24*time.Hour).Format("2006-01-02")) + "::timestamptz", nil
				default: // >=
					return col + " >= " + arg(dates[0].t.Format("2006-01-02")) + "::timestamptz", nil
				}
			}
			return col + " " + n.Op + " " + arg(dates[0].t) + "::timestamptz", nil
		}
	default:
		return "", pqlErr(n.Pos, "unknown field %q", n.Field)
	}
}

// resolvePQLIDs runs a name lookup returning every matching id. Zero
// matches is an unknown-name 400; more than one is an ambiguous-name
// 400 — never a silent pick or an empty list.
func resolvePQLIDs(ctx context.Context, pool *pgxpool.Pool, query string, scopeID, name string, pos int, kind, scope string) ([]string, error) {
	rows, err := pool.Query(ctx, query, scopeID, name)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	switch len(ids) {
	case 0:
		return nil, pqlErr(pos, "unknown %s %q in this %s", kind, name, scope)
	case 1:
		return ids, nil
	default:
		return nil, pqlErr(pos, "ambiguous %s %q: %d matches in this %s", kind, name, len(ids), scope)
	}
}

func translatePQLState(ctx context.Context, pool *pgxpool.Pool, projectID string, n *pqlNode, arg func(any) string) (string, error) {
	ids := make([]string, 0, len(n.Values))
	for _, v := range n.Values {
		got, err := resolvePQLIDs(ctx, pool,
			`SELECT id::text FROM states WHERE project_id = $1::uuid AND lower(name) = lower($2)`,
			projectID, v.Text, v.Pos, "state", "project")
		if err != nil {
			return "", err
		}
		ids = append(ids, got[0])
	}
	switch n.Op {
	case "=":
		return "i.state_id = " + arg(ids[0]) + "::uuid", nil
	case "!=":
		return "i.state_id <> " + arg(ids[0]) + "::uuid", nil
	default: // in
		return "i.state_id = ANY(" + arg(ids) + "::uuid[])", nil
	}
}

// resolvePQLWorkspaceIDs runs a name lookup across the actor's
// accessible projects in the workspace, returning EVERY matching id.
// The query takes ($1 wsID, $2 name, $3 actorID) and must itself join
// projects and apply projectAccessibleSQL, so a name existing only in
// an inaccessible private project never matches. Zero matches is an
// unknown-name 400 ("in this workspace"); there is deliberately NO
// ambiguity error — the set IS the answer in workspace scope (see the
// translatePQL comment above).
func resolvePQLWorkspaceIDs(ctx context.Context, pool *pgxpool.Pool, query, wsID, name, actorID string, pos int, kind string) ([]string, error) {
	rows, err := pool.Query(ctx, query, wsID, name, actorID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return nil, pqlErr(pos, "unknown %s %q in this workspace", kind, name)
	}
	return ids, nil
}

// translatePQLStateWorkspace resolves state names to the set of state
// ids across accessible projects (C112T0). `=` and `in` both use ANY
// over the union set; `!=` is NOT (... = ANY(...)) — the plan's
// spelling, and equivalent to project scope's <> since state_id is
// never NULL on a live issue row.
func translatePQLStateWorkspace(ctx context.Context, pool *pgxpool.Pool, wsID, actorID string, n *pqlNode, arg func(any) string) (string, error) {
	ids := make([]string, 0, len(n.Values))
	for _, v := range n.Values {
		got, err := resolvePQLWorkspaceIDs(ctx, pool,
			`SELECT s.id::text FROM states s JOIN projects p ON p.id = s.project_id
			  WHERE p.workspace_id = $1::uuid AND lower(s.name) = lower($2)
			    AND `+projectAccessibleSQL("p", "$3::uuid"),
			wsID, v.Text, actorID, v.Pos, "state")
		if err != nil {
			return "", err
		}
		ids = append(ids, got...)
	}
	switch n.Op {
	case "!=":
		return "NOT (i.state_id = ANY(" + arg(ids) + "::uuid[]))", nil
	default: // "=" and "in" are both set membership in workspace scope
		return "i.state_id = ANY(" + arg(ids) + "::uuid[])", nil
	}
}

func translatePQLLabel(ctx context.Context, pool *pgxpool.Pool, wsID string, n *pqlNode, arg func(any) string) (string, error) {
	ids := make([]string, 0, len(n.Values))
	for _, v := range n.Values {
		got, err := resolvePQLIDs(ctx, pool,
			`SELECT id::text FROM labels WHERE workspace_id = $1::uuid AND lower(name) = lower($2)`,
			wsID, v.Text, v.Pos, "label", "workspace")
		if err != nil {
			return "", err
		}
		ids = append(ids, got[0])
	}
	// Same EXISTS shape as issueListConds' label filter.
	switch n.Op {
	case "=":
		return "EXISTS (SELECT 1 FROM issue_labels il WHERE il.issue_id = i.id AND il.label_id = " + arg(ids[0]) + "::uuid)", nil
	case "!=":
		return "NOT EXISTS (SELECT 1 FROM issue_labels il WHERE il.issue_id = i.id AND il.label_id = " + arg(ids[0]) + "::uuid)", nil
	default: // in
		return "EXISTS (SELECT 1 FROM issue_labels il WHERE il.issue_id = i.id AND il.label_id = ANY(" + arg(ids) + "::uuid[]))", nil
	}
}

// resolvePQLAssigneeValue maps one assignee value token: `me` to the
// actor, `none` to the no-assignee marker, anything else to a workspace
// member id resolved by name or email (case-insensitive).
func resolvePQLAssigneeValue(ctx context.Context, pool *pgxpool.Pool, wsID, actorID string, v pqlValue) (id string, none bool, err error) {
	lower := strings.ToLower(v.Text)
	if lower == "me" {
		return actorID, false, nil
	}
	if lower == "none" {
		return "", true, nil
	}
	got, err := resolvePQLIDs(ctx, pool,
		`SELECT u.id::text FROM users u JOIN workspace_members m ON m.user_id = u.id
		 WHERE m.workspace_id = $1::uuid AND (lower(u.name) = lower($2) OR lower(u.email) = lower($2))`,
		wsID, v.Text, v.Pos, "assignee", "workspace")
	if err != nil {
		return "", false, err
	}
	return got[0], false, nil
}

func translatePQLAssignee(ctx context.Context, pool *pgxpool.Pool, wsID, actorID string, n *pqlNode, arg func(any) string) (string, error) {
	// Same EXISTS shape as issueListConds' assignee filter.
	existsFor := func(ids []string) string {
		if len(ids) == 1 {
			return "EXISTS (SELECT 1 FROM issue_assignees ia WHERE ia.issue_id = i.id AND ia.user_id = " + arg(ids[0]) + "::uuid)"
		}
		return "EXISTS (SELECT 1 FROM issue_assignees ia WHERE ia.issue_id = i.id AND ia.user_id = ANY(" + arg(ids) + "::uuid[]))"
	}
	notExists := "NOT EXISTS (SELECT 1 FROM issue_assignees ia WHERE ia.issue_id = i.id)"
	existsAny := "EXISTS (SELECT 1 FROM issue_assignees ia WHERE ia.issue_id = i.id)"

	var ids []string
	wantNone := false
	for _, v := range n.Values {
		id, none, err := resolvePQLAssigneeValue(ctx, pool, wsID, actorID, v)
		if err != nil {
			return "", err
		}
		if none {
			wantNone = true
		} else {
			ids = append(ids, id)
		}
	}
	switch n.Op {
	case "=":
		// Single value: either a member, or none.
		if wantNone {
			return notExists, nil
		}
		return existsFor(ids), nil
	case "!=":
		// Single value: not that member, or "has any assignee".
		if wantNone {
			return existsAny, nil
		}
		return "NOT EXISTS (SELECT 1 FROM issue_assignees ia WHERE ia.issue_id = i.id AND ia.user_id = " + arg(ids[0]) + "::uuid)", nil
	default: // in: member matches OR no-assignee, when both are present
		var parts []string
		if len(ids) > 0 {
			parts = append(parts, existsFor(ids))
		}
		if wantNone {
			parts = append(parts, notExists)
		}
		return "(" + strings.Join(parts, " OR ") + ")", nil
	}
}

// translatePQLEpic resolves epic names project-scoped (epics carry a
// UNIQUE(project_id, name) constraint, so an ambiguous match can only
// arise from case-variant names, e.g. "Platform" vs "PLATFORM", which
// the case-insensitive lookup then rejects as ambiguous — same shape
// as state). The `none` sentinel matches issues in no epic, mirroring
// the C75 visual filter's NOT EXISTS on the epic_issues junction.
// != against a named epic uses NOT EXISTS, so issues in other epics
// and issues in no epic both count as "not this epic", like label !=.
func translatePQLEpic(ctx context.Context, pool *pgxpool.Pool, projectID string, n *pqlNode, arg func(any) string) (string, error) {
	// Same EXISTS shape as issueListConds' epic filter.
	existsFor := func(ids []string) string {
		if len(ids) == 1 {
			return "EXISTS (SELECT 1 FROM epic_issues ei WHERE ei.issue_id = i.id AND ei.epic_id = " + arg(ids[0]) + "::uuid)"
		}
		return "EXISTS (SELECT 1 FROM epic_issues ei WHERE ei.issue_id = i.id AND ei.epic_id = ANY(" + arg(ids) + "::uuid[]))"
	}
	notExists := "NOT EXISTS (SELECT 1 FROM epic_issues ei WHERE ei.issue_id = i.id)"
	existsAny := "EXISTS (SELECT 1 FROM epic_issues ei WHERE ei.issue_id = i.id)"

	var ids []string
	wantNone := false
	for _, v := range n.Values {
		if strings.EqualFold(v.Text, "none") {
			wantNone = true
			continue
		}
		got, err := resolvePQLIDs(ctx, pool,
			`SELECT id::text FROM epics WHERE project_id = $1::uuid AND lower(name) = lower($2)`,
			projectID, v.Text, v.Pos, "epic", "project")
		if err != nil {
			return "", err
		}
		ids = append(ids, got[0])
	}
	switch n.Op {
	case "=":
		// Single value: either a named epic, or none.
		if wantNone {
			return notExists, nil
		}
		return existsFor(ids), nil
	case "!=":
		// Single value: not that epic, or "in any epic".
		if wantNone {
			return existsAny, nil
		}
		return "NOT EXISTS (SELECT 1 FROM epic_issues ei WHERE ei.issue_id = i.id AND ei.epic_id = " + arg(ids[0]) + "::uuid)", nil
	default: // in: epic matches OR no-epic, when both are present
		var parts []string
		if len(ids) > 0 {
			parts = append(parts, existsFor(ids))
		}
		if wantNone {
			parts = append(parts, notExists)
		}
		return "(" + strings.Join(parts, " OR ") + ")", nil
	}
}

// translatePQLEstimate resolves estimate-point keys within the
// project's estimate system: points carry key + value under an
// estimate_id, and scales are per-project, so the lookup joins
// estimate_points to estimates and confines to the project. Keys are
// UNIQUE per scale but not per project, so the same key on two scales
// in one project is an ambiguous-name 400 (via resolvePQLIDs), never a
// silent pick. The `none` sentinel matches issues with no estimate
// point (estimate_point_id IS NULL), mirroring the C75 visual filter.
// != against a named point uses IS DISTINCT FROM, so unestimated
// (NULL) issues count as "not this point" — the same inclusive-!=
// convention as label != / assignee !=, which include the empty case
// (a plain <> would silently drop NULL rows).
func translatePQLEstimate(ctx context.Context, pool *pgxpool.Pool, projectID string, n *pqlNode, arg func(any) string) (string, error) {
	var ids []string
	wantNone := false
	for _, v := range n.Values {
		if strings.EqualFold(v.Text, "none") {
			wantNone = true
			continue
		}
		got, err := resolvePQLIDs(ctx, pool,
			`SELECT ep.id::text FROM estimate_points ep JOIN estimates e ON e.id = ep.estimate_id
			 WHERE e.project_id = $1::uuid AND lower(ep.key) = lower($2)`,
			projectID, v.Text, v.Pos, "estimate point", "project")
		if err != nil {
			return "", err
		}
		ids = append(ids, got[0])
	}
	switch n.Op {
	case "=":
		// Single value: either a named point, or none.
		if wantNone {
			return "i.estimate_point_id IS NULL", nil
		}
		return "i.estimate_point_id = " + arg(ids[0]) + "::uuid", nil
	case "!=":
		// Single value: not that point (NULL counts as not it), or
		// "has any estimate point".
		if wantNone {
			return "i.estimate_point_id IS NOT NULL", nil
		}
		return "i.estimate_point_id IS DISTINCT FROM " + arg(ids[0]) + "::uuid", nil
	default: // in: point matches OR no-point, when both are present
		var parts []string
		if len(ids) > 0 {
			parts = append(parts, "i.estimate_point_id = ANY("+arg(ids)+"::uuid[])")
		}
		if wantNone {
			parts = append(parts, "i.estimate_point_id IS NULL")
		}
		return "(" + strings.Join(parts, " OR ") + ")", nil
	}
}

// resolvePQLParentKey maps one parent value token to the parent issue
// id. The key is the computed display id {IDENTIFIER}-{sequence_id}
// (e.g. ENG-123, never stored — same derivation as Issue.DisplayID
// and the activity.go identifier + sequence_id shape). Resolution is
// confined to the queried project by project_id AND by the project's
// own identifier, so a key from another project resolves to nothing:
// a 400 "unknown parent", never a cross-project leak and never a
// silent empty list. A deleted parent is unknown too; an archived
// parent still resolves (archived issues are real issues, only
// hidden from the default list). A malformed key (no dash, bad
// identifier, non-numeric sequence) is a 400, not a lookup miss.
func resolvePQLParentKey(ctx context.Context, pool *pgxpool.Pool, projectID string, v pqlValue) (string, error) {
	identPart, seq, err := parsePQLParentKeyParts(v)
	if err != nil {
		return "", err
	}
	var id string
	err = pool.QueryRow(ctx,
		`SELECT i.id::text FROM issues i JOIN projects p ON p.id = i.project_id
		  WHERE i.project_id = $1::uuid AND lower(p.identifier) = lower($2)
		    AND i.sequence_id = $3 AND i.deleted_at IS NULL`,
		projectID, identPart, seq).Scan(&id)
	if err != nil {
		if err == pgx.ErrNoRows {
			return "", pqlErr(v.Pos, "unknown parent %q in this project", v.Text)
		}
		return "", err
	}
	return id, nil
}

// translatePQLParent resolves parent keys project-scoped (see
// resolvePQLParentKey). The `none` sentinel matches top-level issues
// (parent_id IS NULL), mirroring the C75 visual filter. != against a
// named parent uses IS DISTINCT FROM, so top-level (NULL) issues
// count as "not this parent" — the same inclusive-!= convention as
// estimate != (a plain <> would silently drop NULL rows).
func translatePQLParent(ctx context.Context, pool *pgxpool.Pool, projectID string, n *pqlNode, arg func(any) string) (string, error) {
	var ids []string
	wantNone := false
	for _, v := range n.Values {
		if strings.EqualFold(v.Text, "none") {
			wantNone = true
			continue
		}
		id, err := resolvePQLParentKey(ctx, pool, projectID, v)
		if err != nil {
			return "", err
		}
		ids = append(ids, id)
	}
	switch n.Op {
	case "=":
		// Single value: either a named parent, or none.
		if wantNone {
			return "i.parent_id IS NULL", nil
		}
		return "i.parent_id = " + arg(ids[0]) + "::uuid", nil
	case "!=":
		// Single value: not that parent (NULL counts as not it), or
		// "has any parent".
		if wantNone {
			return "i.parent_id IS NOT NULL", nil
		}
		return "i.parent_id IS DISTINCT FROM " + arg(ids[0]) + "::uuid", nil
	default: // in: parent matches OR top-level, when both are present
		var parts []string
		if len(ids) > 0 {
			parts = append(parts, "i.parent_id = ANY("+arg(ids)+"::uuid[])")
		}
		if wantNone {
			parts = append(parts, "i.parent_id IS NULL")
		}
		return "(" + strings.Join(parts, " OR ") + ")", nil
	}
}

// ---------------------------------------------------------------------------
// Workspace-scope translators (C112T0; projectID == "")
// ---------------------------------------------------------------------------

// translatePQLEpicWorkspace resolves epic names to the set of epic ids
// across accessible projects. Same EXISTS shape as project scope, with
// ANY over the union set; != is NOT EXISTS over the set, so issues in
// other epics and issues in no epic both count as "not this epic".
// The `none` handling is unchanged from project scope.
func translatePQLEpicWorkspace(ctx context.Context, pool *pgxpool.Pool, wsID, actorID string, n *pqlNode, arg func(any) string) (string, error) {
	existsFor := func(ids []string) string {
		return "EXISTS (SELECT 1 FROM epic_issues ei WHERE ei.issue_id = i.id AND ei.epic_id = ANY(" + arg(ids) + "::uuid[]))"
	}
	notExists := "NOT EXISTS (SELECT 1 FROM epic_issues ei WHERE ei.issue_id = i.id)"
	existsAny := "EXISTS (SELECT 1 FROM epic_issues ei WHERE ei.issue_id = i.id)"

	var ids []string
	wantNone := false
	for _, v := range n.Values {
		if strings.EqualFold(v.Text, "none") {
			wantNone = true
			continue
		}
		got, err := resolvePQLWorkspaceIDs(ctx, pool,
			`SELECT e.id::text FROM epics e JOIN projects p ON p.id = e.project_id
			  WHERE p.workspace_id = $1::uuid AND lower(e.name) = lower($2)
			    AND `+projectAccessibleSQL("p", "$3::uuid"),
			wsID, v.Text, actorID, v.Pos, "epic")
		if err != nil {
			return "", err
		}
		ids = append(ids, got...)
	}
	switch n.Op {
	case "=":
		if wantNone {
			return notExists, nil
		}
		return existsFor(ids), nil
	case "!=":
		if wantNone {
			return existsAny, nil
		}
		return "NOT EXISTS (SELECT 1 FROM epic_issues ei WHERE ei.issue_id = i.id AND ei.epic_id = ANY(" + arg(ids) + "::uuid[]))", nil
	default: // in: epic matches OR no-epic, when both are present
		var parts []string
		if len(ids) > 0 {
			parts = append(parts, existsFor(ids))
		}
		if wantNone {
			parts = append(parts, notExists)
		}
		return "(" + strings.Join(parts, " OR ") + ")", nil
	}
}

// translatePQLEstimateWorkspace resolves estimate-point keys to the
// set of point ids across accessible projects (the key lookup joins
// through estimates to projects, same as project scope). The `none`
// handling is unchanged; != keeps the inclusive-NULL convention via
// an explicit IS NULL OR NOT (... = ANY(...)) — a bare NOT over ANY
// would drop unestimated rows, unlike project scope's IS DISTINCT
// FROM on a single id.
func translatePQLEstimateWorkspace(ctx context.Context, pool *pgxpool.Pool, wsID, actorID string, n *pqlNode, arg func(any) string) (string, error) {
	var ids []string
	wantNone := false
	for _, v := range n.Values {
		if strings.EqualFold(v.Text, "none") {
			wantNone = true
			continue
		}
		got, err := resolvePQLWorkspaceIDs(ctx, pool,
			`SELECT ep.id::text FROM estimate_points ep JOIN estimates e ON e.id = ep.estimate_id
			  JOIN projects p ON p.id = e.project_id
			  WHERE p.workspace_id = $1::uuid AND lower(ep.key) = lower($2)
			    AND `+projectAccessibleSQL("p", "$3::uuid"),
			wsID, v.Text, actorID, v.Pos, "estimate point")
		if err != nil {
			return "", err
		}
		ids = append(ids, got...)
	}
	switch n.Op {
	case "=":
		if wantNone {
			return "i.estimate_point_id IS NULL", nil
		}
		return "i.estimate_point_id = ANY(" + arg(ids) + "::uuid[])", nil
	case "!=":
		if wantNone {
			return "i.estimate_point_id IS NOT NULL", nil
		}
		return "(i.estimate_point_id IS NULL OR NOT (i.estimate_point_id = ANY(" + arg(ids) + "::uuid[])))", nil
	default: // in: point matches OR no-point, when both are present
		var parts []string
		if len(ids) > 0 {
			parts = append(parts, "i.estimate_point_id = ANY("+arg(ids)+"::uuid[])")
		}
		if wantNone {
			parts = append(parts, "i.estimate_point_id IS NULL")
		}
		return "(" + strings.Join(parts, " OR ") + ")", nil
	}
}

// parsePQLParentKeyParts validates one parent key token and splits it
// into its identifier and sequence parts. Shared by the project and
// workspace resolvers so malformed-key 400s stay identical in both
// scopes.
func parsePQLParentKeyParts(v pqlValue) (identPart string, seq int, err error) {
	dash := strings.LastIndex(v.Text, "-")
	if dash <= 0 || dash == len(v.Text)-1 {
		return "", 0, pqlErr(v.Pos, "invalid parent key %q (want {IDENTIFIER}-{sequence_id}, e.g. ENG-123)", v.Text)
	}
	identPart, seqPart := v.Text[:dash], v.Text[dash+1:]
	if len(identPart) > 12 {
		return "", 0, pqlErr(v.Pos, "invalid parent key %q (want {IDENTIFIER}-{sequence_id}, e.g. ENG-123)", v.Text)
	}
	for i := 0; i < len(identPart); i++ {
		c := identPart[i]
		if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9') {
			return "", 0, pqlErr(v.Pos, "invalid parent key %q (want {IDENTIFIER}-{sequence_id}, e.g. ENG-123)", v.Text)
		}
	}
	seq, err = atoiStrict(seqPart)
	if err != nil || seq <= 0 {
		return "", 0, pqlErr(v.Pos, "invalid parent key %q (want {IDENTIFIER}-{sequence_id}, e.g. ENG-123)", v.Text)
	}
	return identPart, seq, nil
}

// resolvePQLParentKeyWorkspace maps one parent value token to the
// parent issue id across the workspace: the project is looked up by
// identifier (workspace-unique, case-insensitive), then the sequence
// inside it, confined to accessible projects — a key naming an
// inaccessible project is unknown (400), never a leak. A deleted
// parent is unknown too; an archived parent still resolves, exactly
// as in project scope.
func resolvePQLParentKeyWorkspace(ctx context.Context, pool *pgxpool.Pool, wsID, actorID string, v pqlValue) (string, error) {
	identPart, seq, err := parsePQLParentKeyParts(v)
	if err != nil {
		return "", err
	}
	var id string
	err = pool.QueryRow(ctx,
		`SELECT i.id::text FROM issues i JOIN projects p ON p.id = i.project_id
		  WHERE p.workspace_id = $1::uuid AND lower(p.identifier) = lower($2)
		    AND i.sequence_id = $3 AND i.deleted_at IS NULL
		    AND `+projectAccessibleSQL("p", "$4::uuid"),
		wsID, identPart, seq, actorID).Scan(&id)
	if err != nil {
		if err == pgx.ErrNoRows {
			return "", pqlErr(v.Pos, "unknown parent %q in this workspace", v.Text)
		}
		return "", err
	}
	return id, nil
}

// translatePQLParentWorkspace resolves parent keys across the
// workspace (see resolvePQLParentKeyWorkspace). The `none` handling
// and the inclusive-!= convention (NULL counts as "not this parent")
// are unchanged from project scope; only single-id comparisons become
// set comparisons over the resolved keys.
func translatePQLParentWorkspace(ctx context.Context, pool *pgxpool.Pool, wsID, actorID string, n *pqlNode, arg func(any) string) (string, error) {
	var ids []string
	wantNone := false
	for _, v := range n.Values {
		if strings.EqualFold(v.Text, "none") {
			wantNone = true
			continue
		}
		id, err := resolvePQLParentKeyWorkspace(ctx, pool, wsID, actorID, v)
		if err != nil {
			return "", err
		}
		ids = append(ids, id)
	}
	switch n.Op {
	case "=":
		if wantNone {
			return "i.parent_id IS NULL", nil
		}
		return "i.parent_id = ANY(" + arg(ids) + "::uuid[])", nil
	case "!=":
		if wantNone {
			return "i.parent_id IS NOT NULL", nil
		}
		return "(i.parent_id IS NULL OR NOT (i.parent_id = ANY(" + arg(ids) + "::uuid[])))", nil
	default: // in: parent matches OR top-level, when both are present
		var parts []string
		if len(ids) > 0 {
			parts = append(parts, "i.parent_id = ANY("+arg(ids)+"::uuid[])")
		}
		if wantNone {
			parts = append(parts, "i.parent_id IS NULL")
		}
		return "(" + strings.Join(parts, " OR ") + ")", nil
	}
}
