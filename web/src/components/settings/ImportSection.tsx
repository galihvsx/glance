// Issue import UI (C4T8 CSV backend + C8T2 GitHub importer + C9T1 Jira
// Cloud importer).
//
// Project settings → Import tab: a source selector (Jira / GitHub / CSV
// file) with the shared preview → import → result flow. The CSV mapping
// form follows the backend's ImportMapping contract (title required, the
// rest optional); the GitHub form collects owner/repo/token + filters;
// the Jira form collects site/email/API token/project key. Secrets are
// sent in the JSON body only and are never stored anywhere — the inputs
// are password fields and the values are cleared from state after the
// import runs.

import { useState } from "react";
import { AlertTriangle, CheckCircle2, Cloud, FileUp, GitBranch } from "lucide-react";
import { ApiError } from "../../lib/api";
import {
  buildCsvMapping,
  fetchImportTemplate,
  previewCsvImport,
  previewGitHubImport,
  previewJiraImport,
  runCsvImport,
  runGitHubImport,
  runJiraImport,
  validateGitHubForm,
  validateJiraForm,
  type GitHubImportPreview,
  type GitHubImportPreviewRow,
  type GitHubImportResult,
  type ImportMapping,
  type ImportPreview,
  type ImportResult,
  type ImportRowError,
  type JiraImportPreview,
  type JiraImportPreviewRow,
  type JiraImportResult,
} from "../../lib/import";
import { Alert, AlertDescription } from "../ui/alert";
import { Badge } from "../ui/badge";
import { Button } from "../ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "../ui/card";
import { Input } from "../ui/input";
import { Label } from "../ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "../ui/select";
import { Spinner } from "../ui/spinner";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "../ui/table";

function errMsg(err: unknown, fallback: string): string {
  return err instanceof ApiError ? err.message : fallback;
}

type Source = "csv" | "github" | "jira";

// ---------- shared bits ----------

function RowErrors({ errors, label }: { errors: ImportRowError[]; label: string }) {
  if (errors.length === 0) return null;
  return (
    <Alert variant="destructive" className="mt-4">
      <AlertTriangle className="h-4 w-4" />
      <AlertDescription>
        <p className="font-medium">
          {errors.length} {label} {errors.length === 1 ? "error" : "errors"}
        </p>
        <ul className="mt-1 max-h-40 space-y-1 overflow-y-auto text-sm">
          {errors.map((e, i) => (
            <li key={i} className="font-mono text-xs">
              #{e.row}: {e.message}
            </li>
          ))}
        </ul>
      </AlertDescription>
    </Alert>
  );
}

function Stat({ label, value }: { label: string; value: React.ReactNode }) {
  return (
    <div className="rounded-md border px-3 py-2">
      <div className="text-lg font-semibold">{value}</div>
      <div className="text-xs text-muted-foreground">{label}</div>
    </div>
  );
}

// ---------- CSV panel ----------

const CSV_FIELDS: { key: keyof ImportMapping; label: string; hint: string }[] = [
  { key: "title", label: "Title *", hint: "Required — the CSV column holding the issue title" },
  { key: "description", label: "Description", hint: "Column holding the issue body" },
  { key: "state", label: "State", hint: "State name (must exist)" },
  { key: "priority", label: "Priority", hint: "none/low/medium/high/urgent or 0–4" },
  { key: "labels", label: "Labels", hint: "Comma-separated label names (must exist)" },
  { key: "assignee_email", label: "Assignee email", hint: "Workspace member email" },
  { key: "start_date", label: "Start date", hint: "YYYY-MM-DD" },
  { key: "target_date", label: "Target date", hint: "YYYY-MM-DD" },
];

