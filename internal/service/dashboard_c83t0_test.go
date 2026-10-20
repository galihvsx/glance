package service

// C83T0: phase-2 widget config validation + helper unit tests. Pure
// functions — no database needed.

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestParseWidgetConfigPhase2Valid(t *testing.T) {
	cases := map[string]struct {
		typ string
		raw string
		// normalized expectations
		groupBy        string
		metric         string
		maxRows        int
		visualization  string
		gaugeColor     string
		showPercentage bool
		view           string
	}{
		"smart_counter minimal": {
			typ: "smart_counter", raw: `{"group_by":"assignee"}`,
			groupBy: "assignee", metric: "count", maxRows: 10,
			visualization: "grid", gaugeColor: "#3b82f6", showPercentage: true, view: "grid",
		},
		"smart_counter full": {
			typ:     "smart_counter",
			raw:     `{"group_by":"state_group","metric":"estimate_points","max_rows":25,"visualization":"table"}`,
			groupBy: "state_group", metric: "estimate_points", maxRows: 25,
			visualization: "table", gaugeColor: "#3b82f6", showPercentage: true, view: "grid",
		},
		"smart_gauge minimal": {
			typ: "smart_gauge", raw: `{"group_by":"project"}`,
			groupBy: "project", metric: "count", maxRows: 10,
			visualization: "grid", gaugeColor: "#3b82f6", showPercentage: true, view: "grid",
		},
		"smart_gauge full": {
			typ:     "smart_gauge",
			raw:     `{"group_by":"created_by","metric":"estimate_points","max_rows":1,"gauge_color":"#ff0000","show_percentage":false}`,
			groupBy: "created_by", metric: "estimate_points", maxRows: 1,
			visualization: "grid", gaugeColor: "#ff0000", showPercentage: false, view: "grid",
		},
		"table_2d minimal": {
			typ: "table_2d", raw: `{"group_by":"priority"}`,
			groupBy: "priority", metric: "count", maxRows: 10,
			visualization: "grid", gaugeColor: "#3b82f6", showPercentage: true, view: "grid",
		},
		"table_2d max_rows 100": {
			typ: "table_2d", raw: `{"group_by":"label","max_rows":100}`,
			groupBy: "label", metric: "count", maxRows: 100,
			visualization: "grid", gaugeColor: "#3b82f6", showPercentage: true, view: "grid",
		},
		"intake_breakdown minimal": {
			typ: "intake_breakdown", raw: `{}`,
			groupBy: "", metric: "count", maxRows: 10,
			visualization: "grid", gaugeColor: "#3b82f6", showPercentage: true, view: "grid",
		},
		"intake_breakdown table view": {
			typ: "intake_breakdown", raw: `{"view":"table"}`,
			groupBy: "", metric: "count", maxRows: 10,
			visualization: "grid", gaugeColor: "#3b82f6", showPercentage: true, view: "table",
		},
		"chart with new dims": {
			typ: "bar", raw: `{"group_by":"state_group"}`,
			groupBy: "state_group", metric: "count", maxRows: 10,
			visualization: "grid", gaugeColor: "#3b82f6", showPercentage: true, view: "grid",
		},
	}
	for name, tc := range cases {
		cfg, err := parseWidgetConfig(tc.typ, json.RawMessage(tc.raw))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if cfg.groupBy != tc.groupBy || cfg.metric != tc.metric || cfg.maxRows != tc.maxRows {
			t.Fatalf("%s: parsed = %+v", name, cfg)
		}
		if cfg.visualization != tc.visualization || cfg.gaugeColor != tc.gaugeColor ||
			cfg.showPercentage != tc.showPercentage || cfg.view != tc.view {
			t.Fatalf("%s: presentation parsed = %+v", name, cfg)
		}
	}
}

