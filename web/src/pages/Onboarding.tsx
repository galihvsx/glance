import { useState, type FormEvent } from "react";
import { Navigate, useNavigate } from "react-router-dom";
import { api, ApiError } from "../lib/api";
import type { Workspace } from "../lib/types";
import { useSeedWorkspaces, useWorkspaces } from "../lib/useWorkspaces";
import { Button } from "../components/ui/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "../components/ui/card";
import { Input } from "../components/ui/input";
import { Label } from "../components/ui/label";
import { Alert, AlertDescription } from "../components/ui/alert";
import { Skeleton } from "../components/ui/skeleton";
import { Badge } from "../components/ui/badge";

function slugify(name: string): string {
  return name
    .toLowerCase()
    .trim()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "");
}

// Mirrors the backend contract (service.validSlug): lowercase alphanumeric
// groups joined by single hyphens, 1–64 chars.
const SLUG_RE = /^[a-z0-9]+(-[a-z0-9]+)*$/;
const EMAIL_RE = /^[^\s@]+@[^\s@]+\.[^\s@]+$/;

function slugError(slug: string): string | null {
  if (!slug) return "Slug is required.";
  if (slug.length > 64 || !SLUG_RE.test(slug))
    return "Use lowercase letters, numbers and hyphens (e.g. acme-inc).";
  return null;
}

const STEPS = ["Welcome", "Workspace", "Team"];

function Stepper({ step }: { step: number }) {
  return (
    <ol className="mb-8 flex items-center justify-center gap-2">
      {STEPS.map((label, i) => (
        <li key={label} className="flex items-center gap-2">
          <span
            className={`flex h-6 w-6 items-center justify-center rounded-full text-xs font-medium ${
              i < step
                ? "bg-primary text-primary-foreground"
                : i === step
                  ? "bg-primary text-primary-foreground"
                  : "bg-muted text-muted-foreground"
            }`}
            aria-current={i === step ? "step" : undefined}
          >
            {i + 1}
          </span>
          <span
            className={`text-sm ${i === step ? "font-medium" : "text-muted-foreground"}`}
          >
            {label}
          </span>
          {i < STEPS.length - 1 && (
            <span className="mx-1 h-px w-8 bg-border" aria-hidden />
          )}
        </li>
      ))}
    </ol>
  );
}

function WelcomeStep({ onNext }: { onNext: () => void }) {
  return (
    <Card className="w-full max-w-md">
      <CardHeader className="text-center">
        <CardTitle className="text-2xl">Welcome to glance</CardTitle>
        <CardDescription>
          A lean, self-hosted issue tracker. Let's get your first workspace
          set up — it takes under a minute.
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-3 text-sm text-muted-foreground">
        <p>
          <span className="font-medium text-foreground">1. Workspace.</span>{" "}
          A home for your team's projects.
        </p>
        <p>
          <span className="font-medium text-foreground">2. Team.</span>{" "}
          Invite teammates to collaborate (optional).
        </p>
        <Button className="mt-2 w-full" onClick={onNext}>
          Get started
        </Button>
      </CardContent>
    </Card>
  );
}

