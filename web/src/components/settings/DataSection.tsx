// Data export + archive import section for workspace settings (C10T3,
// C11T0).
//
// Admin-only: the parent renders this only when the viewer is a workspace
// admin (role 20) — the backend rejects members/guests with 403, so there
// is no read-only mode to render. Matches the settings page's Slack /
// danger-zone gating pattern.
//
// Export contract: GET /api/v1/workspaces/{slug}/export streams a single
// JSON archive (format "glance-export/1") as an attachment download. The
// download reuses the shared downloadExport helper from lib/export.ts
// (cookie auth, filename from Content-Disposition, spec §5 error envelope
// surfaced on failure).
//
// Import contract: POST /api/v1/workspaces/{slug}/import with
// multipart/form-data ("file" = the archive JSON) restores a
// glance-export/1 archive into THIS workspace and returns an honest
// report {format, source_workspace, target_workspace, imported, skipped}.
// Re-import is safe: rows whose UUID already exists are skipped, never
// duplicated.
//
// Honest scope note: the archive is data portability, not a full backup —
// attachment files themselves are NOT included, only their metadata
// (filename, type, size), so imports cannot restore them (they are
// counted as skipped in the report). Members are matched to instance
// users by email and are never auto-created; unmapped members are
// skipped and reported, and their assignee links become unassigned.

import { useRef, useState } from "react";
import { CheckCircle2, Database, Download, Upload } from "lucide-react";
import { downloadExport } from "../../lib/export";
import {
  runArchiveImport,
  type ArchiveImportReport,
} from "../../lib/import";
import { ApiError } from "../../lib/api";
import { Button } from "../ui/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "../ui/card";
import { Alert, AlertDescription } from "../ui/alert";
import { Spinner } from "../ui/spinner";
import { toast } from "../ui/toast";

// Section labels for the imported-counters grid, in a readable order.
const IMPORT_LABELS: Array<[string, string]> = [
  ["projects", "Projects"],
  ["issues", "Issues"],
  ["comments", "Comments"],
  ["states", "States"],
  ["labels", "Labels"],
  ["estimates", "Estimates"],
  ["estimate_points", "Estimate points"],
  ["custom_fields", "Custom fields"],
  ["cycles", "Cycles"],
  ["cycle_issues", "Cycle memberships"],
  ["modules", "Modules"],
  ["module_issues", "Module memberships"],
  ["pages", "Pages"],
  ["issue_assignees", "Assignees"],
  ["issue_labels", "Issue labels"],
  ["issue_custom_values", "Custom values"],
  ["members", "Members added"],
];

function ImportResultSummary({ report }: { report: ArchiveImportReport }) {
  const entries = IMPORT_LABELS.filter(
    ([key]) => (report.imported[key] ?? 0) > 0,
  );
  const totalImported = Object.values(report.imported).reduce(
    (a, b) => a + b,
    0,
  );
  return (
    <div className="space-y-3 rounded-md border p-3">
      <p className="flex items-center gap-2 text-sm font-medium">
        <CheckCircle2 className="h-4 w-4 text-green-600" />
        Import complete — {totalImported} rows restored
        {report.skipped.count > 0 &&
          `, ${report.skipped.count} skipped`}
      </p>
      {entries.length > 0 && (
        <div className="grid grid-cols-2 gap-2 sm:grid-cols-3">
          {entries.map(([key, label]) => (
            <div
              key={key}
              className="rounded-md border px-3 py-2"
            >
              <div className="text-lg font-semibold">
                {report.imported[key]}
              </div>
              <div className="text-xs text-muted-foreground">{label}</div>
            </div>
          ))}
        </div>
      )}
      {report.skipped.count > 0 && (
        <div>
          <p className="text-sm font-medium">
            Skipped ({report.skipped.count})
          </p>
          <ul className="mt-1 max-h-40 space-y-1 overflow-y-auto text-xs text-muted-foreground">
            {report.skipped.reasons.map((r, i) => (
              <li key={i} className="font-mono">
                {r}
              </li>
            ))}
          </ul>
        </div>
      )}
    </div>
  );
}

