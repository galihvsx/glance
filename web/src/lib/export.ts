import { toApiParams, type IssueFilters } from "./filters";

export type ExportFormat = "csv" | "json";

/**
 * Build the issues-export download URL (C8T3). Reuses toApiParams so the
 * export honors exactly the filter state the list view shows — the same
 * query contract the list endpoint takes (dates as RFC3339, archived as
 * "1", multi-values comma-separated). The export endpoint ignores the
 * list-only pagination params, so they are not added here.
 */
export function buildExportUrl(
  slug: string,
  identifier: string,
  filters: IssueFilters,
  format: ExportFormat,
): string {
  const p = toApiParams(filters);
  p.set("format", format);
  return `/api/v1/workspaces/${encodeURIComponent(slug)}/projects/${encodeURIComponent(identifier)}/issues/export?${p}`;
}

/** Extract the filename from a Content-Disposition header, if present. */
export function exportFilenameFromHeader(
  contentDisposition: string | null,
): string | null {
  if (!contentDisposition) return null;
  const m = /filename="([^"]+)"/.exec(contentDisposition);
  return m ? m[1] : null;
}

/**
 * Fetch the export URL (cookie auth) and trigger a browser download.
 * On a non-2xx response the server's spec §5 error envelope message is
 * surfaced as the thrown Error's message.
 */
export async function downloadExport(url: string): Promise<void> {
  const res = await fetch(url, { credentials: "include" });
  if (!res.ok) {
    throw new Error(await exportErrorMessage(res));
  }
  const blob = await res.blob();
  const filename =
    exportFilenameFromHeader(res.headers.get("Content-Disposition")) ??
    "glance-issues-export";
  const objectUrl = URL.createObjectURL(blob);
  try {
    const a = document.createElement("a");
    a.href = objectUrl;
    a.download = filename;
    document.body.appendChild(a);
    a.click();
    a.remove();
  } finally {
    URL.revokeObjectURL(objectUrl);
  }
}

async function exportErrorMessage(res: Response): Promise<string> {
  const text = await res.text();
  try {
    const parsed: unknown = JSON.parse(text);
    if (typeof parsed === "object" && parsed !== null && "error" in parsed) {
      const err = (parsed as { error: unknown }).error;
      if (typeof err === "object" && err !== null && "message" in err) {
        const message = (err as { message: unknown }).message;
        if (typeof message === "string" && message.length > 0)
          return message;
      }
    }
  } catch {
    // Not JSON — fall through.
  }
  return res.statusText || `Export failed with status ${res.status}`;
}
