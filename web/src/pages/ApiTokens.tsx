import { useReducer, useState, type FormEvent } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { CheckIcon, CopyIcon, PlusIcon, Trash2Icon } from "lucide-react";
import { ApiError } from "../lib/api";
import {
  type ApiToken,
  type CreatedToken,
  EXPIRY_CHOICES,
  TOKEN_QUERY_KEY,
  TOKEN_SCOPES,
  confirmRevoke,
  createToken,
  expiresAtForChoice,
  fetchTokens,
  initialSecretRevealState,
  revokeToken,
  scopeLabel,
  secretRevealReducer,
  validateCreateTokenForm,
} from "../lib/tokens";
import { relativeTime } from "../lib/relativeTime";
import { toast } from "../components/ui/toast";
import { Button } from "../components/ui/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "../components/ui/card";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "../components/ui/table";
import { Badge } from "../components/ui/badge";
import { Input } from "../components/ui/input";
import { Label } from "../components/ui/label";
import { Checkbox } from "../components/ui/checkbox";
import {
  NativeSelect,
  NativeSelectOption,
} from "../components/ui/native-select";
import { Skeleton } from "../components/ui/skeleton";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "../components/ui/dialog";

async function copyToClipboard(text: string): Promise<boolean> {
  try {
    await navigator.clipboard.writeText(text);
    return true;
  } catch {
    // Clipboard API unavailable (permissions, insecure context): fall back
    // to the legacy execCommand path so the user can still grab the secret.
    try {
      const ta = document.createElement("textarea");
      ta.value = text;
      ta.style.position = "fixed";
      ta.style.opacity = "0";
      document.body.appendChild(ta);
      ta.select();
      const ok = document.execCommand("copy");
      document.body.removeChild(ta);
      return ok;
    } catch {
      return false;
    }
  }
}

function isExpired(token: ApiToken, now: number): boolean {
  if (!token.expires_at) return false;
  return new Date(token.expires_at).getTime() <= now;
}

function formatDate(iso: string): string {
  return new Date(iso).toLocaleDateString("en-US", {
    month: "short",
    day: "numeric",
    year: "numeric",
  });
}

