import { useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { ListTree, Loader2 } from "lucide-react";
import { api } from "../../lib/api";
import {
  breakDownIssue,
  isAINotConfigured,
  isAIProviderFailure,
  useAIAvailable,
  type AISubIssueSuggestion,
} from "../../lib/ai";
import type { Issue } from "../../lib/types";
import { AI_NOT_CONFIGURED_TOOLTIP } from "./DraftWithAI";
import { Button } from "../ui/button";
import { Checkbox } from "../ui/checkbox";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "../ui/dialog";
import { Input } from "../ui/input";
import { toast } from "../ui/toast";

/** One editable, checkable suggestion row in the breakdown dialog. */
interface Row extends AISubIssueSuggestion {
  key: number;
  checked: boolean;
}

/** Wrap plain text in a minimal TipTap doc for the issue create API
 *  (the create endpoint takes `description` as raw JSON, same as the
 *  editor emits). */
export function tiptapDoc(text: string): object {
  return {
    type: "doc",
    content: [
      {
        type: "paragraph",
        content: [{ type: "text", text }],
      },
    ],
  };
}

/**
 * "Break down" for the issue detail (C25T1), rendered next to the
 * Summarize button below the description block.
 *
 * - Clicking asks POST /api/v1/ai/subissues for up to 5 sub-task
 *   suggestions and opens them in a dialog: checkboxes (all checked by
 *   default), titles editable inline, description shown beneath.
 * - "Create N sub-issues" posts each selected suggestion to the
 *   EXISTING issue create endpoint with parent_id set (the same call
 *   SubIssues.tsx uses), then toasts the count and invalidates the
 *   issue detail + list caches.
 * - AI unconfigured → button DISABLED with the honest tooltip
 *   "AI not configured by administrator" (shared with DraftWithAI).
 * - Provider failure (502) → inline error in the dialog, no fake
 *   results.
 */
export default function IssueBreakdown({
  slug,
  identifier,
  issue,
}: {
  slug: string;
  identifier: string;
  issue: Pick<Issue, "id" | "name">;
}) {
  const queryClient = useQueryClient();
  const availability = useAIAvailable();
  const [open, setOpen] = useState(false);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [rows, setRows] = useState<Row[]>([]);

  const notConfigured = availability.data === false;
  const base = `/api/v1/workspaces/${encodeURIComponent(slug)}/projects/${encodeURIComponent(identifier)}`;

  const createMutation = useMutation({
    mutationFn: (row: Row) =>
      api.post<Issue>(`${base}/issues`, {
        name: row.title,
        parent_id: issue.id,
        ...(row.description?.trim() ? { description: tiptapDoc(row.description.trim()) } : {}),
      }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["issue", slug, identifier] });
      queryClient.invalidateQueries({ queryKey: ["issues", slug, identifier] });
    },
  });

  async function breakdown() {
    setError(null);
    setLoading(true);
    try {
      const suggestions = await breakDownIssue(slug, identifier, issue.id);
      if (suggestions.length === 0) {
        setError("The AI returned no sub-tasks for this issue.");
        return;
      }
      setRows(
        suggestions.map((s, i) => ({
          ...s,
          key: i,
          checked: true,
          title: s.title.trim(),
          description: s.description?.trim() ? s.description.trim() : null,
        })),
      );
      setOpen(true);
    } catch (e) {
      if (isAINotConfigured(e)) {
        // Flip every AI button on the page to the honest disabled state.
        queryClient.setQueryData(["ai-available"], false);
        setError("AI is not configured by the administrator.");
      } else if (isAIProviderFailure(e)) {
        setError("The AI provider failed. Try again in a moment.");
      } else {
        setError(e instanceof Error ? e.message : "Breakdown failed.");
      }
    } finally {
      setLoading(false);
    }
  }

  function toggle(key: number, checked: boolean) {
    setRows((prev) => prev.map((r) => (r.key === key ? { ...r, checked } : r)));
  }

  function rename(key: number, title: string) {
    setRows((prev) => prev.map((r) => (r.key === key ? { ...r, title } : r)));
  }

  async function createSelected() {
    const selected = rows.filter((r) => r.checked && r.title.trim());
    if (selected.length === 0 || createMutation.isPending) return;
    let created = 0;
    for (const row of selected) {
      try {
        await createMutation.mutateAsync(row);
        created++;
      } catch {
        // Keep going: partial success is reported, not hidden.
      }
    }
    if (created === 0) {
      setError("None of the sub-issues could be created.");
      return;
    }
    setOpen(false);
    toast.add({
      title:
        created === 1
          ? "Created 1 sub-issue"
          : `Created ${created} sub-issues`,
      ...(created < selected.length
        ? { description: `${selected.length - created} failed to create.` }
        : {}),
      type: created < selected.length ? "error" : "success",
    });
  }

  const selectedCount = rows.filter((r) => r.checked && r.title.trim()).length;

  const button = (
    <Button
      type="button"
      variant="outline"
      size="sm"
      className="gap-1.5"
      onClick={breakdown}
      disabled={loading || notConfigured}
      title={notConfigured ? AI_NOT_CONFIGURED_TOOLTIP : "Break this issue into sub-issues with AI"}
    >
      {loading ? (
        <Loader2 className="h-3.5 w-3.5 animate-spin" aria-hidden />
      ) : (
        <ListTree className="h-3.5 w-3.5" aria-hidden />
      )}
      {loading ? "Breaking down…" : "Break down"}
    </Button>
  );

  return (
    <>
      {/* Disabled buttons don't fire mouse events, so the tooltip needs
          a wrapper to stay reachable. */}
      {notConfigured ? <span title={AI_NOT_CONFIGURED_TOOLTIP}>{button}</span> : button}
      {error && (
        <p role="alert" className="mt-1 text-[11px] text-accent-red">
          {error}
        </p>
      )}
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent className="max-w-lg">
          <DialogHeader>
            <DialogTitle className="flex items-center gap-1.5">
              <ListTree className="h-4 w-4" aria-hidden />
              Break down “{issue.name}”
            </DialogTitle>
            <DialogDescription>
              Uncheck or edit anything before creating — each selected
              suggestion becomes a sub-issue of this issue.
            </DialogDescription>
          </DialogHeader>
          <ul className="-mx-2 max-h-[50vh] overflow-y-auto">
            {rows.map((r) => (
              <li
                key={r.key}
                className="flex items-start gap-2.5 rounded-md px-2 py-1.5 hover:bg-wash"
              >
                <Checkbox
                  checked={r.checked}
                  onCheckedChange={(c) => toggle(r.key, c === true)}
                  aria-label={`Include "${r.title}"`}
                  className="mt-1.5 shrink-0"
                />
                <div className="min-w-0 flex-1">
                  <Input
                    value={r.title}
                    onChange={(e) => rename(r.key, e.target.value)}
                    aria-label="Sub-issue title"
                    className="h-8 text-[12px]"
                  />
                  {r.description && (
                    <p className="mt-1 line-clamp-2 text-[11px] text-ink-muted">
                      {r.description}
                    </p>
                  )}
                </div>
              </li>
            ))}
          </ul>
          <DialogFooter>
            <Button type="button" variant="outline" size="sm" onClick={() => setOpen(false)}>
              Cancel
            </Button>
            <Button
              type="button"
              size="sm"
              onClick={createSelected}
              disabled={selectedCount === 0 || createMutation.isPending}
            >
              {createMutation.isPending
                ? "Creating…"
                : `Create ${selectedCount} sub-issue${selectedCount === 1 ? "" : "s"}`}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  );
}