export default function DataSection({ slug }: { slug: string }) {
  const [exporting, setExporting] = useState(false);
  const [importing, setImporting] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [report, setReport] = useState<ArchiveImportReport | null>(null);
  const fileRef = useRef<HTMLInputElement>(null);

  async function onExport() {
    setError(null);
    setExporting(true);
    try {
      await downloadExport(
        `/api/v1/workspaces/${encodeURIComponent(slug)}/export`,
      );
      toast.add({
        title: "Export downloaded",
        description: "Workspace data archive saved as JSON.",
        type: "success",
      });
    } catch (e) {
      const msg = e instanceof Error ? e.message : "Export failed";
      setError(msg);
      toast.add({ title: "Export failed", description: msg, type: "error" });
    } finally {
      setExporting(false);
    }
  }

  function onPickFile() {
    setError(null);
    setReport(null);
    fileRef.current?.click();
  }

  async function onFileChosen(files: FileList | null) {
    const file = files?.[0];
    if (!file) return;
    // Reset the input so picking the same file twice re-fires change.
    if (fileRef.current) fileRef.current.value = "";
    setError(null);
    setReport(null);
    setImporting(true);
    try {
      const res = await runArchiveImport(slug, file);
      setReport(res);
      toast.add({
        title: "Import complete",
        description: `${Object.values(res.imported).reduce((a, b) => a + b, 0)} rows restored${res.skipped.count > 0 ? `, ${res.skipped.count} skipped` : ""}.`,
        type: "success",
      });
    } catch (e) {
      const msg =
        e instanceof ApiError ? e.message : "Import failed";
      setError(msg);
      toast.add({ title: "Import failed", description: msg, type: "error" });
    } finally {
      setImporting(false);
    }
  }

  return (
    <Card className="mb-4">
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          <Database className="h-4 w-4" />
          Data
        </CardTitle>
        <CardDescription>
          Download a full archive of this workspace&apos;s data, or restore
          one from an archive file.
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-6">
        <div className="space-y-4">
          <p className="text-sm text-muted-foreground">
            Exports everything into a single JSON archive (format{" "}
            <span className="font-mono text-xs">glance-export/1</span>):
            projects and their issues — with comments, custom field values,
            assignees and labels — plus states, labels, cycles, modules and
            pages. Attachment files themselves are{" "}
            <span className="font-medium">not</span> included, only their
            metadata (filename, type, size).
          </p>
          <Button onClick={onExport} disabled={exporting || importing}>
            <Download className="h-4 w-4" />
            {exporting ? "Exporting…" : "Export workspace data"}
          </Button>
        </div>

        <div className="space-y-4 border-t pt-4">
          <p className="text-sm text-muted-foreground">
            Restores a <span className="font-mono text-xs">glance-export/1</span>{" "}
            archive into <span className="font-medium">this workspace</span>.
            Members are matched to existing users by email — they are never
            auto-created, and unmapped members are skipped (their assignee
            links become unassigned). Attachment binaries are not part of
            the archive and cannot be restored; they are counted as skipped.
            Re-importing the same archive is safe: rows that already exist
            are skipped, never duplicated.
          </p>
          <input
            ref={fileRef}
            type="file"
            accept="application/json,.json"
            className="hidden"
            onChange={(e) => void onFileChosen(e.target.files)}
          />
          <Button
            variant="secondary"
            onClick={onPickFile}
            disabled={exporting || importing}
          >
            {importing ? (
              <Spinner className="h-4 w-4" />
            ) : (
              <Upload className="h-4 w-4" />
            )}
            {importing ? "Importing…" : "Import archive"}
          </Button>
          {report && <ImportResultSummary report={report} />}
        </div>

        {error && (
          <Alert variant="destructive">
            <AlertDescription>{error}</AlertDescription>
          </Alert>
        )}
      </CardContent>
    </Card>
  );
}
