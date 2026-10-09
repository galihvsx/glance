import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Check, Copy, Link2, Trash2, X } from "lucide-react";

import { api } from "../lib/api";
import { Button } from "./ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "./ui/card";

/**
 * Share-link modal (C4T5). Creates/lists/revokes public share links for
 * an issue or a page. Anyone with the link can view the sanitized
 * payload — no login required.
 */

export interface ShareLink {
  id: string;
  token: string;
  scope: "issue" | "page";
  expires_at?: string | null;
  revoked_at?: string | null;
  created_at: string;
}

type Expiry = "never" | "7d" | "30d";

export default function ShareModal({
  resource,
  onClose,
}: {
  /** e.g. `/api/v1/workspaces/{slug}/projects/{id}/issues/{uuid}/share` */
  resource: string;
  onClose: () => void;
}) {
  const queryClient = useQueryClient();
  const [expiry, setExpiry] = useState<Expiry>("never");
  const [copied, setCopied] = useState<string | null>(null);
  const key = ["share-links", resource];

  const listQuery = useQuery({
    queryKey: key,
    queryFn: () => api.get<{ links: ShareLink[] }>(resource),
  });
  const links = listQuery.data?.links ?? [];

  const createMutation = useMutation({
    mutationFn: () => {
      const body: { expires_at?: string } = {};
      if (expiry !== "never") {
        const days = expiry === "7d" ? 7 : 30;
        body.expires_at = new Date(Date.now() + days * 864e5).toISOString();
      }
      return api.post<{ token: string; url: string }>(resource, body);
    },
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: key }),
  });

  const revokeMutation = useMutation({
    mutationFn: (token: string) =>
      api.del(`${resource}/${encodeURIComponent(token)}`),
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: key }),
  });

  const shareUrl = (token: string) =>
    `${window.location.origin}/s/${encodeURIComponent(token)}`;

  const copy = async (token: string) => {
    try {
      await navigator.clipboard.writeText(shareUrl(token));
      setCopied(token);
      setTimeout(() => setCopied((c) => (c === token ? null : c)), 2000);
    } catch {
      // Clipboard unavailable — the input is selectable as fallback.
    }
  };

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-4"
      onClick={onClose}
      role="dialog"
      aria-modal="true"
      aria-label="Share link"
    >
      <Card
        className="w-full max-w-md"
        onClick={(e) => e.stopPropagation()}
      >
        <CardHeader className="flex flex-row items-center justify-between">
          <CardTitle className="flex items-center gap-2">
            <Link2 className="h-4 w-4" /> Share publicly
          </CardTitle>
          <Button variant="ghost" size="icon" onClick={onClose} aria-label="Close">
            <X className="h-4 w-4" />
          </Button>
        </CardHeader>
        <CardContent className="space-y-4">
          <p className="text-sm text-muted-foreground">
            Anyone with the link can view a read-only copy — no login
            required. Internal IDs and emails are never included.
          </p>

          <div className="flex items-end gap-2">
            <label className="flex-1 space-y-1">
              <span className="text-xs font-medium">Expires</span>
              <select
                value={expiry}
                onChange={(e) => setExpiry(e.target.value as Expiry)}
                className="w-full rounded-md border bg-background px-2 py-1.5 text-sm"
              >
                <option value="never">Never</option>
                <option value="7d">7 days</option>
                <option value="30d">30 days</option>
              </select>
            </label>
            <Button
              onClick={() => createMutation.mutate()}
              disabled={createMutation.isPending}
            >
              {createMutation.isPending ? "Creating…" : "Create link"}
            </Button>
          </div>

          {listQuery.isLoading ? (
            <p className="text-sm text-muted-foreground">Loading links…</p>
          ) : links.length === 0 ? (
            <p className="text-sm text-muted-foreground">
              No active links yet.
            </p>
          ) : (
            <ul className="space-y-2">
              {links.map((l) => (
                <li
                  key={l.id}
                  className="flex items-center gap-2 rounded-md border px-2 py-1.5"
                >
                  <input
                    readOnly
                    value={shareUrl(l.token)}
                    onFocus={(e) => e.target.select()}
                    className="min-w-0 flex-1 bg-transparent text-xs"
                    aria-label="Share link URL"
                  />
                  <span className="shrink-0 text-xs text-muted-foreground">
                    {l.expires_at
                      ? `expires ${new Date(l.expires_at).toLocaleDateString()}`
                      : "never expires"}
                  </span>
                  <Button
                    variant="ghost"
                    size="icon"
                    onClick={() => copy(l.token)}
                    aria-label="Copy link"
                  >
                    {copied === l.token ? (
                      <Check className="h-4 w-4 text-green-600" />
                    ) : (
                      <Copy className="h-4 w-4" />
                    )}
                  </Button>
                  <Button
                    variant="ghost"
                    size="icon"
                    onClick={() => {
                      if (window.confirm("Revoke this link?")) {
                        revokeMutation.mutate(l.token);
                      }
                    }}
                    aria-label="Revoke link"
                  >
                    <Trash2 className="h-4 w-4 text-destructive" />
                  </Button>
                </li>
              ))}
            </ul>
          )}
        </CardContent>
      </Card>
    </div>
  );
}