func TestParseWidgetConfigPhase2Rejects(t *testing.T) {
	cases := map[string]struct {
		typ string
		raw string
	}{
		"counter missing group_by":       {"smart_counter", `{}`},
		"counter bad group_by":           {"smart_counter", `{"group_by":"sprint"}`},
		"counter bad metric":             {"smart_counter", `{"group_by":"state","metric":"avg"}`},
		"counter bad visualization":      {"smart_counter", `{"group_by":"state","visualization":"pie"}`},
		"counter max_rows 0":             {"smart_counter", `{"group_by":"state","max_rows":0}`},
		"counter max_rows 101":           {"smart_counter", `{"group_by":"state","max_rows":101}`},
		"counter max_rows fractional":    {"smart_counter", `{"group_by":"state","max_rows":2.5}`},
		"counter gauge_color rejected":   {"smart_counter", `{"group_by":"state","gauge_color":"#ff0000"}`},
		"counter view rejected":          {"smart_counter", `{"group_by":"state","view":"table"}`},
		"gauge missing group_by":         {"smart_gauge", `{}`},
		"gauge bad metric":               {"smart_gauge", `{"group_by":"state","metric":"sum"}`},
		"gauge bad color no hash":        {"smart_gauge", `{"group_by":"state","gauge_color":"ff0000"}`},
		"gauge bad color short":          {"smart_gauge", `{"group_by":"state","gauge_color":"#fff"}`},
		"gauge bad color non-hex":        {"smart_gauge", `{"group_by":"state","gauge_color":"#gggggg"}`},
		"gauge show_percentage string":   {"smart_gauge", `{"group_by":"state","show_percentage":"yes"}`},
		"gauge max_rows rejected on bar": {"bar", `{"group_by":"state","max_rows":10}`},
		"bar rejects visualization":      {"bar", `{"group_by":"state","visualization":"table"}`},
		"bar rejects view":               {"bar", `{"group_by":"state","view":"grid"}`},
		"number rejects max_rows":        {"number", `{"max_rows":5}`},
		"table_2d missing group_by":      {"table_2d", `{}`},
		"table_2d bad group_by":          {"table_2d", `{"group_by":"team"}`},
		"table_2d max_rows 101":          {"table_2d", `{"group_by":"state","max_rows":101}`},
		"table_2d rejects gauge_color":   {"table_2d", `{"group_by":"state","gauge_color":"#ff0000"}`},
		"intake bad view":                {"intake_breakdown", `{"view":"cards"}`},
		"intake rejects max_rows":        {"intake_breakdown", `{"max_rows":5}`},
		"intake rejects gauge_color":     {"intake_breakdown", `{"gauge_color":"#ff0000"}`},
		"unknown widget type key":        {"smart_gauge", `{"group_by":"state","chart":"x"}`},
	}
	for name, tc := range cases {
		if _, err := parseWidgetConfig(tc.typ, json.RawMessage(tc.raw)); !errors.Is(err, ErrInvalidWidgetConfig) {
			t.Fatalf("%s: err = %v, want ErrInvalidWidgetConfig", name, err)
		}
	}
	// The widget_type itself is validated before config parsing.
	for _, bad := range []string{"radar", "SMART_COUNTER", ""} {
		if validWidgetType(bad) {
			t.Fatalf("validWidgetType(%q) = true", bad)
		}
	}
}

func TestParseWidgetConfigTable2DIgnoresMetric(t *testing.T) {
	// table_2d's metric is fixed to count (Plane parity): a metric key
	// is accepted and ignored, never an error.
	for _, raw := range []string{
		`{"group_by":"priority","metric":"estimate_points"}`,
		`{"group_by":"priority","metric":"whatever"}`,
		`{"group_by":"priority"}`,
	} {
		cfg, err := parseWidgetConfig("table_2d", json.RawMessage(raw))
		if err != nil {
			t.Fatalf("table_2d %s: %v", raw, err)
		}
		if cfg.metric != "count" {
			t.Fatalf("table_2d %s: metric = %q, want count (ignored)", raw, cfg.metric)
		}
	}
}