function CreateWorkspaceStep({
  onCreated,
  onBack,
}: {
  onCreated: (ws: Workspace) => void;
  onBack: () => void;
}) {
  const [name, setName] = useState("");
  const [slug, setSlug] = useState("");
  const [slugTouched, setSlugTouched] = useState(false);
  const [submitted, setSubmitted] = useState(false);
  const [formError, setFormError] = useState<string | null>(null);
  const [creating, setCreating] = useState(false);

  function onNameChange(value: string) {
    setName(value);
    if (!slugTouched) setSlug(slugify(value));
  }

  const nameInvalid = submitted && !name.trim();
  const slugMsg = slugTouched || submitted ? slugError(slug) : null;
  const canSubmit = name.trim() !== "" && slugError(slug) === null && !creating;

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    setSubmitted(true);
    setFormError(null);
    if (!name.trim() || slugError(slug) !== null) return;
    setCreating(true);
    try {
      // Same shape + error handling as the workspace dialog on the
      // Workspaces page: POST returns the workspace JSON directly.
      const ws = await api.post<Workspace>("/api/v1/workspaces", {
        name: name.trim(),
        slug: slug.trim(),
      });
      onCreated(ws);
    } catch (err) {
      // 409 slug conflict and 400 validation surface the API's message.
      setFormError(
        err instanceof ApiError ? err.message : "Failed to create workspace",
      );
    } finally {
      setCreating(false);
    }
  }

  return (
    <Card className="w-full max-w-md">
      <CardHeader>
        <CardTitle>Create your workspace</CardTitle>
        <CardDescription>
          Workspaces hold projects. You can rename it later.
        </CardDescription>
      </CardHeader>
      <CardContent>
        <form onSubmit={onSubmit} className="space-y-4" noValidate>
          <div className="space-y-2">
            <Label htmlFor="ob-name">Workspace name</Label>
            <Input
              id="ob-name"
              value={name}
              onChange={(e) => onNameChange(e.target.value)}
              placeholder="Acme Inc"
              autoFocus
            />
            {nameInvalid && (
              <p className="text-xs text-destructive">Name is required.</p>
            )}
          </div>
          <div className="space-y-2">
            <Label htmlFor="ob-slug">Slug</Label>
            <Input
              id="ob-slug"
              value={slug}
              onChange={(e) => {
                setSlugTouched(true);
                setSlug(slugify(e.target.value));
              }}
              onBlur={() => setSlugTouched(true)}
              placeholder="acme-inc"
              aria-invalid={slugMsg !== null}
              aria-describedby={slugMsg ? "ob-slug-error" : undefined}
            />
            {slugMsg ? (
              <p id="ob-slug-error" className="text-xs text-destructive">
                {slugMsg}
              </p>
            ) : (
              <p className="text-xs text-muted-foreground">
                Used in URLs: /w/{slug || "acme-inc"}
              </p>
            )}
          </div>
          {formError && (
            <Alert variant="destructive">
              <AlertDescription>{formError}</AlertDescription>
            </Alert>
          )}
          <div className="flex gap-2">
            <Button type="button" variant="outline" onClick={onBack}>
              Back
            </Button>
            <Button type="submit" disabled={!canSubmit} className="flex-1">
              {creating ? "Creating…" : "Create workspace"}
            </Button>
          </div>
        </form>
      </CardContent>
    </Card>
  );
}

function pendingInvitesKey(workspaceId: string) {
  return `glance:pending-invites:${workspaceId}`;
}

