import { useEffect, useState } from "react";
import { CopyPlus, Loader2 } from "lucide-react";
import type { Issue } from "../../lib/types";
import type { SimilarIssue } from "../../lib/similar";
import { Alert, AlertDescription } from "../ui/alert";
import { Button } from "../ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "../ui/dialog";
import { Input } from "../ui/input";
import { useSimilarIssues } from "./useSimilarIssues";
import { cn } from "cn";

/**
 * Mark-as-duplicate dialog (C25T3). The target picker is prefilled with
 * the C8T7 similar-issues list for the issue's own title (the same
 * useSimilarIssues hook the create modal uses); a search field lets the
 * user refine the query. The issue itself is excluded from the list.
 * The caller owns the confirm call, toast and refetch.
 */
export default function MergeDuplicateDialog({
  open,
  onOpenChange,
  slug,
  identifier,
  issue,
  onConfirm,
  confirming,
  error,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  slug: string;
  identifier: string;
  issue: Issue;
  onConfirm: (target: SimilarIssue) => void;
  confirming: boolean;
  error: string | null;
}) {
  const [query, setQuery] = useState(issue.name);
  const [picked, setPicked] = useState<SimilarIssue | null>(null);

  // Reset the query + pick every time the dialog opens, prefilled with
  // the issue's own title.
  useEffect(() => {
    if (open) {
      setQuery(issue.name);
      setPicked(null);
    }
  }, [open, issue.name]);

  const { suggestions, loading } = useSimilarIssues(
    slug,
    identifier,
    query,
    open,
  );
  const candidates = suggestions.filter((s) => s.id !== issue.id);

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-[440px]">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            <CopyPlus className="h-4 w-4" />
            Mark as duplicate
          </DialogTitle>
          <DialogDescription>
            <span className="font-mono font-medium text-foreground">
              {issue.display_id}
            </span>{" "}
            will be closed as a duplicate and its comments, attachments,
            links, reactions, votes, time entries and followers moved to
            the issue you pick below.
          </DialogDescription>
        </DialogHeader>
        <div className="space-y-2 py-2">
          <Input
            value={query}
            onChange={(e) => {
              setQuery(e.target.value);
              // A new query invalidates the previous pick.
              setPicked(null);
            }}
            placeholder="Search issues by title…"
            disabled={confirming}
            aria-label="Search candidate issues"
          />
          <div
            className="max-h-[240px] overflow-y-auto rounded-md border border-border"
            role="radiogroup"
            aria-label="Duplicate target"
          >
            {loading ? (
              <div className="flex items-center gap-2 px-3 py-4 text-xs text-muted-foreground">
                <Loader2 className="h-3.5 w-3.5 animate-spin" aria-hidden />
                Finding similar issues…
              </div>
            ) : candidates.length === 0 ? (
              <div className="px-3 py-4 text-xs text-muted-foreground">
                No similar issues found. Refine the search above.
              </div>
            ) : (
              <ul className="divide-y divide-border">
                {candidates.map((s) => {
                  const selected = picked?.id === s.id;
                  return (
                    <li key={s.id}>
                      <button
                        type="button"
                        role="radio"
                        aria-checked={selected}
                        onClick={() => setPicked(s)}
                        disabled={confirming}
                        className={cn(
                          "flex w-full items-center gap-2 px-3 py-2 text-left text-xs transition-colors hover:bg-muted/70 disabled:opacity-50",
                          selected && "bg-muted/70",
                        )}
                      >
                        <span
                          className={cn(
                            "flex h-3.5 w-3.5 shrink-0 items-center justify-center rounded-full border",
                            selected
                              ? "border-primary"
                              : "border-muted-foreground/40",
                          )}
                          aria-hidden
                        >
                          {selected && (
                            <span className="h-1.5 w-1.5 rounded-full bg-primary" />
                          )}
                        </span>
                        <span className="shrink-0 font-mono text-muted-foreground">
                          {s.display_id}
                        </span>
                        <span className="min-w-0 flex-1 truncate text-foreground">
                          {s.name}
                        </span>
                        <span className="shrink-0 text-muted-foreground">
                          {s.state}
                        </span>
                      </button>
                    </li>
                  );
                })}
              </ul>
            )}
          </div>
        </div>
        {error && (
          <Alert variant="destructive">
            <AlertDescription>{error}</AlertDescription>
          </Alert>
        )}
        <DialogFooter>
          <Button
            variant="outline"
            onClick={() => onOpenChange(false)}
            disabled={confirming}
          >
            Cancel
          </Button>
          <Button
            onClick={() => picked && onConfirm(picked)}
            disabled={!picked || confirming}
          >
            {confirming
              ? "Merging…"
              : picked
                ? `Merge into ${picked.display_id}`
                : "Merge into…"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
