package service

// PQL date-window unit test (C109T0): withoutVisualFilters preserves
// the date-window containers (DueAfter, DueBefore, StartAfter,
// StartBefore, Undated) exactly like the Cycle/Module/Epics scope
// containers, while still zeroing the other visual filter dimensions
// — including DueDateEmpty, which is a filter dimension, not a window.

import (
	"testing"
	"time"
)

func TestWithoutVisualFiltersPreservesDateWindow(t *testing.T) {
	dueAfter := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	dueBefore := time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)
	startAfter := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	startBefore := time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)
	createdAfter := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	in := ListIssuesInput{
		PQL:          "priority >= 2",
		Cycle:        "11111111-1111-1111-1111-111111111111",
		State:        "22222222-2222-2222-2222-222222222222",
		CreatedAfter: &createdAfter,
		DueAfter:     &dueAfter,
		DueBefore:    &dueBefore,
		StartAfter:   &startAfter,
		StartBefore:  &startBefore,
		Undated:      true,
		DueDateEmpty: true,
		Subscribed:   true,
	}
	got := in.withoutVisualFilters()
	if got.DueAfter == nil || !got.DueAfter.Equal(dueAfter) {
		t.Fatalf("DueAfter = %v, want %v", got.DueAfter, dueAfter)
	}
	if got.DueBefore == nil || !got.DueBefore.Equal(dueBefore) {
		t.Fatalf("DueBefore = %v, want %v", got.DueBefore, dueBefore)
	}
	if got.StartAfter == nil || !got.StartAfter.Equal(startAfter) {
		t.Fatalf("StartAfter = %v, want %v", got.StartAfter, startAfter)
	}
	if got.StartBefore == nil || !got.StartBefore.Equal(startBefore) {
		t.Fatalf("StartBefore = %v, want %v", got.StartBefore, startBefore)
	}
	if !got.Undated {
		t.Fatalf("Undated = false, want true")
	}
	if got.Cycle == "" {
		t.Fatalf("Cycle zeroed, want the scope container preserved")
	}
	// Still zeroed: the other visual dimensions, including the
	// due_date_empty filter dimension (not a window).
	if got.State != "" || got.CreatedAfter != nil || got.DueDateEmpty || got.Subscribed {
		t.Fatalf("visual dimensions survived: State=%q CreatedAfter=%v DueDateEmpty=%v Subscribed=%v",
			got.State, got.CreatedAfter, got.DueDateEmpty, got.Subscribed)
	}
}
