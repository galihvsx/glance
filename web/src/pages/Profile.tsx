// Profile & sessions (C6T7): /profile.
//
// Sections: display name (inline edit via PATCH /api/v1/auth/me), email
// (read-only — email is OTP/OAuth identity), API tokens link (C6T0 page),
// active sessions (revoke per session + revoke-others). The avatar is an
// initials fallback — no avatar upload backend exists.

import { useState } from "react";
import { Link } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { KeyRound, MonitorSmartphone, Pencil } from "lucide-react";
import { ApiError } from "../lib/api";
import { useAuth } from "../lib/auth";
import {
  deviceLabel,
  fetchSessions,
  initials,
  revokeSession,
  updateMyName,
} from "../lib/profile";
import { relativeTime } from "../lib/relativeTime";
import { Alert, AlertDescription } from "../components/ui/alert";
import { Badge } from "../components/ui/badge";
import { Button, buttonVariants } from "../components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "../components/ui/card";
import { Input } from "../components/ui/input";
import { Label } from "../components/ui/label";
import { Skeleton } from "../components/ui/skeleton";
import CalendarFeedCard from "../components/profile/CalendarFeedCard";

function errMsg(err: unknown, fallback: string): string {
  return err instanceof ApiError ? err.message : fallback;
}

export default function Profile() {
  const { user, refresh, logout } = useAuth();
  const queryClient = useQueryClient();
  const [editingName, setEditingName] = useState(false);
  const [nameDraft, setNameDraft] = useState("");
  const [error, setError] = useState<string | null>(null);

  const sessionsQuery = useQuery({
    queryKey: ["profile", "sessions"],
    queryFn: fetchSessions,
  });

  const nameMutation = useMutation({
    mutationFn: (name: string) => updateMyName(name),
    onSuccess: () => {
      setEditingName(false);
      setError(null);
      void refresh();
    },
    onError: (e) => setError(errMsg(e, "Failed to update name")),
  });

  const revokeMutation = useMutation({
    mutationFn: (id: string) => revokeSession(id),
    onSuccess: (_data, id) => {
      setError(null);
      // Revoking the current session logs this tab out — converge auth state.
      const wasCurrent = sessionsQuery.data?.some(
        (s) => s.id === id && s.current,
      );
      void queryClient.invalidateQueries({ queryKey: ["profile", "sessions"] });
      if (wasCurrent) void refresh();
    },
    onError: (e) => setError(errMsg(e, "Failed to revoke session")),
  });

  function onRevokeOthers() {
    const others = (sessionsQuery.data ?? []).filter((s) => !s.current);
    if (others.length === 0) return;
    if (
      !window.confirm(
        `Revoke ${others.length} other session${others.length === 1 ? "" : "s"}? You'll stay signed in here.`,
      )
    )
      return;
    for (const s of others) revokeMutation.mutate(s.id);
  }

  const sessions = sessionsQuery.data ?? [];
  const others = sessions.filter((s) => !s.current);

  return (
    <div className="mx-auto max-w-3xl space-y-4 p-4">
      <div className="flex items-center justify-between">
        <h1 className="text-xl font-semibold">Profile</h1>
        <div className="flex items-center gap-2">
          <Button variant="ghost" size="sm" onClick={() => void logout()}>
            Log out
          </Button>
        </div>
      </div>

      {error && (
        <Alert variant="destructive">
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}

      {/* Identity */}
      <Card>
        <CardHeader>
          <CardTitle>Identity</CardTitle>
        </CardHeader>
        <CardContent className="space-y-4">
          <div className="flex items-center gap-4">
            <span
              className="flex h-14 w-14 items-center justify-center rounded-full bg-primary text-lg font-semibold text-primary-foreground"
              aria-hidden
            >
              {user ? initials(user.name, user.email) : "?"}
            </span>
            <div className="flex-1">
              {editingName ? (
                <div className="flex items-center gap-2">
                  <Input
                    value={nameDraft}
                    onChange={(e) => setNameDraft(e.target.value)}
                    placeholder="Your name"
                    maxLength={100}
                    aria-label="Display name"
                  />
                  <Button
                    size="sm"
                    disabled={!nameDraft.trim() || nameMutation.isPending}
                    onClick={() => nameMutation.mutate(nameDraft)}
                  >
                    {nameMutation.isPending ? "Saving…" : "Save"}
                  </Button>
                  <Button
                    size="sm"
                    variant="ghost"
                    onClick={() => setEditingName(false)}
                  >
                    Cancel
                  </Button>
                </div>
              ) : (
                <div className="flex items-center gap-2">
                  <span className="text-base font-medium">
                    {user?.name ?? (
                      <span className="text-muted-foreground">No name set</span>
                    )}
                  </span>
                  <Button
                    variant="ghost"
                    size="icon"
                    aria-label="Edit display name"
                    onClick={() => {
                      setNameDraft(user?.name ?? "");
                      setEditingName(true);
                    }}
                  >
                    <Pencil className="h-4 w-4" />
                  </Button>
                </div>
              )}
              <p className="mt-1 text-sm text-muted-foreground">{user?.email}</p>
            </div>
          </div>
          <p className="text-xs text-muted-foreground">
            Email is your sign-in identity and can't be changed here.
          </p>
        </CardContent>
      </Card>

      {/* API tokens */}
      <Card>
        <CardHeader>
          <CardTitle>API tokens</CardTitle>
        </CardHeader>
        <CardContent>
          <div className="flex items-center justify-between">
            <p className="text-sm text-muted-foreground">
              Personal access tokens for scripts and integrations.
            </p>
            <Link
              to="/settings/tokens"
              className={buttonVariants({ variant: "outline", size: "sm" })}
            >
              <KeyRound className="mr-1 h-4 w-4" /> Manage tokens
            </Link>
          </div>
        </CardContent>
      </Card>

      {/* Calendar feed (iCal subscription token) */}
      <CalendarFeedCard />

      {/* Sessions */}
      <Card>
        <CardHeader className="flex flex-row items-center justify-between">
          <CardTitle>Active sessions</CardTitle>
          {others.length > 0 && (
            <Button variant="outline" size="sm" onClick={onRevokeOthers}>
              Revoke others ({others.length})
            </Button>
          )}
        </CardHeader>
        <CardContent>
          {sessionsQuery.isLoading ? (
            <Skeleton className="h-16 w-full" />
          ) : sessions.length === 0 ? (
            <p className="text-sm text-muted-foreground">No active sessions.</p>
          ) : (
            <ul className="divide-y rounded-md border">
              {sessions.map((s) => (
                <li key={s.id} className="flex items-center gap-3 px-3 py-2.5">
                  <MonitorSmartphone
                    className="h-5 w-5 shrink-0 text-muted-foreground"
                    aria-hidden
                  />
                  <div className="flex-1">
                    <div className="flex items-center gap-2">
                      <span className="text-sm font-medium">
                        {deviceLabel(s.user_agent)}
                      </span>
                      {s.current && <Badge variant="secondary">This device</Badge>}
                    </div>
                    <p className="text-xs text-muted-foreground">
                      {s.ip ?? "unknown IP"} · last seen{" "}
                      {relativeTime(s.last_seen_at)}
                    </p>
                  </div>
                  <Button
                    variant="ghost"
                    size="sm"
                    onClick={() => {
                      if (
                        s.current &&
                        !window.confirm(
                          "Revoke this session? You'll be signed out here.",
                        )
                      )
                        return;
                      revokeMutation.mutate(s.id);
                    }}
                  >
                    Revoke
                  </Button>
                </li>
              ))}
            </ul>
          )}
          <div className="mt-3 space-y-1">
            <Label className="text-xs font-medium text-muted-foreground">
              Details
            </Label>
            <p className="text-xs text-muted-foreground">
              Sessions are listed by most recent sign-in. Revoking the current
              session signs you out of this browser.
            </p>
          </div>
        </CardContent>
      </Card>
    </div>
  );
}
