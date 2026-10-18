// GitHub integration section for workspace settings (C62T0).
//
// Admin-only: the parent renders this only when the viewer is a workspace
// admin (role 20) — the backend rejects members/guests with 403, so there
// is no read-only mode to render. Matches the settings page's danger-zone
// pattern (hidden entirely for non-admins).
//
// Contract summary (C17T0):
// - PATCH /api/v1/workspaces/{slug} {github_webhook_secret} sets the
//   secret (tri-state: omitted key = leave, explicit null = clear, string
//   = set, validated server-side as 1-256 printable-ASCII chars without
//   whitespace). The stored secret is a secret: GET/PATCH responses carry
//   only github_webhook_configured, never the secret itself, so this form
//   can never display it back.
// - The repo admin registers a repo webhook pointing at the inbound URL
//   below; GitHub signs deliveries with X-Hub-Signature-256 using the
//   secret saved here.
//
// Honest scope note (speculative-free): this is auto-link only — a
// pull_request/opened delivery links the PR to the issue when the PR
// title or body mentions the issue's display ID (GLC-123), and a closed
// delivery only updates the linked ref's status (merged/closed). glance
// never moves issues between states from a PR event.

import { useState } from "react";
import { Check, Copy, GitPullRequest } from "lucide-react";
import { api, ApiError } from "../../lib/api";
import { copyText } from "../../lib/clipboard";
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

const WEBHOOK_PATH = "/api/v1/integrations/github/webhook";

// Mirrors the backend contract (validateGitHubWebhookSecret): 1-256 chars,
// printable ASCII (0x21-0x7E) — no whitespace. The backend also trims, so
// validate (and send) the trimmed value.
function secretError(raw: string): string | null {
  const secret = raw.trim();
  if (!secret) return "Paste the webhook secret from GitHub first.";
  if (secret.length > 256 || !/^[!-~]+$/.test(secret))
    return "The secret must be 1–256 printable ASCII characters with no spaces.";
  return null;
}

export default function GitHubSection({
  slug,
  initialConfigured,
}: {
  slug: string;
  initialConfigured: boolean;
}) {
  const wsPath = `/api/v1/workspaces/${encodeURIComponent(slug)}`;
  const webhookUrl = `${window.location.origin}${WEBHOOK_PATH}`;
  const [configured, setConfigured] = useState(initialConfigured);
  const [secret, setSecret] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);
  const [copied, setCopied] = useState(false);

  async function onSave(e: React.FormEvent) {
    e.preventDefault();
    const err = secretError(secret);
    if (err) {
      setError(err);
      return;
    }
    setError(null);
    setSaving(true);
    try {
      const updated = await api.patch<{ github_webhook_configured: boolean }>(
        wsPath,
        { github_webhook_secret: secret.trim() },
      );
      setConfigured(updated.github_webhook_configured);
      setSecret("");
      toast.add({ title: "GitHub webhook secret saved", type: "success" });
    } catch (e) {
      const msg =
        e instanceof ApiError ? e.message : "Failed to save webhook secret";
      setError(msg);
      toast.add({ title: "Failed to save", description: msg, type: "error" });
    } finally {
      setSaving(false);
    }
  }

  async function onClear() {
    if (
      !window.confirm(
        "Remove the GitHub webhook secret? PR auto-linking will stop working until a new secret is saved and registered in GitHub.",
      )
    )
      return;
    setError(null);
    setSaving(true);
    try {
      const updated = await api.patch<{ github_webhook_configured: boolean }>(
        wsPath,
        {
          github_webhook_secret: null,
        },
      );
      setConfigured(updated.github_webhook_configured);
      setSecret("");
      toast.add({ title: "GitHub webhook secret removed", type: "success" });
    } catch (e) {
      const msg =
        e instanceof ApiError ? e.message : "Failed to remove webhook secret";
      setError(msg);
      toast.add({ title: "Failed to remove", description: msg, type: "error" });
    } finally {
      setSaving(false);
    }
  }

  async function onCopy() {
    if (await copyText(webhookUrl)) {
      setCopied(true);
      window.setTimeout(() => setCopied(false), 2000);
    }
  }

  return (
    <Card className="mb-4 rounded-lg border-border-hairline">
      <CardHeader>
        <div className="flex items-center justify-between gap-3">
          <div>
            <CardTitle className="flex items-center gap-2 text-xs font-semibold">
              <GitPullRequest className="h-4 w-4" />
              GitHub
            </CardTitle>
            <CardDescription className="text-xs">
              Auto-link pull requests to issues via a repo webhook.
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
            <span className="font-medium">Auto-link only.</span> When a pull
            request&apos;s title or body mentions an issue&apos;s display ID
            (e.g. <span className="font-mono text-xs">GLC-123</span>), glance
            links the PR to the issue; closing or merging the PR only updates
            that link&apos;s status. glance{" "}
            <span className="font-medium">never moves issues between states</span>{" "}
            from a PR event.
          </AlertDescription>
        </Alert>

        <div className="space-y-2">
          <Label>Inbound webhook URL</Label>
          <div className="flex items-center gap-2">
            <code className="min-w-0 flex-1 truncate rounded-md bg-muted px-3 py-2 font-mono text-xs">
              {webhookUrl}
            </code>
            <Button type="button" variant="outline" onClick={onCopy}>
              {copied ? (
                <Check className="h-4 w-4" />
              ) : (
                <Copy className="h-4 w-4" />
              )}
              {copied ? "Copied" : "Copy"}
            </Button>
          </div>
        </div>

        <div className="space-y-1.5 text-xs text-muted-foreground">
          <p className="font-medium text-foreground">
            Register this in the GitHub repo (Settings → Webhooks → Add
            webhook):
          </p>
          <ul className="list-disc space-y-1 pl-5">
            <li>
              <span className="font-medium">Payload URL:</span> the URL above
            </li>
            <li>
              <span className="font-medium">Content type:</span>{" "}
              <span className="font-mono">application/json</span>
            </li>
            <li>
              <span className="font-medium">Secret:</span> the secret you save
              below — GitHub HMAC-signs each delivery with it
            </li>
            <li>
              <span className="font-medium">Events:</span> “Let me select
              individual events” → check{" "}
              <span className="font-medium">Pull requests</span>
            </li>
          </ul>
        </div>

        {configured && (
          <p className="text-xs text-muted-foreground">
            A webhook secret is configured. The stored secret is never shown
            back — saving a new one replaces it.
          </p>
        )}
        <form onSubmit={onSave} className="space-y-3">
          <div className="space-y-2">
            <Label htmlFor="github-secret">Webhook secret</Label>
            <Input
              id="github-secret"
              type="password"
              value={secret}
              onChange={(e) => setSecret(e.target.value)}
              placeholder="Paste the secret you registered in GitHub"
              autoComplete="new-password"
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
              <Button
                type="button"
                variant="outline"
                disabled={saving}
                onClick={onClear}
              >
                Remove
              </Button>
            )}
          </div>
        </form>
      </CardContent>
    </Card>
  );
}
