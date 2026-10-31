package service

// PQL epic + estimate service tests (C97T0): translated queries
// against the real test DB return exactly the right issues, including
// the `none` sentinels, `in (...)` lists, case-insensitive name/key
// matching, unknown-name 400s, and ambiguous-key 400s. The C95
// scope-survives contract is re-checked here for the new fields: a
// cycle scope ANDs with an epic/estimate predicate.

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// pqlEpicEstimateFixture builds: epics Platform + Mobile, a Fibonacci
// scale with points S/M, and five issues covering every combination
// of epic membership x estimate presence.
func pqlEpicEstimateFixture(t *testing.T) (listTestSetup, map[string]*Issue) {
	t.Helper()
	s := setupListTest(t)
	ctx := context.Background()

	platform, err := CreateEpic(ctx, s.pool, s.slug, s.ident, s.actor, EpicInput{Name: "Platform"})
	if err != nil {
		t.Fatalf("CreateEpic Platform: %v", err)
	}
	mobile, err := CreateEpic(ctx, s.pool, s.slug, s.ident, s.actor, EpicInput{Name: "Mobile"})
	if err != nil {
		t.Fatalf("CreateEpic Mobile: %v", err)
	}
	est, err := CreateEstimate(ctx, s.pool, s.slug, s.ident, s.actor, EstimateInput{
		Name: "Fibonacci",
		Points: []EstimatePointInput{
			{Key: "S", Value: 1},
			{Key: "M", Value: 3},
		},
	})
	if err != nil {
		t.Fatalf("CreateEstimate: %v", err)
	}
	pointByKey := map[string]string{}
	for _, pt := range est.Points {
		pointByKey[pt.Key] = pt.ID
	}
	withEstimate := func(key string) *string {
		id := pointByKey[key]
		return &id
	}

	a := pqlCreateIssue(t, s.pool, s.slug, s.ident, s.actor, "A", CreateIssueInput{EstimatePointID: withEstimate("S")})
	b := pqlCreateIssue(t, s.pool, s.slug, s.ident, s.actor, "B", CreateIssueInput{})
	c := pqlCreateIssue(t, s.pool, s.slug, s.ident, s.actor, "C", CreateIssueInput{EstimatePointID: withEstimate("M")})
	d := pqlCreateIssue(t, s.pool, s.slug, s.ident, s.actor, "D", CreateIssueInput{})
	e := pqlCreateIssue(t, s.pool, s.slug, s.ident, s.actor, "E", CreateIssueInput{EstimatePointID: withEstimate("M")})
	if err := AddEpicIssues(ctx, s.pool, s.slug, s.ident, s.actor, platform.ID, []string{a.ID, b.ID}); err != nil {
		t.Fatalf("AddEpicIssues Platform: %v", err)
	}
	if err := AddEpicIssues(ctx, s.pool, s.slug, s.ident, s.actor, mobile.ID, []string{c.ID}); err != nil {
		t.Fatalf("AddEpicIssues Mobile: %v", err)
	}
	return s, map[string]*Issue{"A": a, "B": b, "C": c, "D": d, "E": e}
}

func TestPQLServiceEpic(t *testing.T) {
	s, _ := pqlEpicEstimateFixture(t)

	pqlWantNames(t, pqlListNames(t, s, `epic = Platform`), "A", "B")
	pqlWantNames(t, pqlListNames(t, s, `epic = platform`), "A", "B") // case-insensitive name match
	pqlWantNames(t, pqlListNames(t, s, `epic = "Mobile"`), "C")
	pqlWantNames(t, pqlListNames(t, s, `epic = none`), "D", "E")
	pqlWantNames(t, pqlListNames(t, s, `epic != Platform`), "C", "D", "E")
	pqlWantNames(t, pqlListNames(t, s, `epic != none`), "A", "B", "C")
	pqlWantNames(t, pqlListNames(t, s, `epic in (Platform, Mobile)`), "A", "B", "C")
	pqlWantNames(t, pqlListNames(t, s, `epic in (Platform, none)`), "A", "B", "D", "E")
	pqlWantNames(t, pqlListNames(t, s, `epic in (none)`), "D", "E")
	pqlWantNames(t, pqlListNames(t, s, `epic = Platform AND estimate = none`), "B")
	pqlWantNames(t, pqlListNames(t, s, `NOT epic = Platform`), "C", "D", "E")
}

func TestPQLServiceEstimate(t *testing.T) {
	s, _ := pqlEpicEstimateFixture(t)

	pqlWantNames(t, pqlListNames(t, s, `estimate = S`), "A")
	pqlWantNames(t, pqlListNames(t, s, `estimate = s`), "A") // case-insensitive key match
	pqlWantNames(t, pqlListNames(t, s, `estimate = M`), "C", "E")
	pqlWantNames(t, pqlListNames(t, s, `estimate = none`), "B", "D")
	// != against a named point includes unestimated (NULL) issues —
	// the inclusive-!= convention shared with label != / assignee !=.
	pqlWantNames(t, pqlListNames(t, s, `estimate != M`), "A", "B", "D")
	pqlWantNames(t, pqlListNames(t, s, `estimate != none`), "A", "C", "E")
	pqlWantNames(t, pqlListNames(t, s, `estimate in (S, M)`), "A", "C", "E")
	pqlWantNames(t, pqlListNames(t, s, `estimate in (M, none)`), "B", "C", "D", "E")
	pqlWantNames(t, pqlListNames(t, s, `estimate in (none)`), "B", "D")
	pqlWantNames(t, pqlListNames(t, s, `estimate = M AND epic = none`), "E")
}

