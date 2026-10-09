import { useCallback, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "react-router-dom";
import { ListTree, Plus, X } from "lucide-react";
import { api } from "../../lib/api";
import type { Issue, IssueChild } from "../../lib/types";
import { Button } from "../ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "../ui/card";
import { Badge } from "../ui/badge";
import { Input } from "../ui/input";
import { Skeleton } from "../ui/skeleton";
import { usePeekParam } from "./usePeek";
import { childRows, removeChildMessage, subIssuesTitle } from "./subIssues";

/** Opens an issue the same way the list/board do: inside a peek drawer
 *  (compact) the drawer swaps to the new issue; on the full page the
 *  router navigates to the issue's detail route. */
function useOpenIssue(slug: string, identifier: string, compact: boolean) {
  const navigate = useNavigate();
  const { openPeek } = usePeekParam();
  return useCallback(
    (uuid: string) => {
      if (compact) {
        openPeek(uuid);
      } else {
        navigate(`/w/${slug}/p/${identifier}/i/${uuid}`);
      }
    },
    [compact, openPeek, navigate, slug, identifier],
  );
}

/** "Child of GLA-123" breadcrumb at the top of an issue detail. The detail
 *  payload only carries the parent's uuid, so the parent is fetched for
 *  its display identifier. Renders nothing while loading or if the
 *  parent can't be read (e.g. deleted). */
export function ParentBreadcrumb({
  slug,
  identifier,
  parentId,
  compact = false,
}: {
  slug: string;
  identifier: string;
  parentId: string;
  compact?: boolean;
}) {
  const base = `/api/v1/workspaces/${encodeURIComponent(slug)}/projects/${encodeURIComponent(identifier)}`;
  const openIssue = useOpenIssue(slug, identifier, compact);
  const parentQuery = useQuery({
    queryKey: ["issue", slug, identifier, parentId],
    queryFn: () =>
      api.get<Issue>(`${base}/issues/${encodeURIComponent(parentId)}`),
    retry: false,
  });

  if (parentQuery.isPending) {
    return <Skeleton className="mb-4 h-4 w-40" />;
  }
  const parent = parentQuery.data;
  if (!parent) return null;
  return (
    <button
      type="button"
      onClick={() => openIssue(parent.id)}
      className="mb-4 text-xs text-muted-foreground hover:underline"
      title={`Open parent issue ${parent.display_id}`}
    >
      ↑ Child of <span className="font-mono">{parent.display_id}</span>
    </button>
  );
}

/** One child row: identifier, title, state chip, click → open, × →
 *  detach from the parent (with confirm). */
function ChildRow({
  child,
  onOpen,
  onRemove,
  removing,
}: {
  child: IssueChild;
  onOpen: (uuid: string) => void;
  onRemove: (uuid: string) => void;
  removing: boolean;
}) {
  return (
    <li
      className="group flex cursor-pointer items-center gap-2 rounded-md px-2 py-1.5 hover:bg-accent"
      onClick={() => onOpen(child.uuid)}
      title={child.title}
    >
      <Badge variant="outline" className="shrink-0 font-mono text-[11px]">
        {child.identifier}
      </Badge>
      <span className="min-w-0 flex-1 truncate text-sm">{child.title}</span>
      <Badge variant="secondary" className="shrink-0">
        {child.state}
      </Badge>
      <Button
        variant="ghost"
        size="sm"
        className="h-6 w-6 shrink-0 p-0 opacity-0 group-hover:opacity-100 focus-visible:opacity-100"
        disabled={removing}
        title="Remove from parent"
        aria-label={`Remove ${child.identifier} from its parent`}
        onClick={(e) => {
          e.stopPropagation();
          onRemove(child.uuid);
        }}
      >
        <X className="h-3.5 w-3.5" />
      </Button>
    </li>
  );
}

/** "Sub-issues" section: child list, inline "Add sub-issue", detach. */
export default function SubIssues({
  slug,
  identifier,
  issue,
  issueKey,
  compact = false,
  onError,
}: {
  slug: string;
  identifier: string;
  issue: Issue;
  /** The react-query key of the detail query (invalidated after mutations). */
  issueKey: unknown[];
  compact?: boolean;
  onError: (msg: string | null) => void;
}) {
  const queryClient = useQueryClient();
  const base = `/api/v1/workspaces/${encodeURIComponent(slug)}/projects/${encodeURIComponent(identifier)}`;
  const openIssue = useOpenIssue(slug, identifier, compact);
  const [adding, setAdding] = useState(false);
  const [draft, setDraft] = useState("");
  const children = childRows(issue.children);
  const issuesKey = ["issues", slug, identifier];

  function invalidate() {
    queryClient.invalidateQueries({ queryKey: issueKey });
    queryClient.invalidateQueries({ queryKey: issuesKey });
  }

  const addMutation = useMutation({
    mutationFn: (name: string) =>
      api.post<Issue>(`${base}/issues`, { name, parent_id: issue.id }),
    onSuccess: () => {
      setDraft("");
      setAdding(false);
      onError(null);
      invalidate();
    },
    onError: (e) =>
      onError(e instanceof Error ? e.message : "Failed to add sub-issue"),
  });

  const removeMutation = useMutation({
    mutationFn: (childUuid: string) =>
      api.del(`${base}/issues/${encodeURIComponent(childUuid)}/parent`),
    onSuccess: () => {
      onError(null);
      invalidate();
    },
    onError: (e) =>
      onError(
        e instanceof Error ? e.message : "Failed to remove sub-issue",
      ),
  });

  function submitAdd() {
    const name = draft.trim();
    if (!name || addMutation.isPending) return;
    addMutation.mutate(name);
  }

  function removeChild(childUuid: string) {
    const child = children.find((c) => c.uuid === childUuid);
    if (!child) return;
    if (!window.confirm(removeChildMessage(child.title))) return;
    removeMutation.mutate(childUuid);
  }

  return (
    <Card>
      <CardHeader className="pb-3">
        <CardTitle className="flex items-center gap-2 text-base">
          <ListTree className="h-4 w-4" />
          {subIssuesTitle(children)}
        </CardTitle>
      </CardHeader>
      <CardContent className="space-y-2">
        {children.length === 0 ? (
          <p className="text-sm text-muted-foreground">
            No sub-issues yet. Add one below.
          </p>
        ) : (
          <ul className="-mx-2">
            {children.map((c) => (
              <ChildRow
                key={c.uuid}
                child={c}
                onOpen={openIssue}
                onRemove={removeChild}
                removing={removeMutation.isPending}
              />
            ))}
          </ul>
        )}
        {adding ? (
          <div className="flex gap-2 pt-1">
            <Input
              value={draft}
              onChange={(e) => setDraft(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === "Enter") submitAdd();
                if (e.key === "Escape") {
                  setAdding(false);
                  setDraft("");
                  e.stopPropagation();
                }
              }}
              placeholder="Sub-issue title — Enter to create, Esc to cancel"
              aria-label="New sub-issue title"
              autoFocus
              disabled={addMutation.isPending}
            />
            <Button
              size="sm"
              onClick={submitAdd}
              disabled={addMutation.isPending || !draft.trim()}
            >
              {addMutation.isPending ? "Adding…" : "Add"}
            </Button>
          </div>
        ) : (
          <Button
            variant="ghost"
            size="sm"
            className="gap-1.5 text-muted-foreground"
            onClick={() => setAdding(true)}
          >
            <Plus className="h-3.5 w-3.5" />
            Add sub-issue
          </Button>
        )}
      </CardContent>
    </Card>
  );
}