func TestParseWidgetConfigIntakeBreakdownIgnoresScoping(t *testing.T) {
	// intake_breakdown honors the dashboard's project scope only:
	// widget-level filters AND project_ids are ignored (Plane parity).
	cfg, err := parseWidgetConfig("intake_breakdown", json.RawMessage(
		`{"view":"table","filters":{"priorities":[1]},"project_ids":["00000000-0000-0000-0000-000000000001"],"group_by":"state","metric":"count"}`))
	if err != nil {
		t.Fatalf("intake_breakdown with scoping keys: %v", err)
	}
	if cfg.groupBy != "" || len(cfg.priorities) != 0 || len(cfg.projectIDs) != 0 {
		t.Fatalf("scoping not ignored: %+v", cfg)
	}
	if cfg.view != "table" {
		t.Fatalf("view = %q, want table", cfg.view)
	}
}

func TestValidWidgetTypeAndGroupByPhase2(t *testing.T) {
	for _, typ := range []string{"smart_counter", "smart_gauge", "table_2d", "intake_breakdown"} {
		if !validWidgetType(typ) {
			t.Fatalf("validWidgetType(%q) = false", typ)
		}
	}
	for _, gb := range []string{"state_group", "project", "created_by"} {
		if !validGroupBy(gb) {
			t.Fatalf("validGroupBy(%q) = false", gb)
		}
	}
	if !validMetric("count") || !validMetric("estimate_points") || validMetric("avg") {
		t.Fatal("validMetric wrong")
	}
}

func TestHexColor(t *testing.T) {
	for _, ok := range []string{"#3b82f6", "#FF0000", "#000000", "#abcdef"} {
		if !hexColor(ok) {
			t.Fatalf("hexColor(%q) = false", ok)
		}
	}
	for _, bad := range []string{"", "3b82f6", "#fff", "#ffff", "#gggggg", "#12345g", "##123456"} {
		if hexColor(bad) {
			t.Fatalf("hexColor(%q) = true", bad)
		}
	}
}

func TestSlugify(t *testing.T) {
	cases := map[string]string{
		"Sprint Board":        "sprint-board",
		"Q3: Review!":         "q3-review",
		"  spaced  out  ":     "spaced-out",
		"a/b\\c":              "a-b-c",
		"Ünïcödé stays ascii": "n-c-d-stays-ascii",
		"":                    "untitled",
		"---":                 "untitled",
		"already-slug":        "already-slug",
		"Widget, v2 (final)":  "widget-v2-final",
	}
	for in, want := range cases {
		if got := slugify(in); got != want {
			t.Fatalf("slugify(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDashboardTemplateCatalogValid(t *testing.T) {
	// Every template widget config must pass its own validation — the
	// catalog is a static contract, so pin it.
	catalog := DashboardTemplateCatalog()
	if len(catalog) < 3 {
		t.Fatalf("catalog has %d templates, want >= 3", len(catalog))
	}
	seen := map[string]bool{}
	for _, tpl := range catalog {
		if tpl.ID == "" || tpl.Name == "" || tpl.Description == "" {
			t.Fatalf("template missing id/name/description: %+v", tpl)
		}
		if seen[tpl.ID] {
			t.Fatalf("duplicate template id %q", tpl.ID)
		}
		seen[tpl.ID] = true
		if len(tpl.Widgets) == 0 {
			t.Fatalf("template %q has no widgets", tpl.ID)
		}
		for _, w := range tpl.Widgets {
			if !validWidgetType(w.WidgetType) {
				t.Fatalf("template %q widget %q: bad type %q", tpl.ID, w.Title, w.WidgetType)
			}
			if _, err := parseWidgetConfig(w.WidgetType, w.Config); err != nil {
				t.Fatalf("template %q widget %q: config invalid: %v", tpl.ID, w.Title, err)
			}
		}
	}
	if _, ok := lookupDashboardTemplate("nope"); ok {
		t.Fatal("lookupDashboardTemplate(nope) found")
	}
}
