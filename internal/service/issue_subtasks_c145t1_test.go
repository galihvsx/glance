package service

// Subtask-row dates (C145T1): SubtaskRow carries the child's own
// start_date / target_date from the issues table (no migration —
// the columns already exist). A child with both dates set returns
// both dates on the row; a dateless child returns nils — never an
// error, never a dropped row. The columns are DATE, so assertions
// compare the calendar day, not the time-of-day encoding.

import (
	"context"
	"testing"
	"time"
)

func TestListIncludeSubtasksDates(t *testing.T) {
	s := setupListTest(t)
	ctx := context.Background()

	parent := createTestIssue(t, s.pool, s.slug, s.ident, s.actor, "dates-parent")
	start := time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)
	target := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	prio := 2
	dated, err := CreateIssue(ctx, s.pool, s.slug, s.ident, s.actor,
		CreateIssueInput{Name: "dated-child", ParentID: &parent.ID, Priority: &prio,
			SortOrder: float64Ptr(1), StartDate: &start, TargetDate: &target})
	if err != nil {
		t.Fatalf("CreateIssue dated child: %v", err)
	}
	dateless, err := CreateIssue(ctx, s.pool, s.slug, s.ident, s.actor,
		CreateIssueInput{Name: "dateless-child", ParentID: &parent.ID, Priority: &prio,
			SortOrder: float64Ptr(2)})
	if err != nil {
		t.Fatalf("CreateIssue dateless child: %v", err)
	}

	r, err := ListIssues(ctx, s.pool, s.slug, s.ident, s.actor,
		ListIssuesInput{PerPage: 100, IncludeSubtasks: true})
	if err != nil {
		t.Fatalf("ListIssues flag on: %v", err)
	}
	var rows []SubtaskRow
	for _, it := range r.Issues {
		if it.ID == parent.ID {
			if it.Subtasks == nil {
				t.Fatal("parent row has nil Subtasks with flag on")
			}
			rows = *it.Subtasks
		}
	}
	if len(rows) != 2 {
		t.Fatalf("parent subtasks = %d rows, want 2 (dated + dateless, neither dropped)", len(rows))
	}
	byID := map[string]SubtaskRow{rows[0].ID: rows[0], rows[1].ID: rows[1]}

	d := byID[dated.ID]
	if d.StartDate == nil || d.StartDate.Format("2006-01-02") != "2026-11-10" {
		t.Fatalf("dated child start_date = %v, want 2026-11-10", d.StartDate)
	}
	if d.TargetDate == nil || d.TargetDate.Format("2006-01-02") != "2026-11-20" {
		t.Fatalf("dated child target_date = %v, want 2026-11-20", d.TargetDate)
	}

	n := byID[dateless.ID]
	if n.StartDate != nil || n.TargetDate != nil {
		t.Fatalf("dateless child dates = (%v, %v), want (nil, nil)", n.StartDate, n.TargetDate)
	}
}

func float64Ptr(f float64) *float64 { return &f }
