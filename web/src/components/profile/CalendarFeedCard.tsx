// CalendarFeedCard: iCal subscription feed token management (C15T3).
// The feed token (glcal_…) authenticates calendar.ics subscription feeds
// for external calendar apps. Regenerate semantics: POST revokes any
// previous live token. The plaintext is shown exactly once after
// creation — the status endpoint never returns it.

import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { CalendarDays, Copy } from "lucide-react";
import { ApiError } from "../../lib/api";
import {
  createFeedToken,
  fetchFeedTokenStatus,
  feedURLPattern,
  revokeFeedToken,
} from "../../lib/calendarFeed";
import { relativeTime } from "../../lib/relativeTime";
import { Alert, AlertDescription } from "../ui/alert";
import { Button } from "../ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "../ui/card";
import { Input } from "../ui/input";
import { Skeleton } from "../ui/skeleton";

const QUERY_KEY = ["profile", "calendar-feed"];

export default function CalendarFeedCard() {
  const queryClient = useQueryClient();
  const [secret, setSecret] = useState<string | null>(null);
  const [copied, setCopied] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const statusQuery = useQuery({ queryKey: QUERY_KEY, queryFn: fetchFeedTokenStatus });

  const invalidate = () =>
    void queryClient.invalidateQueries({ queryKey: QUERY_KEY });

  const createMutation = useMutation({
    mutationFn: createFeedToken,
    onSuccess: (created) => {
      setSecret(created.token);
      setCopied(false);
      setError(null);
      invalidate();
    },
    onError: (e) =>
      setError(e instanceof ApiError ? e.message : "Failed to create feed token"),
  });

  const revokeMutation = useMutation({
    mutationFn: revokeFeedToken,
    onSuccess: () => {
      setSecret(null);
      setError(null);
      invalidate();
    },
    onError: (e) =>
      setError(e instanceof ApiError ? e.message : "Failed to revoke feed token"),
  });

  function onRevoke() {
    if (
      !window.confirm(
        "Revoke the calendar feed token? Existing calendar subscriptions will stop syncing.",
      )
    )
      return;
    revokeMutation.mutate();
  }

  async function copySecret() {
    if (!secret) return;
    try {
      await navigator.clipboard.writeText(secret);
      setCopied(true);
    } catch {
      setError("Copy failed — select the token text manually");
    }
  }

  const status = statusQuery.data;
  const active = status?.active ?? false;

  return (
    <Card>
      <CardHeader className="flex flex-row items-center justify-between">
        <CardTitle className="flex items-center gap-2">
          <CalendarDays className="h-4 w-4 text-muted-foreground" aria-hidden />
          Calendar feed
        </CardTitle>
        {active && (
          <Button
            variant="outline"
            size="sm"
            onClick={onRevoke}
            disabled={revokeMutation.isPending}
          >
            {revokeMutation.isPending ? "Revoking…" : "Revoke"}
          </Button>
        )}
      </CardHeader>
      <CardContent className="space-y-3">
        <p className="text-sm text-muted-foreground">
          Subscribe to project and cycle calendars from external calendar
          apps (Google Calendar, Apple Calendar). The token authenticates
          the feed — it acts as you for reading dated issues, so keep it
          secret.
        </p>

        {statusQuery.isLoading ? (
          <Skeleton className="h-5 w-48" />
        ) : active ? (
          <p className="text-sm">
            <span className="font-medium">Active</span>
            {status?.created_at && (
              <span className="text-muted-foreground">
                {" "}
                since {new Date(status.created_at).toLocaleDateString()}
              </span>
            )}
            {status?.last_used_at && (
              <span className="text-muted-foreground">
                {" "}· last used {relativeTime(status.last_used_at)}
              </span>
            )}
          </p>
        ) : (
          <p className="text-sm text-muted-foreground">No feed token yet.</p>
        )}

        {secret && (
          <Alert>
            <AlertDescription className="space-y-2">
              <p className="text-sm font-medium">
                Your new feed token — shown once. Copy it now.
              </p>
              <div className="flex items-center gap-2">
                <Input
                  readOnly
                  value={secret}
                  onFocus={(e) => e.target.select()}
                  className="font-mono text-xs"
                  aria-label="Feed token"
                />
                <Button size="sm" variant="outline" onClick={copySecret}>
                  <Copy className="mr-1 h-3.5 w-3.5" />
                  {copied ? "Copied" : "Copy"}
                </Button>
              </div>
              <p className="text-xs text-muted-foreground">
                Append it as <code>?token=</code> to a calendar feed URL:
              </p>
              <code className="block break-all rounded bg-muted px-2 py-1 text-xs">
                {feedURLPattern(window.location.origin)}
              </code>
              <p className="text-xs text-muted-foreground">
                …or use the “Copy iCal URL” button in a project's Calendar
                view. Cycle feeds live at the same path under{" "}
                <code>/cycles/{"{id}"}</code>.
              </p>
            </AlertDescription>
          </Alert>
        )}

        {error && (
          <Alert variant="destructive">
            <AlertDescription>{error}</AlertDescription>
          </Alert>
        )}

        <Button
          variant={active ? "outline" : "default"}
          size="sm"
          onClick={() => createMutation.mutate()}
          disabled={createMutation.isPending}
        >
          {createMutation.isPending
            ? "Generating…"
            : active
              ? "Regenerate token"
              : "Generate token"}
        </Button>
        {active && (
          <p className="text-xs text-muted-foreground">
            Regenerating revokes the current token — existing subscriptions
            stop syncing until you update their URLs.
          </p>
        )}
      </CardContent>
    </Card>
  );
}
