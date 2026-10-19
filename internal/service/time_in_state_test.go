package service

// Time-in-state computation (C70T0): unit tests for the pure helpers.
// The helpers take an explicit `now` (frozen clock) so durations are
// byte-exact; the DB-backed read paths are covered by the handler pins
// in internal/api/openapi_test.go.

import (
	"testing"
	"time"
)

var c70Now = time.Date(2026, 10, 19, 9, 0, 0, 0, time.UTC)

func c70At(hour, min int) time.Time {
	return time.Date(2026, 10, 19, hour, min, 0, 0, time.UTC)
}

// No state_id activity rows → the issue's created_at is the fallback
// (an UPPER BOUND on the true time in state for pre-history issues).
func TestStateEnteredAt_NoRowsFallsBackToCreatedAt(t *testing.T) {
	created := c70At(6, 0)
	if got := StateEnteredAt(nil, created); !got.Equal(created) {
		t.Fatalf("StateEnteredAt(nil) = %v, want %v", got, created)
	}
	if got := StateEnteredAt([]StateTransition{}, created); !got.Equal(created) {
		t.Fatalf("StateEnteredAt([]) = %v, want %v", got, created)
	}
}

// One transition → entered at that row's created_at.
func TestStateEnteredAt_SingleTransition(t *testing.T) {
	created := c70At(6, 0)
	entered := c70At(7, 30)
	got := StateEnteredAt([]StateTransition{{At: entered}}, created)
	if !got.Equal(entered) {
		t.Fatalf("StateEnteredAt = %v, want %v", got, entered)
	}
}

// Several transitions → entered at the LATEST row's created_at.
func TestStateEnteredAt_UsesLatestTransition(t *testing.T) {
	created := c70At(6, 0)
	tr := []StateTransition{{At: c70At(7, 0)}, {At: c70At(7, 30)}, {At: c70At(8, 15)}}
	got := StateEnteredAt(tr, created)
	if !got.Equal(tr[2].At) {
		t.Fatalf("StateEnteredAt = %v, want %v", got, tr[2].At)
	}
}

// time_in_state = now − entered_at, second-exact on a frozen clock.
func TestTimeInStateSeconds_Exact(t *testing.T) {
	created := c70At(6, 0)
	tr := []StateTransition{{At: c70At(8, 15)}}
	// 08:15 → 09:00 = 45 minutes = 2700 seconds.
	if got := TimeInStateSeconds(tr, created, c70Now); got != 2700 {
		t.Fatalf("TimeInStateSeconds = %d, want 2700", got)
	}
}

// Fallback case: no rows → now − created_at.
func TestTimeInStateSeconds_FallbackCase(t *testing.T) {
	created := c70At(6, 0)
	// 06:00 → 09:00 = 3 hours = 10800 seconds.
	if got := TimeInStateSeconds(nil, created, c70Now); got != 10800 {
		t.Fatalf("TimeInStateSeconds(nil) = %d, want 10800", got)
	}
}

// A clock-skewed (future) activity row never yields a negative duration.
func TestTimeInStateSeconds_FutureRowClampedToZero(t *testing.T) {
	created := c70At(6, 0)
	tr := []StateTransition{{At: c70Now.Add(10 * time.Second)}}
	if got := TimeInStateSeconds(tr, created, c70Now); got != 0 {
		t.Fatalf("TimeInStateSeconds(future row) = %d, want 0", got)
	}
}

// Terminal states keep counting — there is no reset on done.
func TestTimeInStateSeconds_TerminalStateKeepsCounting(t *testing.T) {
	created := c70At(6, 0)
	tr := []StateTransition{{At: c70At(8, 0)}}
	if got := TimeInStateSeconds(tr, created, c70Now); got != 3600 {
		t.Fatalf("TimeInStateSeconds = %d, want 3600 (keeps counting)", got)
	}
}

// 3-transition fixture, byte-exact per-transition durations:
// transition i lasts until transition i+1's created_at; the latest
// lasts until now.
func TestTransitionDurations_ThreeTransitionsExact(t *testing.T) {
	tr := []StateTransition{{At: c70At(6, 0)}, {At: c70At(7, 0)}, {At: c70At(8, 15)}}
	got := TransitionDurations(tr, c70Now)
	// 06:00→07:00 = 3600, 07:00→08:15 = 4500, 08:15→09:00 = 2700.
	want := []int64{3600, 4500, 2700}
	if len(got) != len(want) {
		t.Fatalf("TransitionDurations = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("TransitionDurations[%d] = %d, want %d", i, got[i], want[i])
		}
	}
}

// Single transition → until now.
func TestTransitionDurations_SingleTransition(t *testing.T) {
	tr := []StateTransition{{At: c70At(8, 15)}}
	got := TransitionDurations(tr, c70Now)
	if len(got) != 1 || got[0] != 2700 {
		t.Fatalf("TransitionDurations = %v, want [2700]", got)
	}
}

// No transitions → no durations.
func TestTransitionDurations_Empty(t *testing.T) {
	if got := TransitionDurations(nil, c70Now); len(got) != 0 {
		t.Fatalf("TransitionDurations(nil) = %v, want []", got)
	}
}

// Out-of-order/skewed rows never yield negative durations.
func TestTransitionDurations_SkewClampedToZero(t *testing.T) {
	tr := []StateTransition{{At: c70At(8, 0)}, {At: c70At(7, 0)}}
	got := TransitionDurations(tr, c70Now)
	if len(got) != 2 || got[0] != 0 || got[1] != 7200 {
		t.Fatalf("TransitionDurations = %v, want [0 7200]", got)
	}
}