function CsvPanel({ slug, identifier }: { slug: string; identifier: string }) {
  const [file, setFile] = useState<File | null>(null);
  const [fields, setFields] = useState<Record<string, string>>({
    title: "title",
    description: "description",
    state: "state",
    priority: "priority",
    labels: "labels",
    assignee_email: "assignee_email",
    start_date: "start_date",
    target_date: "target_date",
  });
  const [preview, setPreview] = useState<ImportPreview | null>(null);
  const [result, setResult] = useState<ImportResult | null>(null);
  const [busy, setBusy] = useState<"preview" | "import" | null>(null);
  const [error, setError] = useState<string | null>(null);

  const mapping = buildCsvMapping(fields);

  const doPreview = async () => {
    if (!file) return;
    setBusy("preview");
    setError(null);
    setResult(null);
    try {
      setPreview(await previewCsvImport(slug, identifier, file, mapping));
    } catch (e) {
      setError(errMsg(e, "Preview failed"));
    } finally {
      setBusy(null);
    }
  };

  const doImport = async () => {
    if (!file) return;
    setBusy("import");
    setError(null);
    setPreview(null);
    try {
      setResult(await runCsvImport(slug, identifier, file, mapping));
    } catch (e) {
      setError(errMsg(e, "Import failed"));
    } finally {
      setBusy(null);
    }
  };

  const downloadTemplate = async () => {
    try {
      const blob = await fetchImportTemplate(slug, identifier);
      const url = URL.createObjectURL(blob);
      const a = document.createElement("a");
      a.href = url;
      a.download = "glance-import-template.csv";
      a.click();
      URL.revokeObjectURL(url);
    } catch (e) {
      setError(errMsg(e, "Template download failed"));
    }
  };

  return (
    <div className="space-y-4">
      <div className="grid gap-4 md:grid-cols-2">
        <div className="space-y-2">
          <Label htmlFor="csv-file">CSV file</Label>
          <Input
            id="csv-file"
            type="file"
            accept=".csv,text/csv"
            onChange={(e) => setFile(e.target.files?.[0] ?? null)}
          />
          <Button variant="outline" size="sm" onClick={downloadTemplate}>
            Download template
          </Button>
        </div>
        <div className="space-y-2">
          <Label>Column mapping</Label>
          <div className="grid grid-cols-2 gap-2">
            {CSV_FIELDS.map((f) => (
              <div key={f.key} className="space-y-1">
                <Label htmlFor={`csv-${f.key}`} className="text-xs">
                  {f.label}
                </Label>
                <Input
                  id={`csv-${f.key}`}
                  value={fields[f.key] ?? ""}
                  title={f.hint}
                  onChange={(e) => setFields({ ...fields, [f.key]: e.target.value })}
                />
              </div>
            ))}
          </div>
          <p className="text-xs text-muted-foreground">
            Name the CSV header feeding each field. Unknown labels, states or
            assignees fail their row — they are never created.
          </p>
        </div>
      </div>

      <div className="flex gap-2">
        <Button onClick={doPreview} disabled={!file || busy !== null} variant="outline">
          {busy === "preview" ? <Spinner className="mr-2" /> : null}Preview
        </Button>
        <Button onClick={doImport} disabled={!file || busy !== null || !fields.title.trim()}>
          {busy === "import" ? <Spinner className="mr-2" /> : null}Import
        </Button>
      </div>

      {error && (
        <Alert variant="destructive">
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}

      {preview && (
        <Card>
          <CardHeader>
            <CardTitle className="text-sm">
              Preview — first {preview.rows.length} rows
            </CardTitle>
          </CardHeader>
          <CardContent>
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Row</TableHead>
                  {Object.keys(preview.rows[0]?.values ?? {}).map((h) => (
                    <TableHead key={h}>{h}</TableHead>
                  ))}
                </TableRow>
              </TableHeader>
              <TableBody>
                {preview.rows.map((r) => (
                  <TableRow key={r.row}>
                    <TableCell className="font-mono">{r.row}</TableCell>
                    {Object.keys(preview.rows[0]?.values ?? {}).map((h) => (
                      <TableCell key={h} className="max-w-48 truncate">
                        {r.values[h]}
                      </TableCell>
                    ))}
                  </TableRow>
                ))}
              </TableBody>
            </Table>
            <RowErrors errors={preview.errors} label="row" />
          </CardContent>
        </Card>
      )}

      {result && (
        <Card>
          <CardHeader>
            <CardTitle className="flex items-center gap-2 text-sm">
              <CheckCircle2 className="h-4 w-4 text-green-600" /> Import result
            </CardTitle>
          </CardHeader>
          <CardContent>
            <div className="grid grid-cols-2 gap-2 sm:grid-cols-4">
              <Stat label="Created" value={result.created} />
              <Stat label="Failed" value={result.failed} />
            </div>
            <RowErrors errors={result.errors} label="row" />
          </CardContent>
        </Card>
      )}
    </div>
  );
}

// ---------- GitHub panel ----------

function GitHubPreviewTable({ preview }: { preview: GitHubImportPreview }) {
  return (
    <Table>
      <TableHeader>
        <TableRow>
          <TableHead>#</TableHead>
          <TableHead>Title</TableHead>
          <TableHead>State</TableHead>
          <TableHead>Labels</TableHead>
          <TableHead>Assignees</TableHead>
          <TableHead>Comments</TableHead>
          <TableHead>Milestone</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {preview.rows.map((r: GitHubImportPreviewRow) => (
          <TableRow key={r.number} className={r.already_imported ? "opacity-60" : undefined}>
            <TableCell className="font-mono">#{r.number}</TableCell>
            <TableCell className="max-w-64">
              <span className="block truncate" title={r.title}>
                {r.title}
              </span>
              {r.already_imported && (
                <Badge variant="secondary" className="mt-1">
                  already imported
                </Badge>
              )}
            </TableCell>
            <TableCell>
              <Badge variant="outline">{r.state}</Badge>
            </TableCell>
            <TableCell>
              <div className="flex max-w-48 flex-wrap gap-1">
                {r.labels.map((l) => (
                  <Badge
                    key={l}
                    variant={r.new_labels.includes(l) ? "default" : "outline"}
                    title={r.new_labels.includes(l) ? "Will be created" : "Exists"}
                  >
                    {l}
                    {r.new_labels.includes(l) ? " +" : ""}
                  </Badge>
                ))}
                {r.labels.length === 0 && <span className="text-muted-foreground">—</span>}
              </div>
            </TableCell>
            <TableCell>
              <div className="flex max-w-40 flex-wrap gap-1">
                {r.assignees.map((a) => (
                  <Badge key={a} variant="outline" title={a}>
                    @{a}
                  </Badge>
                ))}
                {r.assignees.length === 0 && <span className="text-muted-foreground">—</span>}
              </div>
              {r.assignees.length > 0 && (
                <div className="mt-1 text-xs text-muted-foreground">
                  {r.assignee_matched ? "matched a member" : "no member match"}
                </div>
              )}
            </TableCell>
            <TableCell className="font-mono">{r.comments}</TableCell>
            <TableCell>
              {r.milestone ? (
                <Badge variant="outline" title="Milestones are not imported">
                  {r.milestone} (skipped)
                </Badge>
              ) : (
                <span className="text-muted-foreground">—</span>
              )}
            </TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  );
}

function GitHubPanel({ slug, identifier }: { slug: string; identifier: string }) {
  const [owner, setOwner] = useState("");
  const [repo, setRepo] = useState("");
  const [token, setToken] = useState("");
  const [stateFilter, setStateFilter] = useState<"open" | "closed" | "all">("open");
  const [max, setMax] = useState("100");
  const [preview, setPreview] = useState<GitHubImportPreview | null>(null);
  const [result, setResult] = useState<GitHubImportResult | null>(null);
  const [busy, setBusy] = useState<"preview" | "import" | null>(null);
  const [error, setError] = useState<string | null>(null);

  const formError = validateGitHubForm(owner, repo);
  const input = {
    owner: owner.trim(),
    repo: repo.trim(),
    token,
    state_filter: stateFilter,
    max: Math.max(1, parseInt(max, 10) || 100),
  };

  const doPreview = async () => {
    if (formError) return;
    setBusy("preview");
    setError(null);
    setResult(null);
    try {
      setPreview(await previewGitHubImport(slug, identifier, input));
    } catch (e) {
      setError(errMsg(e, "Preview failed"));
    } finally {
      setBusy(null);
    }
  };

  const doImport = async () => {
    if (formError) return;
    if (
      !window.confirm(
        `Import up to ${input.max} ${stateFilter} issues from ${input.owner}/${input.repo} into this project?`,
      )
    ) {
      return;
    }
    setBusy("import");
    setError(null);
    setPreview(null);
    try {
      const res = await runGitHubImport(slug, identifier, input);
      setResult(res);
    } catch (e) {
      setError(errMsg(e, "Import failed"));
    } finally {
      setBusy(null);
      setToken(""); // the PAT never lingers in the form after a run
    }
  };

  return (
    <div className="space-y-4">
      <div className="grid gap-4 md:grid-cols-2">
        <div className="space-y-2">
          <div className="grid grid-cols-2 gap-2">
            <div className="space-y-1">
              <Label htmlFor="gh-owner">Owner *</Label>
              <Input
                id="gh-owner"
                placeholder="octocat"
                value={owner}
                onChange={(e) => setOwner(e.target.value)}
              />
            </div>
            <div className="space-y-1">
              <Label htmlFor="gh-repo">Repo *</Label>
              <Input
                id="gh-repo"
                placeholder="hello-world"
                value={repo}
                onChange={(e) => setRepo(e.target.value)}
              />
            </div>
          </div>
          <div className="space-y-1">
            <Label htmlFor="gh-token">Personal access token</Label>
            <Input
              id="gh-token"
              type="password"
              placeholder="ghp_… (empty works for public repos)"
              value={token}
              autoComplete="off"
              onChange={(e) => setToken(e.target.value)}
            />
            <p className="text-xs text-muted-foreground">
              Sent to api.github.com only, never stored. Needs repo scope for
              private repositories.
            </p>
          </div>
        </div>
        <div className="space-y-2">
          <div className="grid grid-cols-2 gap-2">
            <div className="space-y-1">
              <Label htmlFor="gh-state">Issues</Label>
              <Select
                value={stateFilter}
                onValueChange={(v) => setStateFilter(v as "open" | "closed" | "all")}
              >
                <SelectTrigger id="gh-state">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="open">Open</SelectItem>
                  <SelectItem value="closed">Closed</SelectItem>
                  <SelectItem value="all">All</SelectItem>
                </SelectContent>
              </Select>
            </div>
            <div className="space-y-1">
              <Label htmlFor="gh-max">Max issues</Label>
              <Input
                id="gh-max"
                type="number"
                min={1}
                max={1000}
                value={max}
                onChange={(e) => setMax(e.target.value)}
              />
            </div>
          </div>
          <p className="text-xs text-muted-foreground">
            Pull requests are excluded. Labels are created when missing.
            Comments are imported with attribution. Assignees are matched
            best-effort by public email — misses are reported, never fatal.
            Milestones are skipped (glance has none). Re-running is safe:
            already-imported issues are skipped.
          </p>
        </div>
      </div>

      {formError && (
        <Alert>
          <AlertDescription>{formError}</AlertDescription>
        </Alert>
      )}

      <div className="flex gap-2">
        <Button onClick={doPreview} disabled={!!formError || busy !== null} variant="outline">
          {busy === "preview" ? <Spinner className="mr-2" /> : null}Preview
        </Button>
        <Button onClick={doImport} disabled={!!formError || busy !== null}>
          {busy === "import" ? <Spinner className="mr-2" /> : null}Import
        </Button>
      </div>

      {error && (
        <Alert variant="destructive">
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}

      {preview && (
        <Card>
          <CardHeader>
            <CardTitle className="text-sm">
              Preview — {preview.rows.length} of {preview.total} issues from{" "}
              {preview.source}
            </CardTitle>
          </CardHeader>
          <CardContent className="overflow-x-auto">
            <GitHubPreviewTable preview={preview} />
          </CardContent>
        </Card>
      )}

      {result && (
        <Card>
          <CardHeader>
            <CardTitle className="flex items-center gap-2 text-sm">
              <CheckCircle2 className="h-4 w-4 text-green-600" /> Import result —{" "}
              {result.source}
            </CardTitle>
          </CardHeader>
          <CardContent className="space-y-4">
            <div className="grid grid-cols-2 gap-2 sm:grid-cols-4">
              <Stat label="Created" value={result.created} />
              <Stat label="Skipped (already imported)" value={result.skipped} />
              <Stat label="Pull requests excluded" value={result.prs_skipped} />
              <Stat label="Failed" value={result.failed} />
              <Stat
                label="Labels created"
                value={
                  result.labels_created.length > 0 ? (
                    <span title={result.labels_created.join(", ")}>
                      {result.labels_created.length}
                    </span>
                  ) : (
                    0
                  )
                }
              />
              <Stat label="Assignee misses" value={result.assignee_misses} />
              <Stat label="Milestones skipped" value={result.milestones_skipped} />
            </div>
            {result.labels_created.length > 0 && (
              <div className="flex flex-wrap gap-1">
                {result.labels_created.map((l) => (
                  <Badge key={l} variant="outline">
                    {l}
                  </Badge>
                ))}
              </div>
            )}
            <RowErrors errors={result.errors} label="issue" />
          </CardContent>
        </Card>
      )}
    </div>
  );
}

// ---------- Jira panel ----------

const JIRA_PRIORITY_NAMES = ["none", "low", "medium", "high", "urgent"];

function JiraPreviewTable({ preview }: { preview: JiraImportPreview }) {
  return (
    <Table>
      <TableHeader>
        <TableRow>
          <TableHead>Key</TableHead>
          <TableHead>Title</TableHead>
          <TableHead>Status → State</TableHead>
          <TableHead>Priority</TableHead>
          <TableHead>Labels</TableHead>
          <TableHead>Assignee</TableHead>
          <TableHead>Comments</TableHead>
          <TableHead>Type</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {preview.rows.map((r: JiraImportPreviewRow) => (
          <TableRow key={r.key} className={r.already_imported ? "opacity-60" : undefined}>
            <TableCell className="font-mono">{r.key}</TableCell>
            <TableCell className="max-w-64">
              <span className="block truncate" title={r.title}>
                {r.title}
              </span>
              {r.already_imported && (
                <Badge variant="secondary" className="mt-1">
                  already imported
                </Badge>
              )}
            </TableCell>
            <TableCell>
              <div className="flex items-center gap-1">
                <Badge variant="outline" title={`Jira status: ${r.status}`}>
                  {r.status}
                </Badge>
                <span className="text-muted-foreground">→</span>
                <Badge
                  variant={r.state_is_new ? "default" : "outline"}
                  title={r.state_is_new ? "Will be created as a new state" : "Existing state"}
                >
                  {r.state}
                  {r.state_is_new ? " +" : ""}
                </Badge>
              </div>
            </TableCell>
            <TableCell>
              <Badge variant="outline">{JIRA_PRIORITY_NAMES[r.priority] ?? r.priority}</Badge>
            </TableCell>
            <TableCell>
              <div className="flex max-w-48 flex-wrap gap-1">
                {r.labels.map((l) => (
                  <Badge
                    key={l}
                    variant={r.new_labels.includes(l) ? "default" : "outline"}
                    title={r.new_labels.includes(l) ? "Will be created" : "Exists"}
                  >
                    {l}
                    {r.new_labels.includes(l) ? " +" : ""}
                  </Badge>
                ))}
                {r.labels.length === 0 && <span className="text-muted-foreground">—</span>}
              </div>
            </TableCell>
            <TableCell>
              {r.assignee ? (
                <div>
                  <Badge variant="outline" title={r.assignee}>
                    {r.assignee}
                  </Badge>
                  <div className="mt-1 text-xs text-muted-foreground">
                    {r.assignee_matched ? "matched a member" : "no member match"}
                  </div>
                </div>
              ) : (
                <span className="text-muted-foreground">—</span>
              )}
            </TableCell>
            <TableCell className="font-mono">{r.comments}</TableCell>
            <TableCell>
              <Badge variant="outline">{r.issue_type || "—"}</Badge>
            </TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  );
}

function JiraPanel({ slug, identifier }: { slug: string; identifier: string }) {
  const [site, setSite] = useState("");
  const [email, setEmail] = useState("");
  const [apiToken, setApiToken] = useState("");
  const [projectKey, setProjectKey] = useState("");
  const [max, setMax] = useState("100");
  const [preview, setPreview] = useState<JiraImportPreview | null>(null);
  const [result, setResult] = useState<JiraImportResult | null>(null);
  const [busy, setBusy] = useState<"preview" | "import" | null>(null);
  const [error, setError] = useState<string | null>(null);

  const formError = validateJiraForm(site, email, projectKey);
  const input = {
    site: site.trim().toLowerCase(),
    email: email.trim(),
    api_token: apiToken,
    project_key: projectKey.trim().toUpperCase(),
    max: Math.max(1, parseInt(max, 10) || 100),
  };

  const doPreview = async () => {
    if (formError) return;
    setBusy("preview");
    setError(null);
    setResult(null);
    try {
      setPreview(await previewJiraImport(slug, identifier, input));
    } catch (e) {
      setError(errMsg(e, "Preview failed"));
    } finally {
      setBusy(null);
    }
  };

  const doImport = async () => {
    if (formError) return;
    if (
      !window.confirm(
        `Import up to ${input.max} issues from ${input.project_key} (${input.site}.atlassian.net) into this project?`,
      )
    ) {
      return;
    }
    setBusy("import");
    setError(null);
    setPreview(null);
    try {
      const res = await runJiraImport(slug, identifier, input);
      setResult(res);
    } catch (e) {
      setError(errMsg(e, "Import failed"));
    } finally {
      setBusy(null);
      setApiToken(""); // the API token never lingers in the form after a run
    }
  };

  return (
    <div className="space-y-4">
      <div className="grid gap-4 md:grid-cols-2">
        <div className="space-y-2">
          <div className="space-y-1">
            <Label htmlFor="jira-site">Site *</Label>
            <div className="flex items-center gap-0">
              <Input
                id="jira-site"
                placeholder="acme"
                value={site}
                onChange={(e) => setSite(e.target.value)}
                className="rounded-r-none"
              />
              <span className="rounded-r-md border border-l-0 bg-muted px-3 py-2 text-sm text-muted-foreground">
                .atlassian.net
              </span>
            </div>
            <p className="text-xs text-muted-foreground">
              Subdomain only. Jira Cloud only — Server/Data Center are not
              supported.
            </p>
          </div>
          <div className="space-y-1">
            <Label htmlFor="jira-email">Email *</Label>
            <Input
              id="jira-email"
              type="email"
              placeholder="you@company.com"
              value={email}
              autoComplete="off"
              onChange={(e) => setEmail(e.target.value)}
            />
          </div>
          <div className="space-y-1">
            <Label htmlFor="jira-token">API token *</Label>
            <Input
              id="jira-token"
              type="password"
              placeholder="ATATT3x… (from id.atlassian.com)"
              value={apiToken}
              autoComplete="off"
              onChange={(e) => setApiToken(e.target.value)}
            />
            <p className="text-xs text-muted-foreground">
              Sent to {site.trim() ? `${site.trim().toLowerCase()}.atlassian.net` : "your site"} only,
              never stored.
            </p>
          </div>
        </div>
        <div className="space-y-2">
          <div className="grid grid-cols-2 gap-2">
            <div className="space-y-1">
              <Label htmlFor="jira-key">Project key *</Label>
              <Input
                id="jira-key"
                placeholder="PROJ"
                value={projectKey}
                onChange={(e) => setProjectKey(e.target.value.toUpperCase())}
              />
            </div>
            <div className="space-y-1">
              <Label htmlFor="jira-max">Max issues</Label>
              <Input
                id="jira-max"
                type="number"
                min={1}
                max={1000}
                value={max}
                onChange={(e) => setMax(e.target.value)}
              />
            </div>
          </div>
          <p className="text-xs text-muted-foreground">
            Statuses are matched to states by name; unmatched statuses
            create new states. Labels are created when missing. Comments
            are imported with attribution. Assignees are matched
            best-effort by email — misses are reported, never fatal.
            Sprints are skipped (glance cycles are manual). Subtasks and
            epics import as flat issues. Re-running is safe:
            already-imported issues are skipped.
          </p>
        </div>
      </div>

      {formError && (
        <Alert>
          <AlertDescription>{formError}</AlertDescription>
        </Alert>
      )}

      <div className="flex gap-2">
        <Button onClick={doPreview} disabled={!!formError || busy !== null} variant="outline">
          {busy === "preview" ? <Spinner className="mr-2" /> : null}Preview
        </Button>
        <Button
          onClick={doImport}
          disabled={!!formError || busy !== null || !apiToken}
        >
          {busy === "import" ? <Spinner className="mr-2" /> : null}Import
        </Button>
      </div>

      {error && (
        <Alert variant="destructive">
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}

      {preview && (
        <Card>
          <CardHeader>
            <CardTitle className="text-sm">
              Preview — {preview.rows.length} of {preview.total} issues from{" "}
              {preview.source}
            </CardTitle>
          </CardHeader>
          <CardContent className="overflow-x-auto">
            <JiraPreviewTable preview={preview} />
          </CardContent>
        </Card>
      )}

      {result && (
        <Card>
          <CardHeader>
            <CardTitle className="flex items-center gap-2 text-sm">
              <CheckCircle2 className="h-4 w-4 text-green-600" /> Import result —{" "}
              {result.source}
            </CardTitle>
          </CardHeader>
          <CardContent className="space-y-4">
            <div className="grid grid-cols-2 gap-2 sm:grid-cols-4">
              <Stat label="Created" value={result.created} />
              <Stat label="Skipped (already imported)" value={result.skipped} />
              <Stat label="Failed" value={result.failed} />
              <Stat
                label="Labels created"
                value={
                  result.labels_created.length > 0 ? (
                    <span title={result.labels_created.join(", ")}>
                      {result.labels_created.length}
                    </span>
                  ) : (
                    0
                  )
                }
              />
              <Stat
                label="States created"
                value={
                  result.states_created.length > 0 ? (
                    <span title={result.states_created.join(", ")}>
                      {result.states_created.length}
                    </span>
                  ) : (
                    0
                  )
                }
              />
              <Stat label="Assignee misses" value={result.assignee_misses} />
            </div>
            {result.states_created.length > 0 && (
              <div>
                <p className="mb-1 text-xs text-muted-foreground">New states:</p>
                <div className="flex flex-wrap gap-1">
                  {result.states_created.map((s) => (
                    <Badge key={s} variant="outline">
                      {s}
                    </Badge>
                  ))}
                </div>
              </div>
            )}
            {result.labels_created.length > 0 && (
              <div>
                <p className="mb-1 text-xs text-muted-foreground">New labels:</p>
                <div className="flex flex-wrap gap-1">
                  {result.labels_created.map((l) => (
                    <Badge key={l} variant="outline">
                      {l}
                    </Badge>
                  ))}
                </div>
              </div>
            )}
            <RowErrors errors={result.errors} label="issue" />
          </CardContent>
        </Card>
      )}
    </div>
  );
}

// ---------- section ----------

export default function ImportSection({
  slug,
  identifier,
  canEdit,
}: {
  slug: string;
  identifier: string;
  canEdit: boolean;
}) {
  const [source, setSource] = useState<Source>("github");

  if (!canEdit) {
    return (
      <Alert>
        <AlertDescription>
          Importing issues requires the member role or higher.
        </AlertDescription>
      </Alert>
    );
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-base">Import issues</CardTitle>
        <div className="flex gap-2 pt-2">
          <Button
            variant={source === "jira" ? "default" : "outline"}
            size="sm"
            onClick={() => setSource("jira")}
          >
            <Cloud className="mr-2 h-4 w-4" /> Jira
          </Button>
          <Button
            variant={source === "github" ? "default" : "outline"}
            size="sm"
            onClick={() => setSource("github")}
          >
            <GitBranch className="mr-2 h-4 w-4" /> GitHub
          </Button>
          <Button
            variant={source === "csv" ? "default" : "outline"}
            size="sm"
            onClick={() => setSource("csv")}
          >
            <FileUp className="mr-2 h-4 w-4" /> CSV file
          </Button>
        </div>
      </CardHeader>
      <CardContent>
        {source === "jira" ? (
          <JiraPanel slug={slug} identifier={identifier} />
        ) : source === "github" ? (
          <GitHubPanel slug={slug} identifier={identifier} />
        ) : (
          <CsvPanel slug={slug} identifier={identifier} />
        )}
      </CardContent>
    </Card>
  );
}
