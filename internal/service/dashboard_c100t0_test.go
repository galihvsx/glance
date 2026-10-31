package service

// C100T0: pql_table widget config validation — the PQL-driven row-list
// widget. Pure config parsing (no database): query required and
// non-blank, parse + semantic validation at write time (400 with
// position, typed as *PQLQueryError beneath ErrInvalidWidgetConfig),
// exactly one pinned project, filters rejected (PQL is the filter),
// meaningless keys rejected, group_by/metric accepted-but-ignored.

import (
	"encoding/json"
	"errors"
	"testing"
)

const c100ProjA = "11111111-1111-1111-1111-111111111111"
const c100ProjB = "22222222-2222-2222-2222-222222222222"

func c100PQLConfig(t *testing.T, raw string) *widgetConfig {
	t.Helper()
	cfg, err := parseWidgetConfig(WidgetTypePQLTable, json.RawMessage(raw))
	if err != nil {
		t.Fatalf("parse pql_table %s: %v", raw, err)
	}
	return cfg
}

func TestC100PQLValidConfigDefaults(t *testing.T) {
	cfg := c100PQLConfig(t, `{"query":"priority >= 0","project_ids":["`+c100ProjA+`"]}`)
	if cfg.query != "priority >= 0" {
		t.Fatalf("query = %q, want the stored query", cfg.query)
	}
	if len(cfg.projectIDs) != 1 || cfg.projectIDs[0] != c100ProjA {
		t.Fatalf("projectIDs = %v, want [%s]", cfg.projectIDs, c100ProjA)
	}
	if cfg.maxRows != 10 {
		t.Fatalf("maxRows = %d, want default 10", cfg.maxRows)
	}
	if len(cfg.columns) != 7 {
		t.Fatalf("columns = %v, want all seven", cfg.columns)
	}
	if cfg.sortBy != "state" || cfg.sortDir != "desc" {
		t.Fatalf("sort = %s %s, want state desc", cfg.sortBy, cfg.sortDir)
	}
	if cfg.groupBy != "" {
		t.Fatalf("groupBy = %q, want ignored (empty)", cfg.groupBy)
	}
}

func TestC100PQLGroupByMetricIgnored(t *testing.T) {
	// Row-list precedent: group_by/metric accepted-but-ignored.
	cfg := c100PQLConfig(t, `{"query":"priority >= 0","project_ids":["`+c100ProjA+`"],"group_by":"state","metric":"estimate_points"}`)
	if cfg.groupBy != "" {
		t.Fatalf("groupBy = %q, want ignored (empty)", cfg.groupBy)
	}
	if cfg.metric != WidgetMetricCount {
		t.Fatalf("metric = %q, want count (ignored)", cfg.metric)
	}
}

