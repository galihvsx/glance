import { useEffect, useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Play, Square, Plus, History } from "lucide-react";

import { api } from "../../lib/api";
import { useAuth } from "../../lib/auth";
import type { TimeEntry } from "../../lib/types";
import { Button } from "../ui/button";
import { Card, CardContent } from "../ui/card";
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "../ui/dialog";
import { Input } from "../ui/input";
import { Label } from "../ui/label";
import { Textarea } from "../ui/textarea";
import { Separator } from "../ui/separator";

/** Format seconds as "2h 15m" / "45m" / "30s". */
export function formatDuration(totalSeconds: number): string {
  const s = Math.max(0, Math.floor(totalSeconds));
  const h = Math.floor(s / 3600);
  const m = Math.floor((s % 3600) / 60);
  const sec = s % 60;
  if (h > 0) return `${h}h ${m}m`;
  if (m > 0) return `${m}m`;
  return `${sec}s`;
}

interface PersistedTimer {
  entryId: string;
  startedAt: string; // ISO
}

function timerKey(uuid: string): string {
  return `glance:timer:${uuid}`;
}

function readPersisted(uuid: string): PersistedTimer | null {
  try {
    const raw = localStorage.getItem(timerKey(uuid));
    if (!raw) return null;
    const p = JSON.parse(raw) as PersistedTimer;
    if (typeof p.entryId !== "string" || typeof p.startedAt !== "string")
      return null;
    return p;
  } catch {
    return null;
  }
}

/**
 * Time tracking widget for the issue detail sidebar (C3T7).
 *
 * Running-timer reconciliation: the server is the source of truth. On
 * load we read localStorage for an instantly-visible timer, then the
 * query result reconciles it — if the server reports a running entry for
 * the current user we adopt it (and persist it); otherwise the stale
 * localStorage entry is cleared. This way a reload mid-timer shows the
 * ticking clock immediately instead of flashing "no timer".
 */
