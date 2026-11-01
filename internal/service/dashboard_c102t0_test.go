package service

// C102T0: empty-means-all project scope for pql_table — the C101
// 1..N pin is gone. Pure config parsing (no database): absent or []
// project_ids is valid and normalizes to nil (the other row-list
// types' rule — widgetProjectScope resolves nil to every live,
// accessible workspace project at compute time); 1..N pinned
// projects parse exactly as before; the filters-key rejection, blank
// query 400, bad syntax 400, and group_by/metric
// accepted-but-ignored rules are unchanged.

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestC102PQLEmptyScopeAccepted(t *testing.T) {
	cfg := c100PQLConfig(t, `{"query":"priority >= 0","project_ids":[]}`)
	if cfg.query != "priority >= 0" {
		t.Fatalf("query = %q, want the stored query", cfg.query)
	}
	if cfg.projectIDs != nil {
		t.Fatalf("projectIDs = %v, want nil (empty normalizes to nil)", cfg.projectIDs)
	}
	if cfg.maxRows != 10 {
		t.Fatalf("maxRows = %d, want default 10", cfg.maxRows)
	}
	cfg = c100PQLConfig(t, `{"query":"priority >= 0"}`)
	if cfg.projectIDs != nil {
		t.Fatalf("projectIDs = %v, want nil (absent normalizes to nil)", cfg.projectIDs)
	}
}

func TestC102PQLPinnedScopeUnchanged(t *testing.T) {
	// 1..N pinned projects parse exactly as in C100/C101.
	cfg := c100PQLConfig(t, `{"query":"priority >= 0","project_ids":["`+c100ProjA+`"]}`)
	if len(cfg.projectIDs) != 1 || cfg.projectIDs[0] != c100ProjA {
		t.Fatalf("projectIDs = %v, want [%s]", cfg.projectIDs, c100ProjA)
	}
	cfg = c100PQLConfig(t, `{"query":"priority >= 0","project_ids":["`+c100ProjA+`","`+c100ProjB+`"]}`)
	if len(cfg.projectIDs) != 2 || cfg.projectIDs[0] != c100ProjA || cfg.projectIDs[1] != c100ProjB {
		t.Fatalf("projectIDs = %v, want [A B] in config order", cfg.projectIDs)
	}
}

func TestC102PQLEmptyScopeGroupByMetricIgnored(t *testing.T) {
	// Row-list precedent, unchanged by the widening: group_by/metric
	// accepted-but-ignored on an empty-scope widget too.
	cfg := c100PQLConfig(t, `{"query":"priority >= 0","project_ids":[],"group_by":"state","metric":"estimate_points"}`)
	if cfg.groupBy != "" {
		t.Fatalf("groupBy = %q, want ignored (empty)", cfg.groupBy)
	}
	if cfg.metric != WidgetMetricCount {
		t.Fatalf("metric = %q, want count (ignored)", cfg.metric)
	}
}

func TestC102PQLEmptyScopeOrderByAndLimitPass(t *testing.T) {
	// ORDER BY (multi-key) + LIMIT survive write-time validation on
	// an empty-scope widget — they are the union's global sort and
	// total cap at compute time.
	cfg := c100PQLConfig(t, `{"query":"priority >= 0 ORDER BY priority DESC, created_at ASC LIMIT 5","project_ids":[],"max_rows":2}`)
	if cfg.maxRows != 2 {
		t.Fatalf("maxRows = %d, want 2", cfg.maxRows)
	}
	if cfg.projectIDs != nil {
		t.Fatalf("projectIDs = %v, want nil", cfg.projectIDs)
	}
}

func TestC102PQLEmptyScopeRejects(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{"filters key with empty scope", `{"query":"priority >= 0","project_ids":[],"filters":{"states":["` + c100ProjA + `"]}}`},
		{"empty filters key with empty scope", `{"query":"priority >= 0","project_ids":[],"filters":{}}`},
		{"filters key with missing scope", `{"query":"priority >= 0","filters":{"states":["` + c100ProjA + `"]}}`},
		{"blank query with empty scope", `{"query":"   ","project_ids":[]}`},
		{"blank query with missing scope", `{"query":"   "}`},
		{"missing query with empty scope", `{"project_ids":[]}`},
		{"bad syntax with empty scope", `{"query":"priority >=","project_ids":[]}`},
		{"unknown field with empty scope", `{"query":"sprint = 3","project_ids":[]}`},
		{"malformed project UUID", `{"query":"priority >= 0","project_ids":["not-a-uuid"]}`},
		{"sort_by outside columns with empty scope", `{"query":"priority >= 0","project_ids":[],"columns":["title"],"sort_by":"priority"}`},
		{"max_rows 0 with empty scope", `{"query":"priority >= 0","project_ids":[],"max_rows":0}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := parseWidgetConfig(WidgetTypePQLTable, json.RawMessage(tc.raw)); !errors.Is(err, ErrInvalidWidgetConfig) {
				t.Fatalf("parse %s: err = %v, want ErrInvalidWidgetConfig", tc.raw, err)
			}
		})
	}
}