func TestPQLServiceEpicEstimateScopeSurvives(t *testing.T) {
	s, iss := pqlEpicEstimateFixture(t)
	ctx := context.Background()

	now := time.Now()
	cycle, err := CreateCycle(ctx, s.pool, s.slug, s.ident, s.actor, CycleInput{
		Name: "Sprint C97", StartDate: now.AddDate(0, 0, -1), EndDate: now.AddDate(0, 0, 13),
	})
	if err != nil {
		t.Fatalf("CreateCycle: %v", err)
	}
	// A is in epic Platform; put only A (not B) in the cycle.
	if err := AddCycleIssues(ctx, s.pool, s.slug, s.ident, s.actor, cycle.ID, []string{iss["A"].ID}); err != nil {
		t.Fatalf("AddCycleIssues: %v", err)
	}

	// Epic predicate AND cycle scope: only A (in epic AND in cycle).
	pqlWantNames(t, pqlScopeListNames(t, s, ListIssuesInput{PQL: `epic = Platform`, Cycle: cycle.ID}), "A")
	// Estimate predicate AND cycle scope: A has estimate S and is in
	// the cycle; the estimate=none predicate matches nothing in-cycle.
	pqlWantNames(t, pqlScopeListNames(t, s, ListIssuesInput{PQL: `estimate = S`, Cycle: cycle.ID}), "A")
	pqlWantNames(t, pqlScopeListNames(t, s, ListIssuesInput{PQL: `estimate = none`, Cycle: cycle.ID}))
}

func TestPQLServiceEpicEstimateUnknownAndAmbiguous(t *testing.T) {
	s, _ := pqlEpicEstimateFixture(t)
	ctx := context.Background()

	for _, tc := range []struct {
		query string
		want  string
	}{
		{`epic = NoSuchEpic`, `unknown epic "NoSuchEpic"`},
		{`estimate = XXL`, `unknown estimate point "XXL"`},
		{`epic in (Platform, NoSuchEpic)`, `unknown epic "NoSuchEpic"`},
		{`estimate in (S, XXL)`, `unknown estimate point "XXL"`},
	} {
		_, err := ListIssues(ctx, s.pool, s.slug, s.ident, s.actor,
			ListIssuesInput{PQL: tc.query, PerPage: 10})
		if err == nil {
			t.Errorf("PQL %q: want error, got nil", tc.query)
			continue
		}
		var pqlErr *PQLQueryError
		if !errors.As(err, &pqlErr) {
			t.Errorf("PQL %q: error type = %T, want *PQLQueryError", tc.query, err)
			continue
		}
		if !strings.Contains(pqlErr.Error(), tc.want) {
			t.Errorf("PQL %q: error = %v, want substring %q", tc.query, pqlErr, tc.want)
		}
	}

	// Ambiguous estimate key: a second scale in the same project with
	// the same key S makes `estimate = S` ambiguous, never a pick.
	if _, err := CreateEstimate(ctx, s.pool, s.slug, s.ident, s.actor, EstimateInput{
		Name:   "TShirt",
		Points: []EstimatePointInput{{Key: "S", Value: 2}},
	}); err != nil {
		t.Fatalf("CreateEstimate second scale: %v", err)
	}
	_, err := ListIssues(ctx, s.pool, s.slug, s.ident, s.actor,
		ListIssuesInput{PQL: `estimate = S`, PerPage: 10})
	if err == nil {
		t.Fatalf("estimate = S after second scale: want ambiguous error, got nil")
	}
	var pqlErr *PQLQueryError
	if !errors.As(err, &pqlErr) || !strings.Contains(pqlErr.Error(), "ambiguous estimate point") {
		t.Fatalf("estimate = S after second scale: error = %v, want ambiguous estimate point", err)
	}

	// Ambiguous epic name: the UNIQUE(project_id, name) constraint is
	// case-sensitive, so "PLATFORM" coexists with "Platform" — and the
	// case-insensitive PQL lookup must then refuse to pick one.
	if _, err := CreateEpic(ctx, s.pool, s.slug, s.ident, s.actor, EpicInput{Name: "PLATFORM"}); err != nil {
		t.Fatalf("CreateEpic PLATFORM: %v", err)
	}
	_, err = ListIssues(ctx, s.pool, s.slug, s.ident, s.actor,
		ListIssuesInput{PQL: `epic = Platform`, PerPage: 10})
	if err == nil {
		t.Fatalf("epic = Platform after case-variant: want ambiguous error, got nil")
	}
	if !errors.As(err, &pqlErr) || !strings.Contains(pqlErr.Error(), "ambiguous epic") {
		t.Fatalf("epic = Platform after case-variant: error = %v, want ambiguous epic", err)
	}
}
