package api

// CSV issue importer endpoints (C4T8).
//
//   POST /api/v1/workspaces/:slug/projects/:identifier/imports
//     multipart: "file" = the CSV, "mapping" = JSON ImportMapping.
//     Creates issues; per-row errors never abort the batch.
//     200 {created, failed, errors:[{row, message}]}. Member (15)+.
//
//   POST .../imports/preview — same inputs, no writes.
//     200 {rows:[{row, values}], errors:[...]}.
//
//   GET  .../imports/template.csv — the template CSV download.
//
// The request body is hard-capped (10MB + 1MiB headroom, same pattern as
// the attachment upload) so a hostile body can't buffer unboundedly.

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/labstack/echo/v5"

	"glance/internal/service"
)

// importMaxBytes caps the CSV upload body: 10MB + 1MiB headroom.
const importMaxBytes = 10<<20 + (1 << 20)

// importError maps service sentinel errors to HTTP statuses.
func importError(c *echo.Context, err error) error {
	switch {
	case errors.Is(err, service.ErrImportMapping) || errors.Is(err, service.ErrImportEmpty):
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, err.Error(), nil)
	default:
		return projectError(c, err)
	}
}

// RegisterImportRoutes mounts the importer endpoints on the project group.
func RegisterImportRoutes(e *echo.Echo, h *ProjectHandler) {
	g := e.Group("/api/v1/workspaces/:slug/projects/:identifier/imports", RequireAuth(h.Pool))
	g.POST("", h.importIssues)
	g.POST("/preview", h.previewImport)
	g.GET("/template.csv", h.importTemplate)
	g.POST("/github/preview", h.previewGitHubImport)
	g.POST("/github", h.importGitHubIssues)
	g.POST("/jira/preview", h.previewJiraImport)
	g.POST("/jira", h.importJiraIssues)
}

// importMultipart extracts the CSV file and the JSON mapping from a
// multipart request.
func importMultipart(c *echo.Context) (file interface {
	Read([]byte) (int, error)
	Close() error
}, mapping service.ImportMapping, ok bool) {
	r := c.Request()
	r.Body = http.MaxBytesReader(c.Response(), r.Body, importMaxBytes)
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		WriteError(c, http.StatusRequestEntityTooLarge, ErrCodePayloadTooLarge,
			"request body exceeds the 10MB import size limit", nil)
		return nil, mapping, false
	}
	f, fh, err := r.FormFile("file")
	if err != nil || fh == nil {
		WriteError(c, http.StatusBadRequest, ErrCodeBadRequest,
			"multipart field \"file\" is required", nil)
		return nil, mapping, false
	}
	if err := json.Unmarshal([]byte(r.FormValue("mapping")), &mapping); err != nil {
		f.Close()
		WriteError(c, http.StatusBadRequest, ErrCodeBadRequest,
			"multipart field \"mapping\" must be valid JSON", nil)
		return nil, mapping, false
	}
	return f, mapping, true
}

// importIssues implements POST .../imports: parse, validate per-row,
// insert valid rows.
func (h *ProjectHandler) importIssues(c *echo.Context) error {
	f, mapping, ok := importMultipart(c)
	if !ok {
		return nil
	}
	defer f.Close()
	res, err := service.ImportIssues(c.Request().Context(), h.Pool,
		c.Param("slug"), c.Param("identifier"), CurrentUser(c).ID, f, mapping)
	if err != nil {
		return importError(c, err)
	}
	return c.JSON(http.StatusOK, res)
}

// previewImport implements POST .../imports/preview: parse + validate the
// first 10 rows, no writes.
func (h *ProjectHandler) previewImport(c *echo.Context) error {
	f, mapping, ok := importMultipart(c)
	if !ok {
		return nil
	}
	defer f.Close()
	prev, err := service.PreviewImport(c.Request().Context(), h.Pool,
		c.Param("slug"), c.Param("identifier"), CurrentUser(c).ID, f, mapping)
	if err != nil {
		return importError(c, err)
	}
	return c.JSON(http.StatusOK, prev)
}

// importTemplate implements GET .../imports/template.csv.
func (h *ProjectHandler) importTemplate(c *echo.Context) error {
	return c.Blob(http.StatusOK, "text/csv; charset=utf-8",
		[]byte(service.ImportTemplateCSV()))
}

