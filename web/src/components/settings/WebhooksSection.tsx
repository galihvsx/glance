// Webhooks management section for workspace settings (C6T1).
//
// Admin-only: the parent renders this only when the viewer is a workspace
// admin (role 20) — the backend rejects members/guests with 403, so there
// is no read-only mode to render. Matches the settings page's danger-zone
// pattern (hidden entirely for non-admins).
//
// Contract summary (see lib/webhooks.ts): list/get never carry the secret;
// create returns it once; PATCH never returns it. Regenerate is done
// client-side: a fresh 32-byte hex secret is generated and PATCHed, then
// shown once like a create.

import { useReducer, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  Check,
  Copy,
  Pencil,
  Plus,
  RefreshCw,
  Trash2,
  Webhook as WebhookIcon,
} from "lucide-react";
import { ApiError } from "../../lib/api";
import {
  buildCreateBody,
  createWebhook,
  deleteWebhook,
  eventLabel,
  generateWebhookSecret,
  listWebhooks,
  revealReducer,
  toggleEvent,
  updateWebhook,
  validateWebhookUrl,
  WEBHOOK_EVENTS,
  type Webhook,
  type WebhookCreateBody,
  type WebhookUpdateBody,
} from "../../lib/webhooks";
import { relativeTime } from "../../lib/relativeTime";
import { formatDateTime } from "../../lib/format";
import { Badge } from "../ui/badge";
import { Button } from "../ui/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "../ui/card";
import { Checkbox } from "../ui/checkbox";
import { Input } from "../ui/input";
import { Label } from "../ui/label";
import { Skeleton } from "../ui/skeleton";
import { Switch } from "../ui/switch";
import { Alert, AlertDescription } from "../ui/alert";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "../ui/dialog";
import { toast } from "../ui/toast";

const WEBHOOKS_KEY = (slug: string) => ["webhooks", slug];

function mutationError(e: unknown, fallback: string): string {
  return e instanceof ApiError ? e.message : fallback;
}

function EventsCheckboxes({
  selected,
  onChange,
}: {
  selected: string[];
  onChange: (next: string[]) => void;
}) {
  return (
    <fieldset className="space-y-2">
      <legend className="text-sm font-medium">Events</legend>
      <p className="text-xs text-muted-foreground">
        No events selected means the webhook receives every event.
      </p>
      <div className="grid gap-2 sm:grid-cols-2">
        {WEBHOOK_EVENTS.map((opt) => (
          <label
            key={opt.value}
            className="flex cursor-pointer items-start gap-2.5 rounded-md border border-border p-2.5 hover:bg-muted/50"
          >
            <Checkbox
              checked={selected.includes(opt.value)}
              onCheckedChange={() =>
                onChange(toggleEvent(selected, opt.value))
              }
              aria-label={opt.label}
            />
            <span>
              <span className="block text-sm font-medium">{opt.label}</span>
              <span className="block text-xs text-muted-foreground">
                {opt.hint}
              </span>
            </span>
          </label>
        ))}
      </div>
    </fieldset>
  );
}

function SecretRevealView({
  kind,
  secret,
  onDone,
}: {
  kind: "create" | "regenerate";
  secret: string;
  onDone: () => void;
}) {
  const [copied, setCopied] = useState(false);

  async function copy() {
    try {
      await navigator.clipboard.writeText(secret);
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    } catch {
      // Clipboard unavailable — the field below is selectable as fallback.
    }
  }

  return (
    <div className="space-y-4">
      <Alert>
        <AlertDescription>
          <span className="font-medium">
            {kind === "create" ? "Webhook created." : "New secret generated."}
          </span>{" "}
          Copy the signing secret now — this is the only time it will be
          shown. It cannot be retrieved again.
        </AlertDescription>
      </Alert>
      <div className="flex items-center gap-2">
        <Input
          value={secret}
          readOnly
          onFocus={(e) => e.target.select()}
          className="font-mono text-xs"
          aria-label="Signing secret"
        />
        <Button type="button" variant="outline" onClick={copy}>
          {copied ? <Check className="h-4 w-4" /> : <Copy className="h-4 w-4" />}
          {copied ? "Copied" : "Copy"}
        </Button>
      </div>
      <p className="text-xs text-muted-foreground">
        Glance signs every delivery with HMAC-SHA256 under this secret
        (header <span className="font-mono">X-Glance-Signature</span>).
      </p>
      <DialogFooter>
        <Button type="button" onClick={onDone}>
          Done
        </Button>
      </DialogFooter>
    </div>
  );
}