export default function TimeTracker({
  slug,
  identifier,
  uuid,
}: {
  slug: string;
  identifier: string;
  uuid: string;
}) {
  const queryClient = useQueryClient();
  const { user } = useAuth();
  const timePath = `/api/v1/workspaces/${encodeURIComponent(slug)}/projects/${encodeURIComponent(identifier)}/issues/${encodeURIComponent(uuid)}/time`;
  const timeKey = ["time", slug, identifier, uuid];

  const [now, setNow] = useState(() => Date.now());
  const [logOpen, setLogOpen] = useState(false);
  const [logStart, setLogStart] = useState("");
  const [logEnd, setLogEnd] = useState("");
  const [logNote, setLogNote] = useState("");
  const [logError, setLogError] = useState<string | null>(null);
  const [persisted, setPersisted] = useState<PersistedTimer | null>(() =>
    readPersisted(uuid),
  );

  const timeQuery = useQuery({
    queryKey: timeKey,
    queryFn: () =>
      api.get<{ entries: TimeEntry[]; total_seconds: number }>(timePath),
  });

  const entries = timeQuery.data?.entries ?? [];
  const totalSeconds = timeQuery.data?.total_seconds ?? 0;

  // Server-side running entry for the current user (source of truth).
  const serverRunning = useMemo(
    () =>
      entries.find(
        (e) => e.ended_at == null && user != null && e.user_id === user.id,
      ) ?? null,
    [entries, user],
  );

  // Reconcile: adopt the server's running entry, or clear stale storage.
  useEffect(() => {
    if (!timeQuery.data || !user) return;
    if (serverRunning) {
      const p = { entryId: serverRunning.id, startedAt: serverRunning.started_at };
      localStorage.setItem(timerKey(uuid), JSON.stringify(p));
      setPersisted(p);
    } else if (persisted) {
      localStorage.removeItem(timerKey(uuid));
      setPersisted(null);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [timeQuery.data, serverRunning, user, uuid]);

  // The timer we display: server first, persisted fallback while loading.
  const running =
    serverRunning ??
    (persisted && !timeQuery.data
      ? { id: persisted.entryId, started_at: persisted.startedAt }
      : null);

  // Tick while a timer is displayed.
  useEffect(() => {
    if (!running) return;
    const t = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(t);
  }, [running]);

  const invalidate = () =>
    queryClient.invalidateQueries({ queryKey: timeKey });

  const startMutation = useMutation({
    mutationFn: () => api.post<TimeEntry>(`${timePath}/start`),
    onSuccess: (e) => {
      const p = { entryId: e.id, startedAt: e.started_at };
      localStorage.setItem(timerKey(uuid), JSON.stringify(p));
      setPersisted(p);
      invalidate();
    },
  });
  const stopMutation = useMutation({
    mutationFn: () => api.post<TimeEntry>(`${timePath}/stop`),
    onSuccess: () => {
      localStorage.removeItem(timerKey(uuid));
      setPersisted(null);
      invalidate();
    },
  });
  const logMutation = useMutation({
    mutationFn: (body: { started_at: string; ended_at: string; note: string }) =>
      api.post<TimeEntry>(`${timePath}/log`, body),
    onSuccess: () => {
      setLogOpen(false);
      setLogStart("");
      setLogEnd("");
      setLogNote("");
      setLogError(null);
      invalidate();
    },
    onError: (err) => {
      setLogError(err instanceof Error ? err.message : "Failed to log time");
    },
  });

  const elapsed = running
    ? Math.max(0, (now - new Date(running.started_at).getTime()) / 1000)
    : 0;
  const displayTotal = totalSeconds + elapsed;

  const submitLog = () => {
    setLogError(null);
    if (!logStart || !logEnd) {
      setLogError("Start and end are required.");
      return;
    }
    const s = new Date(logStart);
    const e = new Date(logEnd);
    if (Number.isNaN(s.getTime()) || Number.isNaN(e.getTime())) {
      setLogError("Invalid date/time.");
      return;
    }
    if (e <= s) {
      setLogError("End must be after start.");
      return;
    }
    logMutation.mutate({
      started_at: s.toISOString(),
      ended_at: e.toISOString(),
      note: logNote,
    });
  };

  const busy =
    startMutation.isPending || stopMutation.isPending || timeQuery.isLoading;

  return (
    <Card>
      <CardContent className="space-y-3 pt-6">
        <div className="flex items-center justify-between">
          <Label className="flex items-center gap-1.5">
            <History className="h-3.5 w-3.5" aria-hidden />
            Time tracking
          </Label>
          <span
            className="font-mono text-sm font-medium tabular-nums"
            title="Total logged (completed entries) plus the running timer"
          >
            {formatDuration(displayTotal)}
          </span>
        </div>

        {running ? (
          <div className="space-y-2">
            <div className="font-mono text-2xl font-semibold tabular-nums">
              {formatDuration(elapsed)}
            </div>
            <div className="text-xs text-muted-foreground">
              Running since{" "}
              {new Date(running.started_at).toLocaleTimeString([], {
                hour: "2-digit",
                minute: "2-digit",
              })}
            </div>
            <Button
              variant="destructive"
              size="sm"
              className="w-full"
              disabled={busy}
              onClick={() => stopMutation.mutate()}
            >
              <Square className="mr-1.5 h-3.5 w-3.5" aria-hidden />
              Stop timer
            </Button>
          </div>
        ) : (
          <Button
            variant="outline"
            size="sm"
            className="w-full"
            disabled={busy}
            onClick={() => startMutation.mutate()}
          >
            <Play className="mr-1.5 h-3.5 w-3.5" aria-hidden />
            Start timer
          </Button>
        )}

        <Button
          variant="ghost"
          size="sm"
          className="w-full"
          onClick={() => setLogOpen(true)}
        >
          <Plus className="mr-1.5 h-3.5 w-3.5" aria-hidden />
          Log time manually
        </Button>

        {entries.length > 0 && (
          <>
            <Separator />
            <div className="space-y-1.5">
              {entries.slice(0, 5).map((e) => (
                <div
                  key={e.id}
                  className="flex items-baseline justify-between gap-2 text-xs"
                >
                  <span className="truncate text-muted-foreground">
                    {e.note ||
                      (e.ended_at == null ? "Running…" : "Logged entry")}
                  </span>
                  <span className="shrink-0 font-mono tabular-nums">
                    {e.ended_at == null
                      ? formatDuration(
                          Math.max(
                            0,
                            (now - new Date(e.started_at).getTime()) / 1000,
                          ),
                        )
                      : formatDuration(
                          (new Date(e.ended_at).getTime() -
                            new Date(e.started_at).getTime()) /
                            1000,
                        )}
                  </span>
                </div>
              ))}
              {entries.length > 5 && (
                <div className="text-xs text-muted-foreground">
                  +{entries.length - 5} more
                </div>
              )}
            </div>
          </>
        )}

        <Dialog open={logOpen} onOpenChange={setLogOpen}>
          <DialogContent>
            <DialogHeader>
              <DialogTitle>Log time</DialogTitle>
            </DialogHeader>
            <div className="space-y-3">
              <div className="grid grid-cols-2 gap-3">
                <div className="space-y-1.5">
                  <Label htmlFor="tt-start">Start</Label>
                  <Input
                    id="tt-start"
                    type="datetime-local"
                    value={logStart}
                    onChange={(e) => setLogStart(e.target.value)}
                  />
                </div>
                <div className="space-y-1.5">
                  <Label htmlFor="tt-end">End</Label>
                  <Input
                    id="tt-end"
                    type="datetime-local"
                    value={logEnd}
                    onChange={(e) => setLogEnd(e.target.value)}
                  />
                </div>
              </div>
              <div className="space-y-1.5">
                <Label htmlFor="tt-note">Note</Label>
                <Textarea
                  id="tt-note"
                  value={logNote}
                  onChange={(e) => setLogNote(e.target.value)}
                  placeholder="What did you work on?"
                  rows={2}
                />
              </div>
              {logError && (
                <div className="text-sm text-destructive">{logError}</div>
              )}
            </div>
            <DialogFooter>
              <Button variant="ghost" onClick={() => setLogOpen(false)}>
                Cancel
              </Button>
              <Button
                onClick={submitLog}
                disabled={logMutation.isPending}
              >
                {logMutation.isPending ? "Saving…" : "Save entry"}
              </Button>
            </DialogFooter>
          </DialogContent>
        </Dialog>
      </CardContent>
    </Card>
  );
}
