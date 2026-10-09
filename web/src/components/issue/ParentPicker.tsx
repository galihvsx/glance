import { useEffect, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { ChevronsUpDown, X } from "lucide-react";
import { api } from "../../lib/api";
import type { Issue, IssueListResult } from "../../lib/types";
import { Badge } from "../ui/badge";
import { buttonVariants } from "../ui/button";
import { Input } from "../ui/input";
import { Popover, PopoverContent, PopoverTrigger } from "../ui/popover";
import { ScrollArea } from "../ui/scroll-area";
import { Skeleton } from "../ui/skeleton";
import { cn } from "cn";

const SEARCH_DEBOUNCE_MS = 250;
const SEARCH_PAGE_SIZE = 10;

/** Parent issue picker: searchable popover over the project's issues.
 *  Selecting one stamps `parent_id` on the new issue (sub-issue). The
 *  caller owns the create call; this component only picks. */
export default function ParentPicker({
  slug,
  identifier,
  value,
  onChange,
  disabled,
}: {
  slug: string;
  identifier: string;
  /** Currently selected parent (null = top-level issue). */
  value: Issue | null;
  onChange: (issue: Issue | null) => void;
  disabled?: boolean;
}) {
  const [open, setOpen] = useState(false);
  const [input, setInput] = useState("");
  const [debounced, setDebounced] = useState("");

  useEffect(() => {
    const t = setTimeout(() => setDebounced(input.trim()), SEARCH_DEBOUNCE_MS);
    return () => clearTimeout(t);
  }, [input]);

  const searchQuery = useQuery({
    // Only search when the popover is open — no wasted requests.
    queryKey: ["parent-search", slug, identifier, debounced],
    queryFn: () => {
      const p = new URLSearchParams();
      p.set("per_page", String(SEARCH_PAGE_SIZE));
      if (debounced) p.set("q", debounced);
      return api.get<IssueListResult>(
        `/api/v1/workspaces/${encodeURIComponent(slug)}/projects/${encodeURIComponent(identifier)}/issues?${p}`,
      );
    },
    enabled: open,
    staleTime: 30_000,
  });

  const results = searchQuery.data?.results ?? [];

  function pick(issue: Issue | null) {
    onChange(issue);
    setOpen(false);
  }

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger
        disabled={disabled}
        className={cn(
          buttonVariants({ variant: "outline", size: "sm" }),
          "w-full justify-between gap-2 font-normal",
        )}
      >
        {value ? (
          <span className="flex min-w-0 items-center gap-2">
            <Badge variant="outline" className="font-mono text-[11px] shrink-0">
              {value.display_id}
            </Badge>
            <span className="truncate">{value.name}</span>
          </span>
        ) : (
          <span className="text-muted-foreground">No parent (top-level)</span>
        )}
        <span className="flex shrink-0 items-center gap-1">
          {value && !disabled && (
            <span
              role="button"
              tabIndex={0}
              aria-label="Clear parent"
              className="rounded p-0.5 hover:bg-accent"
              onClick={(e) => {
                e.stopPropagation();
                onChange(null);
              }}
              onKeyDown={(e) => {
                if (e.key === "Enter" || e.key === " ") {
                  e.stopPropagation();
                  onChange(null);
                }
              }}
            >
              <X className="h-3.5 w-3.5" />
            </span>
          )}
          <ChevronsUpDown className="h-4 w-4 text-muted-foreground" />
        </span>
      </PopoverTrigger>
      <PopoverContent className="w-72 p-2" align="start">
        <Input
          value={input}
          onChange={(e) => setInput(e.target.value)}
          placeholder="Search issues…"
          className="mb-2 h-8"
          // Don't steal the dialog's autofocus fight; focus is fine here.
          autoFocus
        />
        {searchQuery.isLoading ? (
          <div className="space-y-2 p-1">
            <Skeleton className="h-8 w-full" />
            <Skeleton className="h-8 w-full" />
            <Skeleton className="h-8 w-full" />
          </div>
        ) : searchQuery.isError ? (
          <p className="p-2 text-sm text-destructive">
            Couldn't load issues. Try again.
          </p>
        ) : (
          <ScrollArea className="max-h-64">
            <button
              type="button"
              className={cn(
                "flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left text-sm hover:bg-accent",
                value === null && "bg-accent",
              )}
              onClick={() => pick(null)}
            >
              <span className="text-muted-foreground">
                No parent (top-level)
              </span>
            </button>
            {results.map((issue) => (
              <button
                key={issue.id}
                type="button"
                className={cn(
                  "flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left text-sm hover:bg-accent",
                  value?.id === issue.id && "bg-accent",
                )}
                onClick={() => pick(issue)}
              >
                <Badge
                  variant="outline"
                  className="font-mono text-[11px] shrink-0"
                >
                  {issue.display_id}
                </Badge>
                <span className="truncate">{issue.name}</span>
              </button>
            ))}
            {results.length === 0 && (
              <p className="p-2 text-sm text-muted-foreground">
                {debounced ? "No issues match." : "No issues in this project."}
              </p>
            )}
          </ScrollArea>
        )}
      </PopoverContent>
    </Popover>
  );
}
