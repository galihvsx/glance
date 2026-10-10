import { useEffect, useMemo, useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { ApiError, api } from "../../lib/api";
import type { IssueLink } from "../../lib/types";
import { toast } from "../ui/toast";
import { issueLinksPath } from "../issue/issueLinks";

/** Dependency editing on the Gantt timeline (C15T0).
 *
 *  Reads the SAME directed issue-link model the detail page uses (C8T0):
 *  one row per edge, kind "blocks", POST/DELETE on
 *  .../issues/{uuid}/links. No new backend surface — this module is a
 *  thin, optimistic client plus a pointer interaction state machine.
 *
 *  Pointer model: a "link mode" toggle arms edge drawing. In link mode a
 *  pointer-down on an issue bar starts a draw; the release point is
 *  hit-tested against the dated rows and a drop on a different issue
 *  creates a `blocks` edge (source → target). Clicking an existing edge
 *  opens a confirm dialog and deletes it. Esc cancels the draw / exits
 *  link mode.
 *
 *  Keyboard users never need the pointer path: every gutter row is a
 *  native button that opens the peek drawer, whose Linked-issues panel
 *  (C8T0) is fully keyboard-operable. */

/** Pointer interaction state for dependency editing. Pure transition
 *  functions below make it unit-testable without React. */
export interface GanttLinkUiState {
  /** Edge drawing is armed; bar pointer-down starts a draw. */
  linkMode: boolean;
  /** Issue id the in-progress draw started from, or null. */
  drawingFrom: string | null;
  /** Edge awaiting the delete confirm dialog, or null. */
  confirmDelete: IssueLink | null;
}

export const initialGanttLinkUiState: GanttLinkUiState = {
  linkMode: false,
  drawingFrom: null,
  confirmDelete: null,
};

export function enterLinkMode(s: GanttLinkUiState): GanttLinkUiState {
  return { ...s, linkMode: true };
}

export function exitLinkMode(s: GanttLinkUiState): GanttLinkUiState {
  return { ...s, linkMode: false, drawingFrom: null };
}

export function startLinkDraw(
  s: GanttLinkUiState,
  fromId: string,
): GanttLinkUiState {
  if (!s.linkMode) return s;
  return { ...s, drawingFrom: fromId };
}

export function cancelLinkDraw(s: GanttLinkUiState): GanttLinkUiState {
  return { ...s, drawingFrom: null };
}

export function requestLinkDelete(
  s: GanttLinkUiState,
  link: IssueLink,
): GanttLinkUiState {
  return { ...s, confirmDelete: link };
}

export function closeLinkDelete(s: GanttLinkUiState): GanttLinkUiState {
  return { ...s, confirmDelete: null };
}

/** Pure drop resolution for a finished draw. "create" when the draw
 *  lands on a different issue, "self" for a self-drop (backend rejects
 *  with 409 — the UI explains instead), "cancel" when the draw never
 *  started or missed every row. */
export function resolveLinkDrop(
  fromId: string | null,
  toId: string | null,
): "create" | "self" | "cancel" {
  if (!fromId || !toId) return "cancel";
  if (fromId === toId) return "self";
  return "create";
}

const TEMP_LINK_PREFIX = "temp-";

/** True for optimistic links that have no server row yet. */
export function isTempLinkId(id: string): boolean {
  return id.startsWith(TEMP_LINK_PREFIX);
}

function tempLinkId(): string {
  return `${TEMP_LINK_PREFIX}${Math.random().toString(36).slice(2)}`;
}

/** Hit-test a vertical svg position against dated rows. `rows` is
 *  [{id, y}] (y = row top in svg coordinates); returns the issue id
 *  whose row contains y, else null. */
export function dropTargetAt(
  rows: { id: string; y: number }[],
  y: number,
  rowH: number,
): string | null {
  for (const r of rows) {
    if (y >= r.y && y < r.y + rowH) return r.id;
  }
  return null;
}

/** Merge server links with optimistic ops: removed ids are hidden, and
 *  added links the server has not echoed yet are appended (deduped by
 *  id, so a server echo replaces the optimistic row instead of
 *  doubling it). */
export function effectiveLinks(
  server: IssueLink[],
  added: IssueLink[],
  removedIds: readonly string[],
): IssueLink[] {
  const removed = new Set(removedIds);
  const serverIds = new Set(server.map((l) => l.id));
  return server
    .filter((l) => !removed.has(l.id))
    .concat(added.filter((a) => !serverIds.has(a.id)));
}

function errorDescription(e: unknown, fallback: string): string {
  if (e instanceof ApiError) return e.message;
  if (e instanceof Error && e.message) return e.message;
  return fallback;
}

export interface GanttLinkOptimistic {
  added: IssueLink[];
  removedIds: string[];
}

export interface CreateLinkInput {
  fromId: string;
  toId: string;
}

/** Create/delete mutations for Gantt edges with optimistic updates.
 *
 *  `serverLinks` is the merged link list from the Gantt list queries.
 *  Returns the effective list (server + optimistic) plus the two
 *  mutations. Optimistic rows are pruned once the server echoes them:
 *  creates replace the temp id on success, deletes drop from
 *  removedIds when the id vanishes server-side, and both roll back on
 *  error with an error toast. On settle the gantt queries refetch. */
export function useGanttLinkMutations(
  slug: string,
  identifier: string,
  serverLinks: IssueLink[],
) {
  const queryClient = useQueryClient();
  const [optimistic, setOptimistic] = useState<GanttLinkOptimistic>({
    added: [],
    removedIds: [],
  });

  // Prune optimistic entries the server has echoed: adds whose id now
  // exists server-side (temp ids replaced on success, so this only
  // catches the refetch window), and removals for ids gone server-side.
  // Keyed on a content-stable string so a fresh-but-equal array identity
  // (e.g. an inline [] in tests) does not re-trigger the effect.
  const serverIdKey = useMemo(
    () => serverLinks.map((l) => l.id).sort().join("|"),
    [serverLinks],
  );
  const serverIds = useMemo(
    () => new Set(serverLinks.map((l) => l.id)),
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [serverIdKey],
  );
  useEffect(() => {
    setOptimistic((o) => ({
      added: o.added.filter((a) => !serverIds.has(a.id)),
      removedIds: o.removedIds.filter((id) => serverIds.has(id)),
    }));
  }, [serverIds]);

  const links = useMemo(
    () => effectiveLinks(serverLinks, optimistic.added, optimistic.removedIds),
    [serverLinks, optimistic],
  );

  const invalidate = () =>
    queryClient.invalidateQueries({ queryKey: ["gantt", slug, identifier] });

  const createMutation = useMutation({
    mutationFn: ({ fromId, toId }: CreateLinkInput) =>
      api.post<IssueLink>(issueLinksPath(slug, identifier, fromId), {
        target_issue_id: toId,
        kind: "blocks",
      }),
    onMutate: (vars) => {
      const temp: IssueLink = {
        id: tempLinkId(),
        issue_id: vars.fromId,
        target_issue_id: vars.toId,
        kind: "blocks",
        created_at: new Date().toISOString(),
      };
      setOptimistic((o) => ({ ...o, added: [...o.added, temp] }));
      return { tempId: temp.id };
    },
    onSuccess: (created, _vars, ctx) => {
      setOptimistic((o) => ({
        ...o,
        added: o.added.map((a) => (a.id === ctx?.tempId ? created : a)),
      }));
    },
    onError: (e, _vars, ctx) => {
      setOptimistic((o) => ({
        ...o,
        added: o.added.filter((a) => a.id !== ctx?.tempId),
      }));
      toast.add({
        title: "Failed to create dependency",
        description: errorDescription(e, "Please try again."),
        type: "error",
      });
    },
    onSettled: () => {
      void invalidate();
    },
  });

  const deleteMutation = useMutation({
    mutationFn: (link: IssueLink) => {
      // A not-yet-synced optimistic add has no server row — just drop it.
      if (isTempLinkId(link.id)) return Promise.resolve();
      return api.del(
        `${issueLinksPath(slug, identifier, link.issue_id)}/${encodeURIComponent(link.id)}`,
      );
    },
    onMutate: (link) => {
      const temp = isTempLinkId(link.id);
      setOptimistic((o) => ({
        added: o.added.filter((a) => a.id !== link.id),
        removedIds: temp ? o.removedIds : [...o.removedIds, link.id],
      }));
      return { link, temp };
    },
    onError: (e, _link, ctx) => {
      if (ctx) {
        setOptimistic((o) => ({
          added: ctx.temp ? [...o.added, ctx.link] : o.added,
          removedIds: ctx.temp
            ? o.removedIds
            : o.removedIds.filter((id) => id !== ctx.link.id),
        }));
      }
      toast.add({
        title: "Failed to remove dependency",
        description: errorDescription(e, "Please try again."),
        type: "error",
      });
    },
    onSettled: () => {
      void invalidate();
    },
  });

  return { links, createMutation, deleteMutation };
}