func TestC100PQLRejects(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{"missing query", `{"project_ids":["` + c100ProjA + `"]}`},
		{"blank query", `{"query":"   ","project_ids":["` + c100ProjA + `"]}`},
		{"empty query", `{"query":"","project_ids":["` + c100ProjA + `"]}`},
		{"non-string query", `{"query":42,"project_ids":["` + c100ProjA + `"]}`},
		{"no projects", `{"query":"priority >= 0","project_ids":[]}`},
		{"missing projects", `{"query":"priority >= 0"}`},
		{"two projects", `{"query":"priority >= 0","project_ids":["` + c100ProjA + `","` + c100ProjB + `"]}`},
		{"filters key", `{"query":"priority >= 0","project_ids":["` + c100ProjA + `"],"filters":{"states":["` + c100ProjA + `"]}}`},
		{"empty filters key", `{"query":"priority >= 0","project_ids":["` + c100ProjA + `"],"filters":{}}`},
		{"visualization key", `{"query":"priority >= 0","project_ids":["` + c100ProjA + `"],"visualization":"table"}`},
		{"gauge_color key", `{"query":"priority >= 0","project_ids":["` + c100ProjA + `"],"gauge_color":"#ff0000"}`},
		{"view key", `{"query":"priority >= 0","project_ids":["` + c100ProjA + `"],"view":"table"}`},
		{"unknown key", `{"query":"priority >= 0","project_ids":["` + c100ProjA + `"],"mode":"pql"}`},
		{"bad syntax", `{"query":"priority >=","project_ids":["` + c100ProjA + `"]}`},
		{"trailing AND", `{"query":"priority >= 0 AND","project_ids":["` + c100ProjA + `"]}`},
		{"unknown field", `{"query":"sprint = 3","project_ids":["` + c100ProjA + `"]}`},
		{"unknown sort field", `{"query":"priority >= 0 ORDER BY due","project_ids":["` + c100ProjA + `"]}`},
		{"LIMIT out of range", `{"query":"priority >= 0 LIMIT 0","project_ids":["` + c100ProjA + `"]}`},
		{"sort_by outside columns", `{"query":"priority >= 0","project_ids":["` + c100ProjA + `"],"columns":["title"],"sort_by":"priority"}`},
		{"bad column", `{"query":"priority >= 0","project_ids":["` + c100ProjA + `"],"columns":["title","sprint"]}`},
		{"max_rows 0", `{"query":"priority >= 0","project_ids":["` + c100ProjA + `"],"max_rows":0}`},
		{"max_rows 101", `{"query":"priority >= 0","project_ids":["` + c100ProjA + `"],"max_rows":101}`},
		{"malformed project UUID", `{"query":"priority >= 0","project_ids":["not-a-uuid"]}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := parseWidgetConfig(WidgetTypePQLTable, json.RawMessage(tc.raw)); !errors.Is(err, ErrInvalidWidgetConfig) {
				t.Fatalf("parse %s: err = %v, want ErrInvalidWidgetConfig", tc.raw, err)
			}
		})
	}
}

func TestC100PQLParseErrorCarriesPosition(t *testing.T) {
	// The handler maps the typed PQL error to 400 with details
	// {"position": N}; the wrapped config error must preserve it.
	_, err := parseWidgetConfig(WidgetTypePQLTable,
		json.RawMessage(`{"query":"priority >=","project_ids":["`+c100ProjA+`"]}`))
	if !errors.Is(err, ErrInvalidWidgetConfig) {
		t.Fatalf("err = %v, want ErrInvalidWidgetConfig", err)
	}
	var pqlErr *PQLQueryError
	if !errors.As(err, &pqlErr) {
		t.Fatalf("err = %v, want a *PQLQueryError beneath it", err)
	}
	if pqlErr.Pos <= 0 {
		t.Fatalf("PQL position = %d, want > 0", pqlErr.Pos)
	}
}

func TestC100PQLQueryRejectedOnOtherTypes(t *testing.T) {
	// A query key is meaningless for every non-pql_table type.
	for _, typ := range []string{WidgetTypeBar, WidgetTypeWorkItemsTable, WidgetTypeAssignedToMe, WidgetTypeNumber} {
		raw := `{"query":"priority >= 0","group_by":"state"}`
		if typ == WidgetTypeWorkItemsTable || typ == WidgetTypeAssignedToMe {
			raw = `{"query":"priority >= 0"}`
		}
		if _, err := parseWidgetConfig(typ, json.RawMessage(raw)); !errors.Is(err, ErrInvalidWidgetConfig) {
			t.Fatalf("query on %s: err = %v, want ErrInvalidWidgetConfig", typ, err)
		}
	}
}

func TestC100PQLValidOrderByAndLimitPass(t *testing.T) {
	// ORDER BY (multi-key) + LIMIT are part of the stored query and
	// must survive write-time validation — they are the widget's sort
	// and total cap at compute time.
	cfg := c100PQLConfig(t, `{"query":"priority >= 0 ORDER BY priority DESC, created_at ASC LIMIT 5","project_ids":["`+c100ProjA+`"],"max_rows":2}`)
	if cfg.maxRows != 2 {
		t.Fatalf("maxRows = %d, want 2", cfg.maxRows)
	}
}