export default function WebhooksSection({ slug }: { slug: string }) {
  const queryClient = useQueryClient();
  const [reveal, dispatchReveal] = useReducer(revealReducer, null);

  const webhooksQuery = useQuery({
    queryKey: WEBHOOKS_KEY(slug),
    queryFn: () => listWebhooks(slug),
  });
  const webhooks = webhooksQuery.data ?? [];

  const invalidate = () =>
    void queryClient.invalidateQueries({ queryKey: WEBHOOKS_KEY(slug) });

  // ---------- mutations ----------

  const createMutation = useMutation({
    mutationFn: (body: WebhookCreateBody) => createWebhook(slug, body),
    onSuccess: (resp) => {
      dispatchReveal({ type: "revealed", kind: "create", secret: resp.secret });
      toast.add({ title: "Webhook created", type: "success" });
    },
    onError: (e) =>
      toast.add({
        title: "Failed to create webhook",
        description: mutationError(e, "Failed to create webhook"),
        type: "error",
      }),
    onSettled: invalidate,
  });

  const updateMutation = useMutation({
    mutationFn: ({ id, body }: { id: string; body: WebhookUpdateBody }) =>
      updateWebhook(slug, id, body),
    onError: (e) =>
      toast.add({
        title: "Failed to update webhook",
        description: mutationError(e, "Failed to update webhook"),
        type: "error",
      }),
    onSettled: invalidate,
  });

  const deleteMutation = useMutation({
    mutationFn: (id: string) => deleteWebhook(slug, id),
    onSuccess: () =>
      toast.add({ title: "Webhook deleted", type: "success" }),
    onError: (e) =>
      toast.add({
        title: "Failed to delete webhook",
        description: mutationError(e, "Failed to delete webhook"),
        type: "error",
      }),
    onSettled: invalidate,
  });

  // ---------- create dialog state ----------

  const [createOpen, setCreateOpen] = useState(false);
  const [createUrl, setCreateUrl] = useState("");
  const [createEvents, setCreateEvents] = useState<string[]>([]);
  const [createSecret, setCreateSecret] = useState("");
  const [createError, setCreateError] = useState<string | null>(null);

  function openCreate() {
    setCreateUrl("");
    setCreateEvents([]);
    setCreateSecret("");
    setCreateError(null);
    setCreateOpen(true);
  }

  function closeCreate() {
    setCreateOpen(false);
    dispatchReveal({ type: "dismissed" }); // the secret must not linger
  }

  function onCreate(e: React.FormEvent) {
    e.preventDefault();
    const urlErr = validateWebhookUrl(createUrl);
    if (urlErr) {
      setCreateError(urlErr);
      return;
    }
    setCreateError(null);
    createMutation.mutate(
      buildCreateBody(createUrl, createEvents, createSecret, true),
    );
  }

  // ---------- edit dialog state ----------

  const [editing, setEditing] = useState<Webhook | null>(null);
  const [editUrl, setEditUrl] = useState("");
  const [editEvents, setEditEvents] = useState<string[]>([]);
  const [editActive, setEditActive] = useState(true);
  const [editError, setEditError] = useState<string | null>(null);

  function openEdit(w: Webhook) {
    setEditing(w);
    setEditUrl(w.url);
    setEditEvents(w.events ?? []);
    setEditActive(w.active);
    setEditError(null);
  }

  function closeEdit() {
    setEditing(null);
    dispatchReveal({ type: "dismissed" });
  }

  function onEdit(e: React.FormEvent) {
    e.preventDefault();
    if (!editing) return;
    const urlErr = validateWebhookUrl(editUrl);
    if (urlErr) {
      setEditError(urlErr);
      return;
    }
    setEditError(null);
    updateMutation.mutate(
      {
        id: editing.id,
        body: {
          url: editUrl.trim(),
          events: [...editEvents],
          active: editActive,
        },
      },
      {
        onSuccess: () => {
          setEditing(null);
          toast.add({ title: "Webhook updated", type: "success" });
        },
      },
    );
  }

  function onRegenerate() {
    if (!editing) return;
    // Generated client-side and PATCHed: deterministic whether or not the
    // deferred-minors empty-secret change (C6T5 T26) has landed.
    const secret = generateWebhookSecret();
    updateMutation.mutate(
      { id: editing.id, body: { secret } },
      {
        onSuccess: () => {
          dispatchReveal({
            type: "revealed",
            kind: "regenerate",
            secret,
          });
          setEditing(null);
        },
      },
    );
  }

  function onDelete(w: Webhook) {
    // House confirm pattern (window.confirm, cf. Admin.tsx).
    if (
      !window.confirm(
        `Delete the webhook to ${w.url}? Deliveries to it will stop.`,
      )
    )
      return;
    deleteMutation.mutate(w.id);
  }

  const acting = createMutation.isPending || updateMutation.isPending;

  return (
    <Card className="mb-4">
      <CardHeader>
        <div className="flex items-center justify-between gap-3">
          <div>
            <CardTitle className="flex items-center gap-2">
              <WebhookIcon className="h-4 w-4" />
              Webhooks
            </CardTitle>
            <CardDescription>
              POST workspace events to external URLs. Deliveries are signed
              with HMAC-SHA256.
            </CardDescription>
          </div>
          <Button size="sm" onClick={openCreate}>
            <Plus className="h-4 w-4" />
            New webhook
          </Button>
        </div>
      </CardHeader>
      <CardContent>
        {webhooksQuery.isError && (
          <Alert variant="destructive" className="mb-3">
            <AlertDescription>
              {mutationError(webhooksQuery.error, "Failed to load webhooks")}
            </AlertDescription>
          </Alert>
        )}
        {webhooksQuery.isLoading ? (
          <div className="space-y-2">
            <Skeleton className="h-12 w-full" />
            <Skeleton className="h-12 w-full" />
          </div>
        ) : webhooks.length === 0 ? (
          <div className="rounded-md border border-dashed p-6 text-center">
            <p className="text-sm text-muted-foreground">
              No webhooks yet. Create one to start receiving event deliveries.
            </p>
          </div>
        ) : (
          <ul className="divide-y divide-border">
            {webhooks.map((w) => (
              <li
                key={w.id}
                className="flex items-center justify-between gap-3 py-3"
              >
                <div className="min-w-0 flex-1">
                  <p
                    className="truncate font-mono text-sm font-medium"
                    title={w.url}
                  >
                    {w.url}
                  </p>
                  <div className="mt-1.5 flex flex-wrap items-center gap-1.5">
                    {w.events.length === 0 ? (
                      <Badge variant="secondary">All events</Badge>
                    ) : (
                      w.events.map((ev) => (
                        <Badge key={ev} variant="secondary">
                          {eventLabel(ev)}
                        </Badge>
                      ))
                    )}
                    <span
                      className="text-xs text-muted-foreground"
                      title={formatDateTime(w.created_at)}
                    >
                      · created {relativeTime(w.created_at)}
                    </span>
                  </div>
                </div>
                <div className="flex shrink-0 items-center gap-2">
                  <Switch
                    checked={w.active}
                    disabled={acting}
                    onCheckedChange={(next) =>
                      updateMutation.mutate({
                        id: w.id,
                        body: { active: next },
                      })
                    }
                    aria-label={`Deliveries for ${w.url}`}
                    size="sm"
                  />
                  <Button
                    variant="ghost"
                    size="icon"
                    onClick={() => openEdit(w)}
                    aria-label={`Edit webhook ${w.url}`}
                  >
                    <Pencil className="h-4 w-4" />
                  </Button>
                  <Button
                    variant="ghost"
                    size="icon"
                    disabled={deleteMutation.isPending}
                    onClick={() => onDelete(w)}
                    aria-label={`Delete webhook ${w.url}`}
                  >
                    <Trash2 className="h-4 w-4" />
                  </Button>
                </div>
              </li>
            ))}
          </ul>
        )}
      </CardContent>

      {/* ---------- Create dialog ---------- */}
      <Dialog open={createOpen} onOpenChange={(open) => !open && closeCreate()}>
        <DialogContent className="max-h-[90vh] overflow-y-auto">
          <DialogHeader>
            <DialogTitle>New webhook</DialogTitle>
            <DialogDescription>
              Deliveries POST JSON to the URL. The signing secret is shown
              exactly once after creation.
            </DialogDescription>
          </DialogHeader>
          {reveal ? (
            <SecretRevealView
              kind={reveal.kind}
              secret={reveal.secret}
              onDone={closeCreate}
            />
          ) : (
            <form onSubmit={onCreate} className="space-y-4">
              <div className="space-y-2">
                <Label htmlFor="wh-create-url">Target URL</Label>
                <Input
                  id="wh-create-url"
                  value={createUrl}
                  onChange={(e) => setCreateUrl(e.target.value)}
                  placeholder="https://example.com/hook"
                  inputMode="url"
                  autoComplete="off"
                  required
                />
                <p className="text-xs text-muted-foreground">
                  Must be http or https. Internal and metadata addresses are
                  refused at delivery time.
                </p>
              </div>
              <EventsCheckboxes
                selected={createEvents}
                onChange={setCreateEvents}
              />
              <div className="space-y-2">
                <Label htmlFor="wh-create-secret">
                  Signing secret{" "}
                  <span className="font-normal text-muted-foreground">
                    (optional)
                  </span>
                </Label>
                <Input
                  id="wh-create-secret"
                  value={createSecret}
                  onChange={(e) => setCreateSecret(e.target.value)}
                  placeholder="Leave empty to auto-generate"
                  autoComplete="off"
                  className="font-mono text-xs"
                />
              </div>
              {createError && (
                <Alert variant="destructive">
                  <AlertDescription>{createError}</AlertDescription>
                </Alert>
              )}
              <DialogFooter>
                <Button
                  type="button"
                  variant="outline"
                  onClick={closeCreate}
                >
                  Cancel
                </Button>
                <Button type="submit" disabled={createMutation.isPending}>
                  {createMutation.isPending ? "Creating…" : "Create webhook"}
                </Button>
              </DialogFooter>
            </form>
          )}
        </DialogContent>
      </Dialog>

      {/* ---------- Edit dialog ---------- */}
      <Dialog open={editing !== null} onOpenChange={(open) => !open && closeEdit()}>
        <DialogContent className="max-h-[90vh] overflow-y-auto">
          <DialogHeader>
            <DialogTitle>Edit webhook</DialogTitle>
            <DialogDescription className="break-all font-mono text-xs">
              {editing?.url}
            </DialogDescription>
          </DialogHeader>
          {reveal ? (
            <SecretRevealView
              kind={reveal.kind}
              secret={reveal.secret}
              onDone={closeEdit}
            />
          ) : editing ? (
            <form onSubmit={onEdit} className="space-y-4">
              <div className="space-y-2">
                <Label htmlFor="wh-edit-url">Target URL</Label>
                <Input
                  id="wh-edit-url"
                  value={editUrl}
                  onChange={(e) => setEditUrl(e.target.value)}
                  inputMode="url"
                  autoComplete="off"
                  required
                />
              </div>
              <EventsCheckboxes
                selected={editEvents}
                onChange={setEditEvents}
              />
              <label className="flex cursor-pointer items-center gap-2.5">
                <Checkbox
                  checked={editActive}
                  onCheckedChange={(c) => setEditActive(c === true)}
                  aria-label="Webhook active"
                />
                <span className="text-sm font-medium">Active</span>
              </label>
              <div className="rounded-md border border-border p-3">
                <div className="flex items-center justify-between gap-3">
                  <div>
                    <p className="text-sm font-medium">Signing secret</p>
                    <p className="text-xs text-muted-foreground">
                      The current secret is never shown. Regenerating replaces
                      it and shows the new one once.
                    </p>
                  </div>
                  <Button
                    type="button"
                    variant="outline"
                    size="sm"
                    disabled={updateMutation.isPending}
                    onClick={onRegenerate}
                  >
                    <RefreshCw className="h-4 w-4" />
                    Regenerate
                  </Button>
                </div>
              </div>
              {editError && (
                <Alert variant="destructive">
                  <AlertDescription>{editError}</AlertDescription>
                </Alert>
              )}
              <DialogFooter>
                <Button type="button" variant="outline" onClick={closeEdit}>
                  Cancel
                </Button>
                <Button type="submit" disabled={updateMutation.isPending}>
                  {updateMutation.isPending ? "Saving…" : "Save changes"}
                </Button>
              </DialogFooter>
            </form>
          ) : null}
        </DialogContent>
      </Dialog>
    </Card>
  );
}
