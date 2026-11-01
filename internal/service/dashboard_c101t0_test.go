package service

// C101T0: multi-project pql_table widget config validation — the
// C100 exactly-one pin widened to 1..N. Pure config parsing (no
// database): 2 and 3 projects accepted, the filters-key rejection
// unchanged, group_by/metric still accepted-but-ignored, ORDER BY +
// LIMIT still pass. (C102T0: empty/missing project_ids are no longer
// 400 — empty-means-all, see dashboard_c102t0_test.go.)

import (
	"encoding/json"
	"errors"
	"testing"
)

const c101ProjC = "33333333-3333-3333-3333-333333333333"

func TestC101PQLMultiProjectAccepted(t *testing.T) {
	cfg := c100PQLConfig(t, `{"query":"priority >= 0","project_ids":["`+c100ProjA+`","`+c100ProjB+`"]}`)
	if len(cfg.projectIDs) != 2 || cfg.projectIDs[0] != c100ProjA || cfg.projectIDs[1] != c100ProjB {
		t.Fatalf("projectIDs = %v, want [A B] in config order", cfg.projectIDs)
	}
	cfg = c100PQLConfig(t, `{"query":"priority >= 0","project_ids":["`+c100ProjA+`","`+c100ProjB+`","`+c101ProjC+`"]}`)
	if len(cfg.projectIDs) != 3 {
		t.Fatalf("projectIDs = %v, want 3 projects", cfg.projectIDs)
	}
}

func TestC101PQLMultiProjectRejects(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		// C102T0: empty/missing project_ids are now ACCEPTED
		// (empty-means-all — see dashboard_c102t0_test.go).
		{"filters key with 2 projects", `{"query":"priority >= 0","project_ids":["` + c100ProjA + `","` + c100ProjB + `"],"filters":{"states":["` + c100ProjA + `"]}}`},
		{"empty filters key with 2 projects", `{"query":"priority >= 0","project_ids":["` + c100ProjA + `","` + c100ProjB + `"],"filters":{}}`},
		{"blank query with 2 projects", `{"query":"   ","project_ids":["` + c100ProjA + `","` + c100ProjB + `"]}`},
		{"bad syntax with 2 projects", `{"query":"priority >=","project_ids":["` + c100ProjA + `","` + c100ProjB + `"]}`},
		{"malformed project UUID with 2 projects", `{"query":"priority >= 0","project_ids":["` + c100ProjA + `","not-a-uuid"]}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := parseWidgetConfig(WidgetTypePQLTable, json.RawMessage(tc.raw)); !errors.Is(err, ErrInvalidWidgetConfig) {
				t.Fatalf("parse %s: err = %v, want ErrInvalidWidgetConfig", tc.raw, err)
			}
		})
	}
}

func TestC101PQLMultiProjectGroupByMetricIgnored(t *testing.T) {
	// Row-list precedent, unchanged by the widening: group_by/metric
	// accepted-but-ignored on a multi-project widget too.
	cfg := c100PQLConfig(t, `{"query":"priority >= 0","project_ids":["`+c100ProjA+`","`+c100ProjB+`"],"group_by":"state","metric":"estimate_points"}`)
	if cfg.groupBy != "" {
		t.Fatalf("groupBy = %q, want ignored (empty)", cfg.groupBy)
	}
	if cfg.metric != WidgetMetricCount {
		t.Fatalf("metric = %q, want count (ignored)", cfg.metric)
	}
}

func TestC101PQLMultiProjectOrderByAndLimitPass(t *testing.T) {
	// ORDER BY (multi-key) + LIMIT survive write-time validation on a
	// multi-project widget — they are the union's global sort and
	// total cap at compute time.
	cfg := c100PQLConfig(t, `{"query":"priority >= 0 ORDER BY priority DESC, created_at ASC LIMIT 5","project_ids":["`+c100ProjA+`","`+c100ProjB+`"],"max_rows":2}`)
	if cfg.maxRows != 2 {
		t.Fatalf("maxRows = %d, want 2", cfg.maxRows)
	}
	if len(cfg.projectIDs) != 2 {
		t.Fatalf("projectIDs = %v, want 2 projects", cfg.projectIDs)
	}
}
