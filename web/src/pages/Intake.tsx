import { useState, type FormEvent } from "react";
import { Link, useParams } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, ApiError } from "../lib/api";
import { INTAKE_STATUS } from "../lib/types";
import type { IntakeInbox, IntakeItem } from "../lib/types";
import type { IssueListResult } from "../lib/types";
import { Button } from "../components/ui/button";
import { Card, CardContent } from "../components/ui/card";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "../components/ui/dialog";
import { Input } from "../components/ui/input";
import { Label } from "../components/ui/label";
import { Badge } from "../components/ui/badge";
import { Skeleton } from "../components/ui/skeleton";
import { Alert, AlertDescription } from "../components/ui/alert";
import ThemeToggle from "../components/ThemeToggle";
import NotificationBell from "../components/notifications/NotificationBell";

/**
 * The effective status of an inbox item. The server stores the snoozed
 * status verbatim — a snoozed row whose snoozed_till has passed reads as
 * pending (resurfaced). Computed here, never trusted from status_name.
 */
function effectivePending(item: IntakeItem): boolean {
  if (item.status === INTAKE_STATUS.pending) return true;
  if (
    item.status === INTAKE_STATUS.snoozed &&
    item.snoozed_till &&
    new Date(item.snoozed_till).getTime() <= Date.now()
  ) {
    return true;
  }
  return false;
}

function fmtTime(iso: string): string {
  const d = new Date(iso);
  return d.toLocaleString(undefined, {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  });
}

