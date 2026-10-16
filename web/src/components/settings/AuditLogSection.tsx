// Workspace audit log section for workspace settings (C37T1).
//
// Admin-only: the parent renders this only when the viewer is a
// workspace admin (role 20) — the backend rejects members/guests with
// 403, so there is no read-only mode to render. Matches the settings
// page's Webhooks / danger-zone gating pattern.
//
// Contract (see lib/workspaceAudit.ts): list newest-first via
// GET /api/v1/workspaces/{slug}/audit-log (pageEnvelope), filters map
// onto the backend query params (action, actor, entity_type, since,
// until — dates as RFC 3339 UTC). Category groups the Action dropdown
// (the backend has no category param). Export CSV downloads
// audit-log-<slug>-<YYYY-MM-DD>.csv via the export endpoint, with the
// same applied filters. IP is NULL for all v1 rows — a null IP renders
// as "—", never "null" or empty.

import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Download, ScrollText } from "lucide-react";
import { ApiError } from "../../lib/api";
import { totalPages } from "../../lib/admin";
import { downloadExport } from "../../lib/export";
import {
  auditCategory,
  buildWorkspaceAuditExportUrl,
  fetchWorkspaceAuditLog,
  truncateAuditId,
  WORKSPACE_AUDIT_ACTIONS,
  WORKSPACE_AUDIT_ALL_ACTIONS,
  WORKSPACE_AUDIT_CATEGORIES,
  WORKSPACE_AUDIT_ENTITY_TYPES,
  WORKSPACE_AUDIT_PER_PAGE,
  type WorkspaceAuditEntry,
  type WorkspaceAuditFilters,
} from "../../lib/workspaceAudit";
import { formatDateTime } from "../../lib/format";
import { Badge } from "../ui/badge";
import { Button } from "../ui/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "../ui/card";
import { Input } from "../ui/input";
import { Label } from "../ui/label";
import { Skeleton } from "../ui/skeleton";
import { Alert, AlertDescription } from "../ui/alert";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "../ui/table";
import { toast } from "../ui/toast";

function queryError(e: unknown, fallback: string): string {
  return e instanceof ApiError ? e.message : fallback;
}

