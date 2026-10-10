package api

// Route-coverage test (C7T7): docs/openapi.yaml must document every route
// the server registers. registerAllAPIRoutes mirrors cmd/glance/main.go's
// registration sequence exactly (nil pools — registration never touches
// the DB), so any new route added to main.go without a spec entry fails
// this test. The served GET /api/v1/openapi.json returns the embedded
// docs.OpenAPI bytes verbatim, so what this test parses is what clients
// get.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/labstack/echo/v5"

	"glance/docs"
)

var openapiParamRe = regexp.MustCompile(`:([A-Za-z_]+)`)

// echoPathToOpenAPI converts Echo's :param style to OpenAPI's {param}.
func echoPathToOpenAPI(p string) string {
	return openapiParamRe.ReplaceAllString(p, "{$1}")
}

// registerAllAPIRoutes mirrors cmd/glance/main.go (Tasks 5+, C2T7, C4T2,
// C5T0, C5T2, C7T4, C7T7). Keep the two in lockstep: a route registered
// in main.go but not here weakens this test silently.
func registerAllAPIRoutes(e *echo.Echo) {
	authHandler := &AuthHandler{}
	RegisterAuthRoutes(e, authHandler)

	RegisterWSRoutes(e, &WSHandler{})

	workspaceHandler := &WorkspaceHandler{}
	RegisterWorkspaceRoutes(e, workspaceHandler)

	projectHandler := &ProjectHandler{}
	RegisterProjectRoutes(e, projectHandler)
	RegisterImportRoutes(e, projectHandler)
	RegisterAutomationRoutes(e, &AutomationHandler{})

	issueHandler := &IssueHandler{}
	RegisterIssueRoutes(e, issueHandler)
	RegisterWorkItemRoutes(e, issueHandler)
	RegisterTokenRoutes(e, &TokenHandler{})
	RegisterNotifyRoutes(e, &NotifyHandler{})
	RegisterAdminRoutes(e, &AdminHandler{})
	RegisterTaxonomyRoutes(e, issueHandler)
	RegisterStateRoutes(e, &StateHandler{})
	RegisterSatelliteRoutes(e, issueHandler)
	RegisterIntakeRoutes(e, issueHandler)
	RegisterIssueLinkRoutes(e, issueHandler)
	RegisterMyWorkRoutes(e, &MyWorkHandler{})
	RegisterCycleRoutes(e, issueHandler)
	RegisterModuleRoutes(e, issueHandler)
	RegisterPageRoutes(e, issueHandler)
	RegisterReleaseRoutes(e, issueHandler)
	RegisterTemplateRoutes(e, issueHandler)
	RegisterCustomFieldRoutes(e, issueHandler)
	RegisterPublicRoutes(e, issueHandler)
	RegisterAnalyticsRoutes(e, issueHandler)
	RegisterTimeSummaryRoutes(e, issueHandler)
	RegisterActivityRoutes(e, issueHandler)
	RegisterOverviewRoutes(e, issueHandler)

	RegisterFavoriteRoutes(e, &FavoriteHandler{})

	RegisterAIRoutes(e, &AIHandler{})

	RegisterOpenAPIRoutes(e)
}

func loadOpenAPISpec(t *testing.T) map[string]map[string]json.RawMessage {
	t.Helper()
	var spec struct {
		OpenAPI string                                `json:"openapi"`
		Paths   map[string]map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(docs.OpenAPI, &spec); err != nil {
		t.Fatalf("embedded openapi spec is not valid JSON: %v", err)
	}
	if !strings.HasPrefix(spec.OpenAPI, "3.0.") {
		t.Fatalf("spec openapi = %q, want 3.0.x", spec.OpenAPI)
	}
	if len(spec.Paths) == 0 {
		t.Fatal("spec has no paths")
	}
	return spec.Paths
}

func TestOpenAPISpecCoversAllRoutes(t *testing.T) {
	e := echo.New()
	registerAllAPIRoutes(e)
	paths := loadOpenAPISpec(t)

	var missing []string
	for _, r := range e.Router().Routes() {
		switch r.Method {
		case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		default:
			continue // echo internal routes (echo_route_not_found)
		}
		if !strings.HasPrefix(r.Path, "/api/v1/") {
			continue // /ws, /health, SPA catch-all: out of spec scope
		}
		p := echoPathToOpenAPI(r.Path)
		ops, ok := paths[p]
		if !ok {
			missing = append(missing, r.Method+" "+p+" (path missing from spec)")
			continue
		}
		if _, ok := ops[strings.ToLower(r.Method)]; !ok {
			missing = append(missing, r.Method+" "+p+" (method missing from spec)")
		}
	}
	if len(missing) > 0 {
		t.Fatalf("openapi spec is missing %d registered routes:\n%s",
			len(missing), strings.Join(missing, "\n"))
	}
}

func TestOpenAPIOperationsHaveSummaryAndResponses(t *testing.T) {
	paths := loadOpenAPISpec(t)
	var bad []string
	for p, ops := range paths {
		for method, raw := range ops {
			var op struct {
				Summary   string         `json:"summary"`
				Responses map[string]any `json:"responses"`
			}
			if err := json.Unmarshal(raw, &op); err != nil {
				t.Fatalf("spec path %s %s: invalid operation: %v", p, method, err)
			}
			if op.Summary == "" {
				bad = append(bad, method+" "+p+" (missing summary)")
			}
			if len(op.Responses) == 0 {
				bad = append(bad, method+" "+p+" (missing responses)")
			}
		}
	}
	if len(bad) > 0 {
		t.Fatalf("spec has %d operations missing summary/responses:\n%s",
			len(bad), strings.Join(bad, "\n"))
	}
}

func TestOpenAPIEndpointServesSpec(t *testing.T) {
	e := echo.New()
	RegisterOpenAPIRoutes(e)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/openapi.json", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/openapi.json = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("Content-Type = %q, want application/json", ct)
	}
	var spec struct {
		OpenAPI string `json:"openapi"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &spec); err != nil {
		t.Fatalf("endpoint body is not valid JSON: %v", err)
	}
	if !strings.HasPrefix(spec.OpenAPI, "3.0.") {
		t.Fatalf("endpoint openapi = %q, want 3.0.x", spec.OpenAPI)
	}
}