/** "YYYY-MM-DDTHH:mm" for datetime-local inputs (local time). */
function toLocalInputValue(d: Date): string {
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

// ---------- Row ----------

function IntakeRow({
  item,
  onAccept,
  onReject,
  onSnooze,
  onDuplicate,
  busy,
}: {
  item: IntakeItem;
  onAccept: () => void;
  onReject: () => void;
  onSnooze: () => void;
  onDuplicate: () => void;
  busy: boolean;
}) {
  const resurfaced =
    item.status === INTAKE_STATUS.snoozed && effectivePending(item);
  return (
    <Card>
      <CardContent className="flex flex-wrap items-center gap-3 py-3">
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-2">
            {item.issue ? (
              <>
                <Badge variant="secondary">{item.issue.display_id}</Badge>
                <Link
                  to={`../i/${item.issue.id}`}
                  className="truncate font-medium hover:underline"
                >
                  {item.issue.name}
                </Link>
              </>
            ) : (
              <span className="text-sm text-muted-foreground">
                Issue {item.issue_id}
              </span>
            )}
            {resurfaced && <Badge variant="outline">resurfaced</Badge>}
          </div>
          <p className="mt-1 text-xs text-muted-foreground">
            arrived {fmtTime(item.created_at)}
            {item.snoozed_till && !resurfaced
              ? ` · snoozed until ${fmtTime(item.snoozed_till)}`
              : null}
          </p>
        </div>
        <div className="flex flex-wrap gap-2">
          <Button size="sm" onClick={onAccept} disabled={busy}>
            Accept
          </Button>
          <Button size="sm" variant="outline" onClick={onSnooze} disabled={busy}>
            Snooze
          </Button>
          <Button
            size="sm"
            variant="outline"
            onClick={onDuplicate}
            disabled={busy}
          >
            Duplicate
          </Button>
          <Button
            size="sm"
            variant="outline"
            className="text-destructive"
            onClick={onReject}
            disabled={busy}
          >
            Reject
          </Button>
        </div>
      </CardContent>
    </Card>
  );
}

// ---------- Page ----------

export default function Intake() {
  const { slug = "", identifier = "" } = useParams<{
    slug: string;
    identifier: string;
  }>();
  const queryClient = useQueryClient();

  const base = `/api/v1/workspaces/${encodeURIComponent(slug)}/projects/${encodeURIComponent(identifier)}`;
  const inboxKey = ["intake", slug, identifier] as const;

  const [actionError, setActionError] = useState<string | null>(null);
  const [snoozeTarget, setSnoozeTarget] = useState<IntakeItem | null>(null);
  const [dupTarget, setDupTarget] = useState<IntakeItem | null>(null);

  const inboxQuery = useQuery({
    queryKey: inboxKey,
    queryFn: () => api.get<IntakeInbox>(`${base}/intake`),
  });

  // Remove an item from the cached inbox (optimistic triage).
  function removeFromCache(itemId: string) {
    queryClient.setQueryData<IntakeInbox>(inboxKey, (old) => {
      if (!old) return old;
      return {
        ...old,
        items: old.items.filter((i) => i.id !== itemId),
        snoozed: (old.snoozed ?? []).filter((i) => i.id !== itemId),
      };
    });
  }

  function triageMutation<V>(
    fn: (item: IntakeItem, vars: V) => Promise<unknown>,
  ) {
    return useMutation({
      mutationFn: ({ item, vars }: { item: IntakeItem; vars: V }) =>
        fn(item, vars),
      onMutate: async ({ item }) => {
        setActionError(null);
        await queryClient.cancelQueries({ queryKey: inboxKey });
        const previous = queryClient.getQueryData<IntakeInbox>(inboxKey);
        removeFromCache(item.id);
        return { previous };
      },
      onError: (e, _vars, context) => {
        if (context?.previous) {
          queryClient.setQueryData(inboxKey, context.previous);
        }
        setActionError(
          e instanceof ApiError ? e.message : "Triage action failed",
        );
      },
      onSettled: () => {
        void queryClient.invalidateQueries({ queryKey: inboxKey });
        void queryClient.invalidateQueries({
          queryKey: ["issues", slug, identifier],
        });
      },
    });
  }

  const acceptMutation = triageMutation<Record<string, never>>((item) =>
    api.post(`${base}/intake/issues/${item.issue_id}/accept`),
  );
  const rejectMutation = triageMutation<Record<string, never>>((item) =>
    api.post(`${base}/intake/issues/${item.issue_id}/reject`),
  );
  const snoozeMutation = triageMutation<{ snoozed_till: string }>(
    (item, vars) =>
      api.post(`${base}/intake/issues/${item.issue_id}/snooze`, vars),
  );
  const duplicateMutation = triageMutation<{ duplicate_to_id: string }>(
    (item, vars) =>
      api.post(`${base}/intake/issues/${item.issue_id}/duplicate`, vars),
  );

  const busy =
    acceptMutation.isPending ||
    rejectMutation.isPending ||
    snoozeMutation.isPending ||
    duplicateMutation.isPending;

  function triage(item: IntakeItem, kind: "accept" | "reject") {
    const vars = { item, vars: {} as Record<string, never> };
    if (kind === "accept") acceptMutation.mutate(vars);
    else rejectMutation.mutate(vars);
  }

  // Snooze dialog state.
  const [snoozeAt, setSnoozeAt] = useState("");
  const [snoozeError, setSnoozeError] = useState<string | null>(null);

  function openSnooze(item: IntakeItem) {
    setSnoozeTarget(item);
    setSnoozeAt("");
    setSnoozeError(null);
  }

  function submitSnooze(e: FormEvent) {
    e.preventDefault();
    if (!snoozeTarget) return;
    const at = new Date(snoozeAt);
    if (Number.isNaN(at.getTime())) {
      setSnoozeError("Pick a date and time.");
      return;
    }
    if (at.getTime() <= Date.now()) {
      setSnoozeError("Snooze until must be in the future.");
      return;
    }
    setSnoozeError(null);
    snoozeMutation.mutate(
      {
        item: snoozeTarget,
        vars: { snoozed_till: new Date(snoozeAt).toISOString() },
      },
      {
      onSuccess: () => setSnoozeTarget(null),
      onError: (err) => {
        setSnoozeError(
          err instanceof ApiError ? err.message : "Snooze failed",
        );
      },
    });
  }

  // Duplicate dialog state.
  const [dupTargetId, setDupTargetId] = useState("");
  const [dupQuery, setDupQuery] = useState("");
  const [dupError, setDupError] = useState<string | null>(null);

  const dupSearch = useQuery({
    queryKey: ["intake-duplicate-search", slug, identifier, dupQuery],
    queryFn: () =>
      api.get<IssueListResult>(
        `${base}/issues?per_page=10${dupQuery.trim() ? `&q=${encodeURIComponent(dupQuery.trim())}` : ""}`,
      ),
    enabled: dupTarget !== null,
  });

  function openDuplicate(item: IntakeItem) {
    setDupTarget(item);
    setDupTargetId("");
    setDupQuery("");
    setDupError(null);
  }

  function submitDuplicate(e: FormEvent) {
    e.preventDefault();
    if (!dupTarget || !dupTargetId) {
      setDupError("Pick the issue this is a duplicate of.");
      return;
    }
    if (dupTargetId === dupTarget.issue_id) {
      setDupError("An issue can't be a duplicate of itself.");
      return;
    }
    setDupError(null);
    duplicateMutation.mutate(
      { item: dupTarget, vars: { duplicate_to_id: dupTargetId } },
      {
      onSuccess: () => setDupTarget(null),
      onError: (err) => {
        setDupError(
          err instanceof ApiError ? err.message : "Marking duplicate failed",
        );
      },
    });
  }

  const data = inboxQuery.data;
  const pendingItems = (data?.items ?? []).filter(effectivePending);
  const snoozedItems = data?.snoozed ?? [];
  const nowLocal = toLocalInputValue(new Date(Date.now() + 60_000));

  return (
    <div className="mx-auto w-full max-w-4xl p-6">
      <div className="mb-6 flex items-start justify-between gap-4">
        <div>
          <Link
            to={`/w/${slug}/p/${identifier}`}
            className="text-xs text-muted-foreground hover:underline"
          >
            ← Issues
          </Link>
          <h1 className="mt-1 text-2xl font-semibold tracking-tight">
            Intake inbox
          </h1>
          <p className="text-sm text-muted-foreground">
            Triage new issues: accept them into the backlog, or reject, snooze,
            or mark as duplicate.
          </p>
        </div>
        <NotificationBell />
        <ThemeToggle />
      </div>

      {inboxQuery.isError && (
        <Alert variant="destructive" className="mb-4">
          <AlertDescription>
            {inboxQuery.error instanceof ApiError
              ? inboxQuery.error.message
              : "Failed to load the intake inbox."}
          </AlertDescription>
        </Alert>
      )}
      {actionError && (
        <Alert variant="destructive" className="mb-4">
          <AlertDescription>{actionError}</AlertDescription>
        </Alert>
      )}

      {inboxQuery.isLoading ? (
        <div className="space-y-3">
          {[0, 1, 2].map((i) => (
            <Skeleton key={i} className="h-20 w-full" />
          ))}
        </div>
      ) : data ? (
        <>
          <section>
            <h2 className="mb-3 text-sm font-medium text-muted-foreground">
              Needs triage ({pendingItems.length})
            </h2>
            {pendingItems.length === 0 ? (
              <Card>
                <CardContent className="py-10 text-center">
                  <p className="text-lg font-medium">Inbox zero</p>
                  <p className="mt-1 text-sm text-muted-foreground">
                    Nothing waiting for triage. New issues created with intake
                    enabled will show up here.
                  </p>
                </CardContent>
              </Card>
            ) : (
              <div className="space-y-3">
                {pendingItems.map((item) => (
                  <IntakeRow
                    key={item.id}
                    item={item}
                    busy={busy}
                    onAccept={() => triage(item, "accept")}
                    onReject={() => triage(item, "reject")}
                    onSnooze={() => openSnooze(item)}
                    onDuplicate={() => openDuplicate(item)}
                  />
                ))}
              </div>
            )}
          </section>

          {snoozedItems.length > 0 && (
            <section className="mt-8">
              <h2 className="mb-3 text-sm font-medium text-muted-foreground">
                Snoozed ({snoozedItems.length})
              </h2>
              <div className="space-y-3">
                {snoozedItems.map((item) => (
                  <IntakeRow
                    key={item.id}
                    item={item}
                    busy={busy}
                    onAccept={() => triage(item, "accept")}
                    onReject={() => triage(item, "reject")}
                    onSnooze={() => openSnooze(item)}
                    onDuplicate={() => openDuplicate(item)}
                  />
                ))}
              </div>
            </section>
          )}
        </>
      ) : null}

      {/* Snooze dialog */}
      <Dialog
        open={snoozeTarget !== null}
        onOpenChange={(open) => {
          if (!open) setSnoozeTarget(null);
        }}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Snooze issue</DialogTitle>
            <DialogDescription>
              {snoozeTarget?.issue
                ? `${snoozeTarget.issue.display_id} — ${snoozeTarget.issue.name}`
                : "This issue"}{" "}
              will resurface in the inbox after the chosen time.
            </DialogDescription>
          </DialogHeader>
          <form onSubmit={submitSnooze} className="space-y-4">
            <div className="space-y-2">
              <Label htmlFor="snooze-at">Snooze until</Label>
              <Input
                id="snooze-at"
                type="datetime-local"
                min={nowLocal}
                value={snoozeAt}
                onChange={(e) => setSnoozeAt(e.target.value)}
              />
              <div className="flex gap-2">
                <Button
                  type="button"
                  size="sm"
                  variant="outline"
                  onClick={() =>
                    setSnoozeAt(
                      toLocalInputValue(new Date(Date.now() + 86_400_000)),
                    )
                  }
                >
                  Tomorrow
                </Button>
                <Button
                  type="button"
                  size="sm"
                  variant="outline"
                  onClick={() =>
                    setSnoozeAt(
                      toLocalInputValue(new Date(Date.now() + 7 * 86_400_000)),
                    )
                  }
                >
                  Next week
                </Button>
              </div>
            </div>
            {snoozeError && (
              <Alert variant="destructive">
                <AlertDescription>{snoozeError}</AlertDescription>
              </Alert>
            )}
            <DialogFooter>
              <Button
                type="button"
                variant="outline"
                onClick={() => setSnoozeTarget(null)}
              >
                Cancel
              </Button>
              <Button type="submit" disabled={snoozeMutation.isPending}>
                {snoozeMutation.isPending ? "Snoozing…" : "Snooze"}
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>

      {/* Duplicate dialog */}
      <Dialog
        open={dupTarget !== null}
        onOpenChange={(open) => {
          if (!open) setDupTarget(null);
        }}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Mark as duplicate</DialogTitle>
            <DialogDescription>
              {dupTarget?.issue
                ? `${dupTarget.issue.display_id} — ${dupTarget.issue.name}`
                : "This issue"}{" "}
              is a duplicate of…
            </DialogDescription>
          </DialogHeader>
          <form onSubmit={submitDuplicate} className="space-y-4">
            <div className="space-y-2">
              <Label htmlFor="dup-search">Search issues</Label>
              <Input
                id="dup-search"
                placeholder="Type to search…"
                value={dupQuery}
                onChange={(e) => setDupQuery(e.target.value)}
              />
            </div>
            <div className="max-h-56 space-y-1 overflow-y-auto">
              {dupSearch.isLoading && <Skeleton className="h-10 w-full" />}
              {dupSearch.data?.results
                .filter((i) => i.id !== dupTarget?.issue_id)
                .map((i) => (
                  <button
                    key={i.id}
                    type="button"
                    onClick={() => setDupTargetId(i.id)}
                    className={`flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left text-sm hover:bg-accent ${
                      dupTargetId === i.id ? "bg-accent" : ""
                    }`}
                  >
                    <Badge variant="secondary">{i.display_id}</Badge>
                    <span className="truncate">{i.name}</span>
                  </button>
                ))}
              {dupSearch.data && dupSearch.data.results.length === 0 && (
                <p className="py-2 text-center text-sm text-muted-foreground">
                  No issues found.
                </p>
              )}
            </div>
            {dupError && (
              <Alert variant="destructive">
                <AlertDescription>{dupError}</AlertDescription>
              </Alert>
            )}
            <DialogFooter>
              <Button
                type="button"
                variant="outline"
                onClick={() => setDupTarget(null)}
              >
                Cancel
              </Button>
              <Button
                type="submit"
                disabled={!dupTargetId || duplicateMutation.isPending}
              >
                {duplicateMutation.isPending
                  ? "Marking…"
                  : "Mark as duplicate"}
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>
    </div>
  );
}