// ---------- GitHub importer (C8T2) ----------
//
//   POST .../imports/github/preview — JSON {owner, repo, token,
//     state_filter?, max?}: fetch from the GitHub REST API, report how the
//     first 10 issues would map. No writes. Member (15)+.
//   POST .../imports/github — same inputs: import the issues.
//
// The PAT travels in the JSON body only: it is forwarded to api.github.com
// as a Bearer header and is never stored, never logged, and never echoed
// in error messages (pinned by TestGitHubTokenNeverPersisted).
//
// Error honesty: repo-not-found 404 surfaces GitHub's message, bad token
// 401, rate-limit exhaustion 429 with Retry-After (never retried
// silently), other upstream failures 502.

// githubImportMaxBody caps the GitHub import JSON body: 1MB is generous
// for owner/repo/token/filter fields (no files ride this endpoint).
const githubImportMaxBody = 1 << 20

// githubImportBody is the JSON body of the github import endpoints.
type githubImportBody struct {
	Owner       string `json:"owner"`
	Repo        string `json:"repo"`
	Token       string `json:"token"`
	StateFilter string `json:"state_filter"`
	Max         int    `json:"max"`
}

// githubImportError maps the github importer service errors to HTTP
// statuses. GitHub's own messages are surfaced for 404/502; the token is
// never part of any message.
func githubImportError(c *echo.Context, err error) error {
	var rl *service.GitHubRateLimitError
	switch {
	case errors.As(err, &rl):
		c.Response().Header().Set("Retry-After", strconv.Itoa(rl.RetryAfter))
		return WriteError(c, http.StatusTooManyRequests, ErrCodeRateLimited,
			"github rate limit exceeded: "+rl.Message,
			map[string]any{"retry_after": rl.RetryAfter})
	case errors.Is(err, service.ErrGitHubRepoNotFound):
		return WriteError(c, http.StatusNotFound, ErrCodeNotFound, err.Error(), nil)
	case errors.Is(err, service.ErrGitHubUnauthorized):
		return WriteError(c, http.StatusUnauthorized, ErrCodeUnauthorized, err.Error(), nil)
	case errors.Is(err, service.ErrGitHubUpstream):
		return WriteError(c, http.StatusBadGateway, ErrCodeBadGateway, err.Error(), nil)
	case errors.Is(err, service.ErrGitHubBadInput):
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, err.Error(), nil)
	default:
		return projectError(c, err)
	}
}

// decodeGitHubImportBody reads the JSON import body under the size cap.
func decodeGitHubImportBody(c *echo.Context) (service.GitHubImportInput, bool) {
	r := c.Request()
	r.Body = http.MaxBytesReader(c.Response(), r.Body, githubImportMaxBody)
	var b githubImportBody
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
		WriteError(c, http.StatusBadRequest, ErrCodeBadRequest,
			"request body must be JSON {owner, repo, token, state_filter?, max?}", nil)
		return service.GitHubImportInput{}, false
	}
	return service.GitHubImportInput{
		Owner:       b.Owner,
		Repo:        b.Repo,
		Token:       b.Token,
		StateFilter: b.StateFilter,
		Max:         b.Max,
	}, true
}

// previewGitHubImport implements POST .../imports/github/preview.
func (h *ProjectHandler) previewGitHubImport(c *echo.Context) error {
	in, ok := decodeGitHubImportBody(c)
	if !ok {
		return nil
	}
	prev, err := service.PreviewGitHubImport(c.Request().Context(), h.Pool,
		c.Param("slug"), c.Param("identifier"), CurrentUser(c).ID, in)
	if err != nil {
		return githubImportError(c, err)
	}
	return c.JSON(http.StatusOK, prev)
}

// importGitHubIssues implements POST .../imports/github.
func (h *ProjectHandler) importGitHubIssues(c *echo.Context) error {
	in, ok := decodeGitHubImportBody(c)
	if !ok {
		return nil
	}
	res, err := service.ImportGitHubIssues(c.Request().Context(), h.Pool,
		c.Param("slug"), c.Param("identifier"), CurrentUser(c).ID, in)
	if err != nil {
		return githubImportError(c, err)
	}
	return c.JSON(http.StatusOK, res)
}