function InviteStep({
  workspace,
  onDone,
}: {
  workspace: Workspace;
  onDone: () => void;
}) {
  const [email, setEmail] = useState("");
  const [emails, setEmails] = useState<string[]>(() => {
    try {
      const raw = localStorage.getItem(pendingInvitesKey(workspace.id));
      return raw ? (JSON.parse(raw) as string[]) : [];
    } catch {
      return [];
    }
  });
  const [emailError, setEmailError] = useState<string | null>(null);
  const [sending, setSending] = useState(false);

  function addEmail() {
    const value = email.trim().toLowerCase();
    setEmailError(null);
    if (!value) return;
    if (!EMAIL_RE.test(value)) {
      setEmailError("Enter a valid email address.");
      return;
    }
    if (emails.includes(value)) {
      setEmailError("That email is already on the list.");
      return;
    }
    const next = [...emails, value];
    setEmails(next);
    setEmail("");
    try {
      localStorage.setItem(pendingInvitesKey(workspace.id), JSON.stringify(next));
    } catch {
      // Storage full/blocked — the list still works for this session.
    }
  }

  function removeEmail(value: string) {
    const next = emails.filter((e) => e !== value);
    setEmails(next);
    try {
      localStorage.setItem(pendingInvitesKey(workspace.id), JSON.stringify(next));
    } catch {
      // ignore
    }
  }

  // Done sends the pending emails to the invites endpoint. Registered
  // users become members immediately (invited/already-member results are
  // dropped from the pending list); unregistered ones stay pending on
  // this device. Any failure keeps the whole list — the wizard must never
  // break on a network error.
  async function handleDone() {
    if (emails.length > 0) {
      setSending(true);
      try {
        const res = await api.post<{ results: { email: string; status: string }[] }>(
          `/api/v1/workspaces/${encodeURIComponent(workspace.slug)}/invites`,
          { emails },
        );
        const added = new Set(
          res.results
            .filter((r) => r.status === "invited" || r.status === "already-member")
            .map((r) => r.email),
        );
        if (added.size > 0) {
          const next = emails.filter((e) => !added.has(e));
          setEmails(next);
          try {
            localStorage.setItem(pendingInvitesKey(workspace.id), JSON.stringify(next));
          } catch {
            // ignore
          }
        }
      } catch {
        // Offline or server error: keep the pending list as-is.
      } finally {
        setSending(false);
      }
    }
    onDone();
  }

  return (
    <Card className="w-full max-w-md">
      <CardHeader>
        <CardTitle>Invite your team</CardTitle>
        <CardDescription>
          Optional — you can skip this and invite people later.
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-4">
        <div className="flex gap-2">
          <Input
            value={email}
            onChange={(e) => setEmail(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter") {
                e.preventDefault();
                addEmail();
              }
            }}
            placeholder="teammate@example.com"
            type="email"
            aria-label="Teammate email"
          />
          <Button type="button" variant="outline" onClick={addEmail}>
            Add
          </Button>
        </div>
        {emailError && (
          <p className="text-xs text-destructive">{emailError}</p>
        )}
        {emails.length > 0 && (
          <ul className="space-y-2">
            {emails.map((e) => (
              <li
                key={e}
                className="flex items-center justify-between rounded-md border px-3 py-2 text-sm"
              >
                <span className="truncate">{e}</span>
                <span className="flex items-center gap-2">
                  <Badge variant="secondary">Pending</Badge>
                  <Button
                    type="button"
                    variant="ghost"
                    size="sm"
                    onClick={() => removeEmail(e)}
                    aria-label={`Remove ${e}`}
                  >
                    Remove
                  </Button>
                </span>
              </li>
            ))}
          </ul>
        )}
        <Alert>
          <AlertDescription className="text-xs">
            Teammates who already have an account are added to the
            workspace right away. Others stay on this device as pending —
            once they sign in, add them from the workspace settings.
          </AlertDescription>
        </Alert>
        <div className="flex gap-2">
          <Button
            type="button"
            variant="outline"
            onClick={onDone}
            disabled={sending}
            className="flex-1"
          >
            Skip for now
          </Button>
          <Button
            type="button"
            onClick={handleDone}
            disabled={emails.length === 0 || sending}
            className="flex-1"
          >
            {sending ? "Inviting…" : "Done"}
          </Button>
        </div>
      </CardContent>
    </Card>
  );
}

export default function Onboarding() {
  const navigate = useNavigate();
  const { data: workspaces, isLoading } = useWorkspaces();
  const seedWorkspaces = useSeedWorkspaces();
  const [step, setStep] = useState(0);
  // Set once the wizard creates a workspace: the entry guard below must
  // not fire for a workspace born inside this session (the list query is
  // seeded instead of refetched, so there is no redirect loop).
  const [created, setCreated] = useState<Workspace | null>(null);

  if (isLoading) {
    return (
      <div className="flex min-h-svh items-center justify-center p-6">
        <div className="w-full max-w-md space-y-3" aria-label="Loading">
          <Skeleton className="h-8 w-2/3" />
          <Skeleton className="h-4 w-full" />
          <Skeleton className="h-10 w-full" />
        </div>
      </div>
    );
  }

  // Existing users (≥1 workspace before the wizard started) don't need
  // onboarding.
  if (!created && workspaces && workspaces.length > 0) {
    return <Navigate to="/" replace />;
  }

  function handleCreated(ws: Workspace) {
    setCreated(ws);
    seedWorkspaces([ws]);
    setStep(2);
  }

  return (
    <div className="flex min-h-svh flex-col items-center justify-center p-6">
      <Stepper step={step} />
      {step === 0 && <WelcomeStep onNext={() => setStep(1)} />}
      {step === 1 && (
        <CreateWorkspaceStep
          onCreated={handleCreated}
          onBack={() => setStep(0)}
        />
      )}
      {step === 2 && created && (
        <InviteStep
          workspace={created}
          onDone={() => navigate("/", { replace: true })}
        />
      )}
    </div>
  );
}
