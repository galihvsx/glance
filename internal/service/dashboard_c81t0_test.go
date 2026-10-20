package service

// C81T0: widget config validation + grid clamping unit tests. These are
// pure functions — no database needed.

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestParseWidgetConfigValid(t *testing.T) {
	cfg, err := parseWidgetConfig("bar", json.RawMessage(
		`{"group_by":"state","metric":"count","filters":{"assignees":["00000000-0000-0000-0000-000000000001"],"priorities":[1,3],"states":[],"labels":[]},"project_ids":["00000000-0000-0000-0000-000000000002"]}`))
	if err != nil {
		t.Fatalf("valid config: %v", err)
	}
	if cfg.groupBy != "state" || cfg.metric != "count" {
		t.Fatalf("parsed = %+v", cfg)
	}
	if len(cfg.assignees) != 1 || len(cfg.priorities) != 2 || len(cfg.projectIDs) != 1 {
		t.Fatalf("parsed = %+v", cfg)
	}
	if cfg.priorities[0] != 1 || cfg.priorities[1] != 3 {
		t.Fatalf("priorities = %v", cfg.priorities)
	}
}

func TestParseWidgetConfigRejects(t *testing.T) {
	validUUID := "00000000-0000-0000-0000-000000000001"
	cases := map[string]struct {
		typ string
		raw string
	}{
		"missing group_by on chart":     {"bar", `{}`},
		"bad group_by":                  {"bar", `{"group_by":"sprint"}`},
		"non-string group_by":           {"bar", `{"group_by":3}`},
		"bad metric":                    {"bar", `{"group_by":"state","metric":"sum"}`},
		"unknown top key":               {"bar", `{"group_by":"state","chart":"x"}`},
		"unknown filter key":            {"bar", `{"group_by":"state","filters":{"teams":[]}}`},
		"filters not object":            {"bar", `{"group_by":"state","filters":[]}`},
		"malformed assignee uuid":       {"bar", `{"group_by":"state","filters":{"assignees":["nope"]}}`},
		"malformed state uuid":          {"bar", `{"group_by":"state","filters":{"states":["nope"]}}`},
		"malformed label uuid":          {"bar", `{"group_by":"state","filters":{"labels":["nope"]}}`},
		"malformed project uuid":        {"bar", `{"group_by":"state","project_ids":["nope"]}`},
		"priority out of range":         {"bar", `{"group_by":"state","filters":{"priorities":[5]}}`},
		"priority negative":             {"bar", `{"group_by":"state","filters":{"priorities":[-1]}}`},
		"priority fractional":           {"bar", `{"group_by":"state","filters":{"priorities":[1.5]}}`},
		"priority not array":            {"bar", `{"group_by":"state","filters":{"priorities":"3"}}`},
		"project_ids not array":         {"bar", `{"group_by":"state","project_ids":"x"}`},
		"not json":                      {"bar", `{"group_by":`},
		"group_by required on donut":    {"donut", `{"metric":"count"}`},
		"empty assignee entry rejected": {"bar", `{"group_by":"state","filters":{"assignees":[""]}}`},
	}
	for name, tc := range cases {
		if _, err := parseWidgetConfig(tc.typ, json.RawMessage(tc.raw)); !errors.Is(err, ErrInvalidWidgetConfig) {
			t.Fatalf("%s: err = %v, want ErrInvalidWidgetConfig", name, err)
		}
	}

	// Uppercase hex UUIDs are accepted (isUUIDFormat allows A-F).
	if _, err := parseWidgetConfig("bar", json.RawMessage(
		`{"group_by":"state","filters":{"states":["`+validUUID+`"]}}`)); err != nil {
		t.Fatalf("uppercase uuid: %v", err)
	}
}

func TestParseWidgetConfigNumberIgnoresGroupBy(t *testing.T) {
	// number widgets need no group_by; a stale one is ignored, not rejected.
	cfg, err := parseWidgetConfig("number", json.RawMessage(`{"group_by":"state"}`))
	if err != nil {
		t.Fatalf("number with group_by: %v", err)
	}
	if cfg.groupBy != "" {
		t.Fatalf("groupBy = %q, want empty (ignored for number)", cfg.groupBy)
	}
	if _, err := parseWidgetConfig("number", json.RawMessage(`{}`)); err != nil {
		t.Fatalf("number empty config: %v", err)
	}
	// …but a bad metric is still rejected on number widgets.
	if _, err := parseWidgetConfig("number", json.RawMessage(`{"metric":"avg"}`)); !errors.Is(err, ErrInvalidWidgetConfig) {
		t.Fatalf("number bad metric: err = %v, want ErrInvalidWidgetConfig", err)
	}
}

func TestParseWidgetConfigEmptyMeansEmpty(t *testing.T) {
	cfg, err := parseWidgetConfig("bar", json.RawMessage(`{"group_by":"priority"}`))
	if err != nil {
		t.Fatalf("minimal config: %v", err)
	}
	if len(cfg.assignees) != 0 || len(cfg.priorities) != 0 || len(cfg.states) != 0 ||
		len(cfg.labels) != 0 || len(cfg.projectIDs) != 0 {
		t.Fatalf("minimal config parsed filters = %+v, want empty", cfg)
	}
	// nil/empty raw is "{}" — still needs group_by for chart types.
	if _, err := parseWidgetConfig("bar", nil); !errors.Is(err, ErrInvalidWidgetConfig) {
		t.Fatalf("nil config on bar: err = %v, want ErrInvalidWidgetConfig", err)
	}
}

func TestClampGrid(t *testing.T) {
	cases := []struct {
		in   [4]int
		want [4]int
	}{
		{[4]int{0, 0, 6, 4}, [4]int{0, 0, 6, 4}},
		{[4]int{-5, -1, 99, 0}, [4]int{0, 0, 12, 1}},
		{[4]int{11, 20, 1, 12}, [4]int{11, 20, 1, 12}},
		{[4]int{0, 0, 13, -3}, [4]int{0, 0, 12, 1}},
	}
	for _, tc := range cases {
		x, y, w, h := clampGrid(tc.in[0], tc.in[1], tc.in[2], tc.in[3])
		got := [4]int{x, y, w, h}
		if got != tc.want {
			t.Fatalf("clampGrid(%v) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestValidWidgetTypeAndGroupBy(t *testing.T) {
	for _, typ := range []string{"bar", "line", "area", "donut", "pie", "number"} {
		if !validWidgetType(typ) {
			t.Fatalf("validWidgetType(%q) = false", typ)
		}
	}
	for _, bad := range []string{"", "radar", "BAR", "table"} {
		if validWidgetType(bad) {
			t.Fatalf("validWidgetType(%q) = true", bad)
		}
	}
	for _, gb := range []string{"state", "priority", "assignee", "label"} {
		if !validGroupBy(gb) {
			t.Fatalf("validGroupBy(%q) = false", gb)
		}
	}
	if validGroupBy("sprint") {
		t.Fatal("validGroupBy(sprint) = true")
	}
}