export default function AuditLogSection({ slug }: { slug: string }) {
  const [page, setPage] = useState(1);
  // Draft = what the inputs show; applied = what the query actually
  // uses. Filters only hit the server on Apply, so typing doesn't
  // refetch (the Admin audit tab's pattern).
  const [draft, setDraft] = useState<WorkspaceAuditFilters>({});
  const [applied, setApplied] = useState<WorkspaceAuditFilters>({});
  const [selected, setSelected] = useState<WorkspaceAuditEntry | null>(null);
  const [exporting, setExporting] = useState(false);
  const [exportError, setExportError] = useState<string | null>(null);

  const auditQuery = useQuery({
    queryKey: ["workspace-audit-log", slug, page, applied],
    queryFn: () => fetchWorkspaceAuditLog(slug, page, applied),
  });

  const rows = auditQuery.data?.items ?? [];
  const total = auditQuery.data?.total ?? 0;
  const pages = totalPages(total, WORKSPACE_AUDIT_PER_PAGE);

  const actionOptions = draft.category
    ? WORKSPACE_AUDIT_ACTIONS[draft.category]
    : WORKSPACE_AUDIT_ALL_ACTIONS;

  function applyFilters() {
    setApplied(draft);
    setPage(1);
    setSelected(null);
  }

  function clearFilters() {
    setDraft({});
    setApplied({});
    setPage(1);
    setSelected(null);
  }

  async function onExport() {
    setExportError(null);
    setExporting(true);
    try {
      await downloadExport(buildWorkspaceAuditExportUrl(slug, applied));
      toast.add({
        title: "Audit log exported",
        description: "CSV download started.",
        type: "success",
      });
    } catch (e) {
      const msg = e instanceof Error ? e.message : "Export failed";
      setExportError(msg);
      toast.add({ title: "Export failed", description: msg, type: "error" });
    } finally {
      setExporting(false);
    }
  }

  function onRowKeyDown(e: React.KeyboardEvent, entry: WorkspaceAuditEntry) {
    if (e.key === "Enter" || e.key === " ") {
      e.preventDefault();
      setSelected(entry);
    }
  }

  return (
    <Card className="mb-4 rounded-lg border-border-hairline">
      <CardHeader>
        <div className="flex items-center justify-between gap-3">
          <div>
            <CardTitle className="flex items-center gap-2 text-xs font-semibold">
              <ScrollText className="h-4 w-4" />
              Audit log
            </CardTitle>
            <CardDescription className="text-xs">
              Control-plane events only — membership, project lifecycle,
              workspace settings, integrations. Newest first. Issue
              activity lives in the activity feed, not here.
            </CardDescription>
          </div>
          <Button
            size="sm"
            variant="outline"
            onClick={onExport}
            disabled={exporting}
          >
            <Download className="h-4 w-4" />
            {exporting ? "Exporting…" : "Export CSV"}
          </Button>
        </div>
      </CardHeader>
      <CardContent>
        {/* ---------- Filters ---------- */}
        <div className="mb-4 grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
          <div className="space-y-1.5">
            <Label htmlFor="audit-category">Category</Label>
            <select
              id="audit-category"
              aria-label="Category"
              value={draft.category ?? ""}
              onChange={(e) =>
                setDraft({
                  ...draft,
                  category: (e.target.value || undefined) as
                    | WorkspaceAuditFilters["category"]
                    | undefined,
                  // A category switch resets the action: the previous
                  // action may not belong to the new category.
                  action: undefined,
                })
              }
              className="w-full rounded-md border border-border bg-[#111113] px-2 py-1.5 text-sm"
            >
              <option value="">All categories</option>
              {WORKSPACE_AUDIT_CATEGORIES.map((c) => (
                <option key={c} value={c}>
                  {c}
                </option>
              ))}
            </select>
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="audit-action">Action</Label>
            <select
              id="audit-action"
              aria-label="Action"
              value={draft.action ?? ""}
              onChange={(e) =>
                setDraft({ ...draft, action: e.target.value || undefined })
              }
              className="w-full rounded-md border border-border bg-[#111113] px-2 py-1.5 font-mono text-sm"
            >
              <option value="">
                {draft.category
                  ? `All ${draft.category} actions`
                  : "All actions"}
              </option>
              {actionOptions.map((a) => (
                <option key={a} value={a}>
                  {a}
                </option>
              ))}
            </select>
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="audit-actor">Actor email</Label>
            <Input
              id="audit-actor"
              aria-label="Actor email"
              placeholder="Actor email…"
              value={draft.actor ?? ""}
              onChange={(e) =>
                setDraft({ ...draft, actor: e.target.value || undefined })
              }
              autoComplete="off"
            />
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="audit-entity-type">Entity type</Label>
            <select
              id="audit-entity-type"
              aria-label="Entity type"
              value={draft.entityType ?? ""}
              onChange={(e) =>
                setDraft({
                  ...draft,
                  entityType: e.target.value || undefined,
                })
              }
              className="w-full rounded-md border border-border bg-[#111113] px-2 py-1.5 text-sm"
            >
              <option value="">All entities</option>
              {WORKSPACE_AUDIT_ENTITY_TYPES.map((t) => (
                <option key={t} value={t}>
                  {t}
                </option>
              ))}
            </select>
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="audit-since">Since</Label>
            <Input
              id="audit-since"
              aria-label="Since"
              type="datetime-local"
              value={draft.since ?? ""}
              onChange={(e) =>
                setDraft({ ...draft, since: e.target.value || undefined })
              }
            />
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="audit-until">Until</Label>
            <Input
              id="audit-until"
              aria-label="Until"
              type="datetime-local"
              value={draft.until ?? ""}
              onChange={(e) =>
                setDraft({ ...draft, until: e.target.value || undefined })
              }
            />
          </div>
        </div>
        <div className="mb-4 flex items-center gap-2">
          <Button size="sm" onClick={applyFilters}>
            Apply
          </Button>
          <Button size="sm" variant="ghost" onClick={clearFilters}>
            Clear
          </Button>
          <p className="text-xs text-muted-foreground">
            Category narrows the action list; the server filters by the
            chosen action.
          </p>
        </div>

        {/* ---------- List ---------- */}
        {auditQuery.isError && (
          <Alert variant="destructive" className="mb-3">
            <AlertDescription>
              {queryError(auditQuery.error, "Failed to load audit log")}
            </AlertDescription>
          </Alert>
        )}
        {auditQuery.isError && (
          <Button
            size="sm"
            variant="outline"
            onClick={() => void auditQuery.refetch()}
          >
            Retry
          </Button>
        )}
        {exportError && (
          <Alert variant="destructive" className="mb-3">
            <AlertDescription>{exportError}</AlertDescription>
          </Alert>
        )}
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>When</TableHead>
              <TableHead>Actor</TableHead>
              <TableHead>Action</TableHead>
              <TableHead>Entity</TableHead>
              <TableHead>IP</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {auditQuery.isLoading &&
              Array.from({ length: 5 }).map((_, i) => (
                <TableRow key={i}>
                  <TableCell colSpan={5}>
                    <Skeleton className="h-5 w-full" />
                  </TableCell>
                </TableRow>
              ))}
            {!auditQuery.isLoading &&
              rows.map((entry) => (
                <TableRow
                  key={entry.id}
                  className="cursor-pointer hover:bg-wash-hover"
                  tabIndex={0}
                  role="button"
                  aria-label={`View audit entry ${entry.action}`}
                  onClick={() => setSelected(entry)}
                  onKeyDown={(e) => onRowKeyDown(e, entry)}
                >
                  <TableCell
                    className="whitespace-nowrap text-sm text-muted-foreground"
                    title={entry.at}
                  >
                    {formatDateTime(entry.at)}
                  </TableCell>
                  <TableCell>
                    <div className="font-medium">
                      {entry.actor_email ?? "—"}
                    </div>
                    {entry.actor_id && (
                      <div
                        className="font-mono text-xs text-muted-foreground"
                        title={entry.actor_id}
                      >
                        {truncateAuditId(entry.actor_id)}
                      </div>
                    )}
                  </TableCell>
                  <TableCell>
                    <span className="flex flex-wrap items-center gap-1.5">
                      <Badge variant="secondary">
                        {auditCategory(entry.action)}
                      </Badge>
                      <span className="font-mono text-xs">
                        {entry.action}
                      </span>
                    </span>
                  </TableCell>
                  <TableCell>
                    <div className="font-mono text-xs">
                      {entry.entity_type}
                    </div>
                    <div
                      className="font-mono text-xs text-muted-foreground"
                      title={entry.entity_id}
                    >
                      {truncateAuditId(entry.entity_id)}
                    </div>
                  </TableCell>
                  <TableCell className="font-mono text-xs text-muted-foreground">
                    {entry.ip ?? "—"}
                  </TableCell>
                </TableRow>
              ))}
          </TableBody>
        </Table>
        {!auditQuery.isLoading &&
          !auditQuery.isError &&
          rows.length === 0 && (
            <div className="mt-4 rounded-lg border border-dashed border-border-hairline p-8 text-center text-xs text-muted-foreground">
              No audit entries match these filters.
            </div>
          )}

        {/* ---------- Detail panel ---------- */}
        {selected && (
          <div
            role="dialog"
            aria-label="Audit entry detail"
            className="mt-4 space-y-3 rounded-lg border border-border p-4"
          >
            <div className="flex items-start justify-between gap-3">
              <div>
                <p className="font-mono text-sm font-semibold">
                  {selected.action}
                </p>
                <p className="text-xs text-muted-foreground">
                  {formatDateTime(selected.at)} · actor{" "}
                  {selected.actor_email ?? "—"} · IP {selected.ip ?? "—"}
                </p>
                <p className="mt-1 font-mono text-xs">
                  entity: {selected.entity_type} ·{" "}
                  {truncateAuditId(selected.entity_id)}
                </p>
              </div>
              <Button
                size="sm"
                variant="ghost"
                onClick={() => setSelected(null)}
              >
                Close
              </Button>
            </div>
            <div>
              <p className="mb-1 text-xs font-medium">Meta</p>
              <pre className="overflow-auto rounded-md border border-border bg-muted/30 p-3 font-mono text-xs">
                {JSON.stringify(selected.meta ?? {}, null, 2)}
              </pre>
            </div>
          </div>
        )}

        {/* ---------- Pagination ---------- */}
        <div className="mt-4 flex items-center justify-end gap-2">
          <span className="mr-1 text-xs text-muted-foreground">
            Page {page} of {pages} · {total} total
          </span>
          <Button
            variant="outline"
            size="sm"
            disabled={page <= 1}
            onClick={() => {
              setSelected(null);
              setPage(page - 1);
            }}
            aria-label="Previous page"
          >
            Previous
          </Button>
          <Button
            variant="outline"
            size="sm"
            disabled={page >= pages}
            onClick={() => {
              setSelected(null);
              setPage(page + 1);
            }}
            aria-label="Next page"
          >
            Next
          </Button>
        </div>
      </CardContent>
    </Card>
  );
}