// ---------- Jira Cloud importer (C9T1) ----------
//
//   POST .../imports/jira/preview — JSON {site, email, api_token,
//     project_key, max?}: fetch from the Jira Cloud REST v3 API, report
//     how the first 10 issues would map. No writes. Member (15)+.
//   POST .../imports/jira — same inputs: import the issues.
//
// Jira Cloud only: `site` is the Atlassian subdomain ("acme" →
// https://acme.atlassian.net). Server/Data Center are deliberately
// unsupported — a user-controlled host would break the SSRF guard.
//
// The API token travels in the JSON body only: it is forwarded to
// <site>.atlassian.net as an HTTP Basic (email:token) header and is
// never stored, never logged, and never echoed in error messages
// (pinned by TestJiraTokenNeverPersisted).
//
// Error honesty: site/project-not-found 404 surfaces Jira's message,
// bad credentials 401, rate-limit exhaustion 429 with Retry-After
// (never retried silently), other upstream failures 502.

// jiraImportMaxBody caps the Jira import JSON body: 1MB is generous for
// site/email/token/key fields (no files ride this endpoint).
const jiraImportMaxBody = 1 << 20

// jiraImportBody is the JSON body of the jira import endpoints.
type jiraImportBody struct {
	Site       string `json:"site"`
	Email      string `json:"email"`
	APIToken   string `json:"api_token"`
	ProjectKey string `json:"project_key"`
	Max        int    `json:"max"`
}

// jiraImportError maps the jira importer service errors to HTTP
// statuses. Jira's own messages are surfaced for 404/502; the token is
// never part of any message.
func jiraImportError(c *echo.Context, err error) error {
	var rl *service.JiraRateLimitError
	switch {
	case errors.As(err, &rl):
		c.Response().Header().Set("Retry-After", strconv.Itoa(rl.RetryAfter))
		return WriteError(c, http.StatusTooManyRequests, ErrCodeRateLimited,
			"jira rate limit exceeded: "+rl.Message,
			map[string]any{"retry_after": rl.RetryAfter})
	case errors.Is(err, service.ErrJiraNotFound):
		return WriteError(c, http.StatusNotFound, ErrCodeNotFound, err.Error(), nil)
	case errors.Is(err, service.ErrJiraUnauthorized):
		return WriteError(c, http.StatusUnauthorized, ErrCodeUnauthorized, err.Error(), nil)
	case errors.Is(err, service.ErrJiraUpstream):
		return WriteError(c, http.StatusBadGateway, ErrCodeBadGateway, err.Error(), nil)
	case errors.Is(err, service.ErrJiraBadInput):
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, err.Error(), nil)
	default:
		return projectError(c, err)
	}
}

// decodeJiraImportBody reads the JSON import body under the size cap.
func decodeJiraImportBody(c *echo.Context) (service.JiraImportInput, bool) {
	r := c.Request()
	r.Body = http.MaxBytesReader(c.Response(), r.Body, jiraImportMaxBody)
	var b jiraImportBody
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
		WriteError(c, http.StatusBadRequest, ErrCodeBadRequest,
			"request body must be JSON {site, email, api_token, project_key, max?}", nil)
		return service.JiraImportInput{}, false
	}
	return service.JiraImportInput{
		Site:       b.Site,
		Email:      b.Email,
		APIToken:   b.APIToken,
		ProjectKey: b.ProjectKey,
		Max:        b.Max,
	}, true
}

// previewJiraImport implements POST .../imports/jira/preview.
func (h *ProjectHandler) previewJiraImport(c *echo.Context) error {
	in, ok := decodeJiraImportBody(c)
	if !ok {
		return nil
	}
	prev, err := service.PreviewJiraImport(c.Request().Context(), h.Pool,
		c.Param("slug"), c.Param("identifier"), CurrentUser(c).ID, in)
	if err != nil {
		return jiraImportError(c, err)
	}
	return c.JSON(http.StatusOK, prev)
}

// importJiraIssues implements POST .../imports/jira.
func (h *ProjectHandler) importJiraIssues(c *echo.Context) error {
	in, ok := decodeJiraImportBody(c)
	if !ok {
		return nil
	}
	res, err := service.ImportJiraIssues(c.Request().Context(), h.Pool,
		c.Param("slug"), c.Param("identifier"), CurrentUser(c).ID, in)
	if err != nil {
		return jiraImportError(c, err)
	}
	return c.JSON(http.StatusOK, res)
}
