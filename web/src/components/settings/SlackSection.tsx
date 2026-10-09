// Slack notifications section for workspace settings (C9T3).
//
// Admin-only: the parent renders this only when the viewer is a workspace
// admin (role 20) — the backend rejects members/guests with 403, so there
// is no read-only mode to render. Matches the settings page's danger-zone
// pattern (hidden entirely for non-admins).
//
// Contract summary:
// - PATCH /api/v1/workspaces/{slug} {slack_webhook_url} sets the URL
//   (must be https://hooks.slack.com/…, validated server-side); null or
//   "" clears it. The stored URL is a secret: GET/PATCH responses carry
//   only slack_configured, never the URL itself, so this form can never
//   display it back.
// - POST /api/v1/workspaces/{slug}/slack/test enqueues a probe message.
//
// Honest scope note (speculative-free): this is a Slack *incoming
// webhook* — one-way POSTs from glance to a channel. There is no Slack
// app, no OAuth, and no interactive message buttons.

import { useState } from "react";
import { MessageSquare, Send } from "lucide-react";
import { api, ApiError } from "../../lib/api";
import { Button } from "../ui/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "../ui/card";
import { Input } from "../ui/input";
import { Label } from "../ui/label";
import { Alert, AlertDescription } from "../ui/alert";
import { Badge } from "../ui/badge";
import { toast } from "../ui/toast";

const SLACK_PREFIX = "https://hooks.slack.com/";

function urlError(raw: string): string | null {
  const url = raw.trim();
  if (!url) return "Paste the webhook URL from Slack first.";
  if (!url.startsWith(SLACK_PREFIX))
    return `The URL must start with ${SLACK_PREFIX}`;
  return null;
}

export default function SlackSection({
  slug,
  initialConfigured,
}: {
  slug: string;
  initialConfigured: boolean;
}) {
  const wsPath = `/api/v1/workspaces/${encodeURIComponent(slug)}`;
  const [configured, setConfigured] = useState(initialConfigured);
  const [url, setUrl] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);
  const [testing, setTesting] = useState(false);

  async function onSave(e: React.FormEvent) {
    e.preventDefault();
    const err = urlError(url);
    if (err) {
      setError(err);
      return;
    }
    setError(null);
    setSaving(true);
    try {
      const updated = await api.patch<{ slack_configured: boolean }>(
        wsPath,
        { slack_webhook_url: url.trim() },
      );
      setConfigured(updated.slack_configured);
      setUrl("");
      toast.add({ title: "Slack webhook saved", type: "success" });
    } catch (e) {
      const msg =
        e instanceof ApiError ? e.message : "Failed to save webhook URL";
      setError(msg);
      toast.add({ title: "Failed to save", description: msg, type: "error" });
    } finally {
      setSaving(false);
    }
  }

  async function onClear() {
    if (
      !window.confirm(
        "Remove the Slack webhook URL? Notifications to Slack will stop.",
      )
    )
      return;
    setError(null);
    setSaving(true);
    try {
      const updated = await api.patch<{ slack_configured: boolean }>(wsPath, {
        slack_webhook_url: null,
      });
      setConfigured(updated.slack_configured);
      setUrl("");
      toast.add({ title: "Slack webhook removed", type: "success" });
    } catch (e) {
      const msg =
        e instanceof ApiError ? e.message : "Failed to remove webhook URL";
      setError(msg);
      toast.add({ title: "Failed to remove", description: msg, type: "error" });
    } finally {
      setSaving(false);
    }
  }

  async function onTest() {
    setError(null);
    setTesting(true);
    try {
      await api.post<{ ok: boolean }>(`${wsPath}/slack/test`, {});
      toast.add({
        title: "Test message sent",
        description: "Check the Slack channel for the probe message.",
        type: "success",
      });
    } catch (e) {
      const msg =
        e instanceof ApiError ? e.message : "Failed to send test message";
      setError(msg);
      toast.add({
        title: "Failed to send test message",
        description: msg,
        type: "error",
      });
    } finally {
      setTesting(false);
    }
  }

  return (
    <Card className="mb-4">
      <CardHeader>
        <div className="flex items-center justify-between gap-3">
          <div>
            <CardTitle className="flex items-center gap-2">
              <MessageSquare className="h-4 w-4" />
              Slack
            </CardTitle>
            <CardDescription>
              Post issue notifications to a Slack channel.
            </CardDescription>
          </div>
          <Badge variant={configured ? "default" : "secondary"}>
            {configured ? "Configured" : "Not configured"}
          </Badge>
        </div>
      </CardHeader>
      <CardContent className="space-y-4">
        <Alert>
          <AlertDescription>
            This is a Slack <span className="font-medium">incoming webhook</span>,
            not a Slack app: glance POSTs plain-text notifications (issue
            created, state changed, commented, @mentioned) to the URL you
            paste. No OAuth, no interactive buttons. Create one in Slack
            under <span className="font-medium">Incoming Webhooks</span> and
            paste the URL below — glance only accepts{" "}
            <span className="font-mono text-xs">{SLACK_PREFIX}</span> URLs.
          </AlertDescription>
        </Alert>
        {configured && (
          <p className="text-xs text-muted-foreground">
            A webhook URL is configured. The stored URL is never shown back —
            saving a new one replaces it.
          </p>
        )}
        <form onSubmit={onSave} className="space-y-3">
          <div className="space-y-2">
            <Label htmlFor="slack-url">Incoming webhook URL</Label>
            <Input
              id="slack-url"
              value={url}
              onChange={(e) => setUrl(e.target.value)}
              placeholder="https://hooks.slack.com/services/…"
              inputMode="url"
              autoComplete="off"
              spellCheck={false}
              className="font-mono text-xs"
            />
          </div>
          {error && (
            <Alert variant="destructive">
              <AlertDescription>{error}</AlertDescription>
            </Alert>
          )}
          <div className="flex flex-wrap items-center gap-2">
            <Button type="submit" disabled={saving}>
              {saving ? "Saving…" : "Save"}
            </Button>
            {configured && (
              <>
                <Button
                  type="button"
                  variant="outline"
                  disabled={saving || testing}
                  onClick={onClear}
                >
                  Remove
                </Button>
                <Button
                  type="button"
                  variant="outline"
                  disabled={saving || testing}
                  onClick={onTest}
                >
                  <Send className="h-4 w-4" />
                  {testing ? "Sending…" : "Send test message"}
                </Button>
              </>
            )}
          </div>
        </form>
      </CardContent>
    </Card>
  );
}