export default function ApiTokens() {
  const queryClient = useQueryClient();
  const [createOpen, setCreateOpen] = useState(false);
  const [secretState, dispatchSecret] = useReducer(
    secretRevealReducer,
    initialSecretRevealState,
  );
  const [createdName, setCreatedName] = useState<string | null>(null);
  const [copied, setCopied] = useState(false);
  const [revokingId, setRevokingId] = useState<string | null>(null);

  // Create form state
  const [name, setName] = useState("");
  const [scopes, setScopes] = useState<string[]>(["read"]);
  const [expiryId, setExpiryId] = useState("never");
  const [formError, setFormError] = useState<string | null>(null);

  const tokensQuery = useQuery({
    queryKey: TOKEN_QUERY_KEY,
    queryFn: fetchTokens,
  });

  const createMutation = useMutation({
    mutationFn: createToken,
    onSuccess: (created: CreatedToken) => {
      setCreatedName(created.name);
      setCopied(false);
      dispatchSecret({ type: "created", token: created.token });
      setCreateOpen(false);
      setName("");
      setScopes(["read"]);
      setExpiryId("never");
      setFormError(null);
      void queryClient.invalidateQueries({ queryKey: TOKEN_QUERY_KEY });
    },
    onError: (e) => {
      setFormError(e instanceof ApiError ? e.message : "Failed to create token");
    },
  });

  const revokeMutation = useMutation({
    mutationFn: revokeToken,
    onSuccess: () => {
      toast.add({ title: "Token revoked", type: "success" });
      setRevokingId(null);
      void queryClient.invalidateQueries({ queryKey: TOKEN_QUERY_KEY });
    },
    onError: (e) => {
      toast.add({
        title: e instanceof ApiError ? e.message : "Failed to revoke token",
        type: "error",
      });
      setRevokingId(null);
    },
  });

  function toggleScope(scope: string, checked: boolean | string) {
    setScopes((prev) =>
      checked === true
        ? [...prev, scope]
        : prev.filter((s) => s !== scope),
    );
  }

  function openCreate() {
    setFormError(null);
    createMutation.reset();
    setCreateOpen(true);
  }

  function onCreate(e: FormEvent) {
    e.preventDefault();
    const error = validateCreateTokenForm(name, scopes);
    if (error) {
      setFormError(error);
      return;
    }
    const choice = EXPIRY_CHOICES.find((c) => c.id === expiryId) ?? EXPIRY_CHOICES[0];
    const expires_at = expiresAtForChoice(choice);
    createMutation.mutate({
      name: name.trim(),
      scopes,
      ...(expires_at ? { expires_at } : {}),
    });
  }

  function onRevoke(token: ApiToken) {
    // House pattern: window.confirm before destructive actions.
    if (!confirmRevoke(token.name, window.confirm)) return;
    setRevokingId(token.id);
    revokeMutation.mutate(token.id);
  }

  async function onCopySecret() {
    if (!secretState.secret) return;
    const ok = await copyToClipboard(secretState.secret);
    if (ok) {
      setCopied(true);
      toast.add({ title: "Token copied", type: "success" });
    } else {
      toast.add({
        title: "Copy failed — select the token text manually",
        type: "error",
      });
    }
  }

  function dismissSecret() {
    // The secret is dropped from state here; the list endpoint never carries
    // it, so closing this view is the last time it is ever visible.
    dispatchSecret({ type: "dismissed" });
    setCreatedName(null);
  }

  const tokens = tokensQuery.data ?? [];
  const now = Date.now();

  return (
    <div className="mx-auto max-w-4xl px-4 py-8">
      <div className="mb-6 flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-semibold tracking-tight">API tokens</h1>
          <p className="mt-1 text-sm text-muted-foreground">
            Personal tokens for API and CLI access. They act as you — keep them
            secret.
          </p>
        </div>
        <Button onClick={openCreate}>
          <PlusIcon className="h-4 w-4" />
          New token
        </Button>
      </div>

      <Card>
        <CardHeader>
          <CardTitle>Tokens</CardTitle>
          <CardDescription>
            Last used times are updated when a token authenticates a request.
          </CardDescription>
        </CardHeader>
        <CardContent>
          {tokensQuery.isLoading && (
            <div className="space-y-2">
              {Array.from({ length: 3 }).map((_, i) => (
                <Skeleton key={i} className="h-10 w-full" />
              ))}
            </div>
          )}
          {tokensQuery.isError && (
            <p className="text-sm text-destructive">
              {tokensQuery.error instanceof ApiError
                ? tokensQuery.error.message
                : "Failed to load tokens"}
            </p>
          )}
          {!tokensQuery.isLoading && !tokensQuery.isError && tokens.length === 0 && (
            <p className="py-8 text-center text-sm text-muted-foreground">
              No tokens yet. Create one to authenticate API and CLI calls.
            </p>
          )}
          {!tokensQuery.isLoading && !tokensQuery.isError && tokens.length > 0 && (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Name</TableHead>
                  <TableHead>Scopes</TableHead>
                  <TableHead>Created</TableHead>
                  <TableHead>Last used</TableHead>
                  <TableHead>Expires</TableHead>
                  <TableHead className="text-right">Actions</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {tokens.map((t) => {
                  const expired = isExpired(t, now);
                  return (
                    <TableRow key={t.id}>
                      <TableCell className="font-medium">
                        <div className="flex items-center gap-2">
                          {t.name}
                          {expired && (
                            <Badge variant="destructive">expired</Badge>
                          )}
                        </div>
                      </TableCell>
                      <TableCell>
                        <div className="flex flex-wrap gap-1">
                          {t.scopes.map((s) => (
                            <Badge key={s} variant="secondary">
                              {s}
                            </Badge>
                          ))}
                        </div>
                      </TableCell>
                      <TableCell className="text-muted-foreground">
                        {relativeTime(t.created_at)}
                      </TableCell>
                      <TableCell className="text-muted-foreground">
                        {t.last_used_at ? relativeTime(t.last_used_at) : "Never"}
                      </TableCell>
                      <TableCell className="text-muted-foreground">
                        {t.expires_at ? formatDate(t.expires_at) : "Never"}
                      </TableCell>
                      <TableCell className="text-right">
                        <Button
                          variant="ghost"
                          size="sm"
                          disabled={revokingId === t.id}
                          onClick={() => onRevoke(t)}
                          aria-label={`Revoke token ${t.name}`}
                        >
                          <Trash2Icon className="h-4 w-4" />
                          <span className="sr-only">Revoke</span>
                        </Button>
                      </TableCell>
                    </TableRow>
                  );
                })}
              </TableBody>
            </Table>
          )}
        </CardContent>
      </Card>

      {/* Create dialog */}
      <Dialog
        open={createOpen}
        onOpenChange={(open) => {
          if (!open) {
            createMutation.reset();
            setFormError(null);
          }
          setCreateOpen(open);
        }}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>New API token</DialogTitle>
            <DialogDescription>
              Give the token a name you will recognize later — it is shown in
              the list, the secret itself only once.
            </DialogDescription>
          </DialogHeader>
          <form onSubmit={onCreate} className="space-y-4">
            <div className="space-y-2">
              <Label htmlFor="token-name">Name</Label>
              <Input
                id="token-name"
                value={name}
                onChange={(e) => setName(e.target.value)}
                placeholder="e.g. deploy bot"
                autoFocus
              />
            </div>
            <div className="space-y-2">
              <Label>Scopes</Label>
              <div className="space-y-2">
                {TOKEN_SCOPES.map((s) => (
                  <label
                    key={s}
                    className="flex cursor-pointer items-start gap-2 text-sm"
                  >
                    <Checkbox
                      checked={scopes.includes(s)}
                      onCheckedChange={(checked) => toggleScope(s, checked)}
                    />
                    <span>
                      <span className="font-medium">{s}</span>
                      <span className="text-muted-foreground">
                        {" — "}
                        {scopeLabel(s)}
                      </span>
                    </span>
                  </label>
                ))}
              </div>
            </div>
            <div className="space-y-2">
              <Label htmlFor="token-expiry">Expires</Label>
              <NativeSelect
                id="token-expiry"
                value={expiryId}
                onChange={(e) => setExpiryId(e.target.value)}
              >
                {EXPIRY_CHOICES.map((c) => (
                  <NativeSelectOption key={c.id} value={c.id}>
                    {c.label}
                  </NativeSelectOption>
                ))}
              </NativeSelect>
            </div>
            {formError && (
              <p className="text-sm text-destructive">{formError}</p>
            )}
            <DialogFooter>
              <Button
                type="button"
                variant="outline"
                onClick={() => setCreateOpen(false)}
              >
                Cancel
              </Button>
              <Button type="submit" disabled={createMutation.isPending}>
                {createMutation.isPending ? "Creating…" : "Create token"}
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>

      {/* Show-once secret dialog */}
      <Dialog
        open={secretState.showing}
        onOpenChange={(open) => {
          if (!open) dismissSecret();
        }}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Token created{createdName ? ` — ${createdName}` : ""}</DialogTitle>
            <DialogDescription>
              Copy now — it will never be shown again.
            </DialogDescription>
          </DialogHeader>
          <div className="space-y-3">
            <div className="flex items-center gap-2 rounded-lg border bg-muted p-3">
              <code className="flex-1 break-all font-mono text-sm select-all">
                {secretState.secret}
              </code>
              <Button variant="outline" size="sm" onClick={onCopySecret}>
                {copied ? (
                  <CheckIcon className="h-4 w-4" />
                ) : (
                  <CopyIcon className="h-4 w-4" />
                )}
                {copied ? "Copied" : "Copy"}
              </Button>
            </div>
            <p className="text-sm text-muted-foreground">
              This token acts as you. Store it in your password manager or CI
              secrets. The glance API will never display it again — if you lose
              it, revoke this token and create a new one.
            </p>
          </div>
          <DialogFooter>
            <Button onClick={dismissSecret}>Done</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}
