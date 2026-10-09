// Data export section for workspace settings (C10T3).
//
// Admin-only: the parent renders this only when the viewer is a workspace
// admin (role 20) — the backend rejects members/guests with 403, so there
// is no read-only mode to render. Matches the settings page's Slack /
// danger-zone gating pattern.
//
// Contract: GET /api/v1/workspaces/{slug}/export streams a single JSON
// archive (format "glance-export/1") as an attachment download. The
// download reuses the shared downloadExport helper from lib/export.ts
// (cookie auth, filename from Content-Disposition, spec §5 error envelope
// surfaced on failure).
//
// Honest scope note: the archive is data portability, not a full backup —
// attachment files themselves are NOT included, only their metadata
// (filename, type, size). There is no archive importer yet.

import { useState } from "react";
import { Database, Download } from "lucide-react";
import { downloadExport } from "../../lib/export";
import { Button } from "../ui/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "../ui/card";
import { Alert, AlertDescription } from "../ui/alert";
import { toast } from "../ui/toast";

export default function DataSection({ slug }: { slug: string }) {
  const [exporting, setExporting] = useState(false);
  const [error, setError] = useState<string | null>(null);

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

  return (
    <Card className="mb-4">
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          <Database className="h-4 w-4" />
          Data
        </CardTitle>
        <CardDescription>
          Download a full archive of this workspace&apos;s data.
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-4">
        <p className="text-sm text-muted-foreground">
          Exports everything into a single JSON archive (format{" "}
          <span className="font-mono text-xs">glance-export/1</span>):
          projects and their issues — with comments, custom field values,
          assignees and labels — plus states, labels, cycles, modules and
          pages. Attachment files themselves are{" "}
          <span className="font-medium">not</span> included, only their
          metadata (filename, type, size). There is no archive importer yet,
          so treat this as a portable copy of your data.
        </p>
        {error && (
          <Alert variant="destructive">
            <AlertDescription>{error}</AlertDescription>
          </Alert>
        )}
        <Button onClick={onExport} disabled={exporting}>
          <Download className="h-4 w-4" />
          {exporting ? "Exporting…" : "Export workspace data"}
        </Button>
      </CardContent>
    </Card>
  );
}
