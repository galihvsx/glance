// TeamspaceManageDialog (C89T1): the sidebar teamspace manage dialog
// (prototype-as-spec): create, rename, and delete with a confirm step.
// Create/rename reject an empty (whitespace-only) name inline, before
// any API call. Delete confirms with the LIVE assigned-project count
// from the sidebar project list: "N projects will move to Ungrouped —
// no projects will be deleted." (The delete response counts archived
// rows too, but that count only exists after the delete runs, so the
// confirm — which must precede it — uses the live count; the two
// agree whenever no archived project is assigned to the teamspace.)
// After any mutation the caller invalidates ["projects", slug] and
// ["teamspaces", slug] via the shared invalidate helper here.
// Members tab (C90T1, Admin-only; C92T1: the teamspace's Lead joins
// it): in rename mode, workspace Admins — and, since C92, the
// teamspace's Lead even when they are not a workspace Admin — get
// Details/Members tabs. The roster is {id, name?, email} — there
// is no teamspace role, so rows show name + email, never a workspace
// role. Anyone else never sees the tab (not a disabled tab).
// Add/remove mutations refetch on success only — a failed mutation
// leaves the roster on the old value with an honest error, never an
// optimistic lie. The add picker lists workspace members NOT already
// in the teamspace, from GET /api/v1/workspaces/:slug/members.
// Lead tab (C92T1, workspace-Admin-only): set/clear the teamspace's
// Lead from the member list. Setting a new Lead replaces the old
// one; the Lead must be a teamspace member (the picker only offers
// members, and the server 400s anything else). The Lead's name shows
// in the dialog header ("Lead: …") once the roster resolves. The
// dialog must not use useAuth (sidebar tests render without an
// AuthProvider) — Lead identity comes from a GET /api/v1/me query
// instead, compared against teamspace.lead_user_id.

import { useEffect, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, ApiError } from "../../lib/api";
import type { User } from "../../lib/auth";
import type { Project, Teamspace, TeamspaceMember } from "../../lib/types";
import {
  addTeamspaceMember,
  clearTeamspaceLead,
  createTeamspace,
  deleteTeamspace,
  fetchTeamspaceMembers,
  fetchWorkspaceMembers,
  removeTeamspaceMember,
  setTeamspaceLead,
  teamspaceKeys,
  teamspaceMemberKeys,
  updateTeamspace,
  workspaceMembersKey,
} from "../../lib/teamspaces";
import { Alert, AlertDescription } from "../ui/alert";
import { Button } from "../ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "../ui/dialog";
import { Input } from "../ui/input";
import { Label } from "../ui/label";
import {
  Tabs,
  TabsContent,
  TabsList,
  TabsTrigger,
} from "../ui/tabs";

export type TeamspaceDialogState =
  | { mode: "create" }
  | { mode: "rename"; teamspace: Teamspace }
  | { mode: "delete"; teamspace: Teamspace };

function errMsg(err: unknown, fallback: string): string {
  return err instanceof ApiError ? err.message : fallback;
}

/** Avatar initials: first letters of the first two name words, else
 *  the first letter of the email local part. */
function initials(member: TeamspaceMember): string {
  const name = (member.name ?? "").trim();
  if (name) {
    const parts = name.split(/\s+/).filter(Boolean);
    if (parts.length >= 2) {
      return (parts[0][0] + parts[1][0]).toUpperCase();
    }
    return parts[0].slice(0, 2).toUpperCase();
  }
  return member.email.charAt(0).toUpperCase();
}

function TeamspaceMembersTab({
  slug,
  teamspace,
}: {
  slug: string;
  teamspace: Teamspace;
}) {
  const queryClient = useQueryClient();
  const [selectedUserId, setSelectedUserId] = useState("");
  const [error, setError] = useState<string | null>(null);

  const rosterQuery = useQuery({
    queryKey: teamspaceMemberKeys(slug, teamspace.id).list,
    queryFn: () => fetchTeamspaceMembers(slug, teamspace.id),
  });
  const workspaceMembersQuery = useQuery({
    queryKey: workspaceMembersKey(slug),
    queryFn: () => fetchWorkspaceMembers(slug),
  });

  const invalidate = () => {
    void queryClient.invalidateQueries({
      queryKey: teamspaceMemberKeys(slug, teamspace.id).list,
    });
    void queryClient.invalidateQueries({ queryKey: teamspaceKeys(slug).list });
  };

  const addMutation = useMutation({
    mutationFn: (userId: string) =>
      addTeamspaceMember(slug, teamspace.id, userId),
    onSuccess: () => {
      setError(null);
      setSelectedUserId("");
      invalidate();
    },
    onError: (e) => setError(errMsg(e, "Failed to add member")),
  });

  const removeMutation = useMutation({
    mutationFn: (userId: string) =>
      removeTeamspaceMember(slug, teamspace.id, userId),
    onSuccess: () => {
      setError(null);
      invalidate();
    },
    onError: (e) => setError(errMsg(e, "Failed to remove member")),
  });

  const roster = rosterQuery.data ?? [];
  const rosterIds = new Set(roster.map((m) => m.id));
  const candidates = (workspaceMembersQuery.data ?? []).filter(
    (m) => !rosterIds.has(m.id),
  );

  return (
    <div className="space-y-3">
      {rosterQuery.isLoading && (
        <p className="text-[13px] text-ink-secondary">Loading members…</p>
      )}
      {rosterQuery.isError && (
        <Alert variant="destructive">
          <AlertDescription>Failed to load members.</AlertDescription>
        </Alert>
      )}
      {!rosterQuery.isLoading && !rosterQuery.isError && roster.length === 0 && (
        <p className="text-[13px] text-ink-secondary">
          No members yet. Add a workspace member below.
        </p>
      )}
      {roster.map((m) => (
        <div key={m.id} className="flex items-center gap-2.5">
          <span className="flex size-6 shrink-0 items-center justify-center rounded-full bg-wash-hover text-[11px] font-medium">
            {initials(m)}
          </span>
          <span className="min-w-0 flex-1">
            <span className="block truncate text-[13px]">
              {m.name?.trim() ? m.name : m.email}
            </span>
            {m.name?.trim() && (
              <span className="block truncate text-[12px] text-ink-secondary">
                {m.email}
              </span>
            )}
          </span>
          <Button
            variant="outline"
            size="sm"
            disabled={removeMutation.isPending}
            onClick={() => removeMutation.mutate(m.id)}
          >
            Remove
          </Button>
        </div>
      ))}
      <div className="flex gap-2">
        <select
          aria-label="Add a workspace member"
          className="h-9 flex-1 rounded-[6px] border border-input bg-background px-2 text-[13px]"
          value={selectedUserId}
          disabled={workspaceMembersQuery.isLoading || candidates.length === 0}
          onChange={(e) => {
            setSelectedUserId(e.target.value);
            setError(null);
          }}
        >
          <option value="">Add a workspace member…</option>
          {candidates.map((m) => (
            <option key={m.id} value={m.id}>
              {m.name?.trim() ? `${m.name} (${m.email})` : m.email}
            </option>
          ))}
        </select>
        <Button
          disabled={!selectedUserId || addMutation.isPending}
          onClick={() => selectedUserId && addMutation.mutate(selectedUserId)}
        >
          {addMutation.isPending ? "Adding…" : "Add"}
        </Button>
      </div>
      {!workspaceMembersQuery.isLoading && candidates.length === 0 && (
        <p className="text-[12px] text-ink-secondary">
          All workspace members are already in this teamspace.
        </p>
      )}
      {error && (
        <Alert variant="destructive">
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}
      <p className="text-[12px] text-ink-secondary">
        Only workspace members can be added. Removing a member hides this
        teamspace (and its projects) from their sidebar — their project
        access elsewhere is unchanged.
      </p>
    </div>
  );
}

function TeamspaceLeadTab({
  slug,
  teamspace,
  leadUserId,
  onLeadChange,
}: {
  slug: string;
  teamspace: Teamspace;
  /** The current Lead id (null = none), owned by the dialog so the
   *  header stays in sync after a set/clear without waiting for the
   *  sidebar list refetch. */
  leadUserId: string | null;
  onLeadChange: (leadUserId: string | null) => void;
}) {
  const queryClient = useQueryClient();
  const [draft, setDraft] = useState<string | null>(leadUserId);
  const [error, setError] = useState<string | null>(null);

  // A new teamspace (or an externally changed Lead) resets the draft.
  useEffect(() => {
    setDraft(leadUserId);
  }, [teamspace.id, leadUserId]);

  const rosterQuery = useQuery({
    queryKey: teamspaceMemberKeys(slug, teamspace.id).list,
    queryFn: () => fetchTeamspaceMembers(slug, teamspace.id),
  });

  const saveMutation = useMutation({
    mutationFn: (next: string | null) =>
      next === null
        ? clearTeamspaceLead(slug, teamspace.id)
        : setTeamspaceLead(slug, teamspace.id, next),
    onSuccess: (ts) => {
      setError(null);
      onLeadChange(ts.lead_user_id ?? null);
      void queryClient.invalidateQueries({ queryKey: teamspaceKeys(slug).list });
    },
    onError: (e) => setError(errMsg(e, "Failed to save lead")),
  });

  const roster = rosterQuery.data ?? [];
  const unchanged = draft === leadUserId;

  return (
    <div className="space-y-3">
      {rosterQuery.isLoading && (
        <p className="text-[13px] text-ink-secondary">Loading members…</p>
      )}
      {rosterQuery.isError && (
        <Alert variant="destructive">
          <AlertDescription>Failed to load members.</AlertDescription>
        </Alert>
      )}
      {!rosterQuery.isLoading && !rosterQuery.isError && (
        <div className="flex gap-2">
          <select
            aria-label="Teamspace lead"
            className="h-9 flex-1 rounded-[6px] border border-input bg-background px-2 text-[13px]"
            value={draft ?? ""}
            disabled={saveMutation.isPending}
            onChange={(e) => {
              setDraft(e.target.value === "" ? null : e.target.value);
              setError(null);
            }}
          >
            <option value="">— No lead —</option>
            {roster.map((m) => (
              <option key={m.id} value={m.id}>
                {m.name?.trim() ? m.name : m.email}
                {m.id === leadUserId ? " (current lead)" : ""}
              </option>
            ))}
          </select>
          <Button
            disabled={unchanged || saveMutation.isPending}
            onClick={() => saveMutation.mutate(draft)}
          >
            {saveMutation.isPending ? "Saving…" : "Save"}
          </Button>
        </div>
      )}
      {error && (
        <Alert variant="destructive">
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}
      <p className="text-[12px] text-ink-secondary">
        One Lead per teamspace. The Lead must be a teamspace member.
        The Lead can manage this teamspace&apos;s members, edit its
        settings, delete it, and link/unlink projects — teamspace-scoped
        powers only, no workspace-level powers. Setting a new Lead
        replaces the old one. If the Lead leaves the teamspace, the
        Lead designation clears automatically.
      </p>
    </div>
  );
}

export default function TeamspaceManageDialog({
  slug,
  state,
  projects,
  onClose,
  isAdmin = false,
}: {
  slug: string | null;
  state: TeamspaceDialogState | null;
  /** Live sidebar project list, for the delete-confirm count. */
  projects: Project[];
  onClose: () => void;
  /** Workspace Admin (role 20) — the Members tab is admin-only and
   *  hidden (never disabled) for everyone else. The caller derives
   *  this from useWorkspaces(); the dialog must not use useAuth,
   *  since sidebar tests render without an AuthProvider. */
  isAdmin?: boolean;
}) {
  const queryClient = useQueryClient();
  const [name, setName] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [tab, setTab] = useState<"details" | "members" | "lead">("details");
  // The Lead id owned by the dialog, so the header + Lead tab stay in
  // sync after a set/clear without waiting for the sidebar refetch.
  const [leadUserId, setLeadUserId] = useState<string | null>(null);

  // Reset the form whenever a new dialog state opens (rename prefills).
  useEffect(() => {
    if (state?.mode === "rename") setName(state.teamspace.name);
    else setName("");
    setError(null);
    setTab("details");
    setLeadUserId(
      state?.mode === "rename" ? (state.teamspace.lead_user_id ?? null) : null,
    );
  }, [state]);

  // Caller identity for the Lead affordance (C92T1): the dialog must
  // not use useAuth (sidebar tests render without an AuthProvider),
  // so the Lead check reads GET /api/v1/me directly. Only needed in
  // rename mode for non-admins — Admins already get every tab.
  const meQuery = useQuery({
    queryKey: ["me"],
    queryFn: () => api.get<User>("/api/v1/me"),
    enabled: state?.mode === "rename" && !isAdmin,
    retry: false,
  });
  // The caller Leads this teamspace when their id matches the
  // dialog-owned Lead id (null = no Lead, never a match).
  const isLead =
    leadUserId !== null && meQuery.data?.id === leadUserId;
  // Roster for the manage header's "Lead: …" label. This hook must
  // sit above the delete-mode early return (hooks order): it is
  // enabled only for the tabbed rename surface, and shares its key
  // with the Members/Lead tabs, so it costs no extra request.
  const renameTeamspace =
    state?.mode === "rename" ? state.teamspace : null;
  const manageRosterQuery = useQuery({
    queryKey:
      renameTeamspace && slug && (isAdmin || isLead)
        ? teamspaceMemberKeys(slug, renameTeamspace.id).list
        : ["teamspaces", "none", "members"],
    queryFn: () =>
      fetchTeamspaceMembers(slug as string, (renameTeamspace as Teamspace).id),
    enabled:
      renameTeamspace !== null && slug !== null && (isAdmin || isLead),
    retry: false,
  });

  const invalidate = () => {
    if (!slug) return;
    void queryClient.invalidateQueries({ queryKey: teamspaceKeys(slug).list });
    void queryClient.invalidateQueries({ queryKey: ["projects", slug] });
  };

  const saveMutation = useMutation({
    mutationFn: () => {
      if (!slug) throw new Error("no workspace");
      if (state?.mode === "rename") {
        return updateTeamspace(slug, state.teamspace.id, { name: name.trim() });
      }
      return createTeamspace(slug, { name: name.trim() });
    },
    onSuccess: () => {
      setError(null);
      invalidate();
      onClose();
    },
    onError: (e) =>
      setError(
        errMsg(
          e,
          state?.mode === "rename"
            ? "Failed to rename teamspace"
            : "Failed to create teamspace",
        ),
      ),
  });

  const deleteMutation = useMutation({
    mutationFn: () => {
      if (!slug || state?.mode !== "delete") throw new Error("no teamspace");
      return deleteTeamspace(slug, state.teamspace.id);
    },
    onSuccess: () => {
      setError(null);
      invalidate();
      onClose();
    },
    onError: (e) => setError(errMsg(e, "Failed to delete teamspace")),
  });

  const open = state !== null && slug !== null;

  function submitName() {
    if (name.trim() === "") {
      setError("Teamspace name is required.");
      return;
    }
    setError(null);
    saveMutation.mutate();
  }

  if (state?.mode === "delete") {
    const liveCount = projects.filter(
      (p) => !p.archived_at && p.teamspace_id === state.teamspace.id,
    ).length;
    return (
      <Dialog open={open} onOpenChange={(v) => !v && onClose()}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>
              Delete teamspace &ldquo;{state.teamspace.name}&rdquo;?
            </DialogTitle>
            <DialogDescription>
              {liveCount} {liveCount === 1 ? "project" : "projects"} will
              move to Ungrouped — no projects will be deleted.
            </DialogDescription>
          </DialogHeader>
          {error && (
            <Alert variant="destructive">
              <AlertDescription>{error}</AlertDescription>
            </Alert>
          )}
          <DialogFooter>
            <Button variant="outline" onClick={onClose}>
              Cancel
            </Button>
            <Button
              variant="destructive"
              disabled={deleteMutation.isPending}
              onClick={() => deleteMutation.mutate()}
            >
              {deleteMutation.isPending ? "Deleting…" : "Delete teamspace"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    );
  }

  const isRename = state?.mode === "rename";
  const nameForm = (
    <>
      <div className="space-y-2">
        <Label htmlFor="teamspace-name">Name</Label>
        <Input
          id="teamspace-name"
          value={name}
          maxLength={120}
          onChange={(e) => {
            setName(e.target.value);
            setError(null);
          }}
          onKeyDown={(e) => {
            if (e.key === "Enter") submitName();
          }}
        />
      </div>
      {error && (
        <Alert variant="destructive">
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}
      <DialogFooter>
        <Button variant="outline" onClick={onClose}>
          Cancel
        </Button>
        <Button disabled={saveMutation.isPending} onClick={submitName}>
          {saveMutation.isPending
            ? "Saving…"
            : isRename
              ? "Save changes"
              : "Create teamspace"}
        </Button>
      </DialogFooter>
    </>
  );

  // Tabbed manage surface in rename mode (C90T1 Members tab, C92T1
  // Lead tab + Lead affordance): workspace Admins get all three tabs;
  // the teamspace's Lead — even when not a workspace Admin — gets
  // Details + Members (Lead tab stays Admin-only). Anyone else keeps
  // the unchanged rename form — tabs are hidden, never disabled.
  if (isRename && (isAdmin || isLead) && state?.mode === "rename" && slug) {
    const liveProjectCount = projects.filter(
      (p) => !p.archived_at && p.teamspace_id === state.teamspace.id,
    ).length;
    const memberCount = state.teamspace.member_count;
    const leadName =
      leadUserId !== null
        ? (manageRosterQuery.data ?? []).find((m) => m.id === leadUserId)
        : undefined;
    const leadLabel =
      leadName !== undefined
        ? ` · Lead: ${leadName.name?.trim() ? leadName.name : leadName.email}`
        : null;
    return (
      <Dialog open={open} onOpenChange={(v) => !v && onClose()}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Manage teamspace</DialogTitle>
            <DialogDescription>
              {state.teamspace.name}
              {memberCount !== undefined && ` · ${memberCount} members`}
              {` · ${liveProjectCount} ${liveProjectCount === 1 ? "project" : "projects"}`}
              {leadLabel}
            </DialogDescription>
          </DialogHeader>
          <Tabs
            value={tab}
            onValueChange={(v) =>
              setTab(
                v === "members" ? "members" : v === "lead" ? "lead" : "details",
              )
            }
          >
            <TabsList>
              <TabsTrigger value="details">Details</TabsTrigger>
              <TabsTrigger value="members">
                Members
                {memberCount !== undefined && ` (${memberCount})`}
              </TabsTrigger>
              {isAdmin && <TabsTrigger value="lead">Lead</TabsTrigger>}
            </TabsList>
            <TabsContent value="details">{nameForm}</TabsContent>
            <TabsContent value="members">
              <TeamspaceMembersTab slug={slug} teamspace={state.teamspace} />
            </TabsContent>
            {isAdmin && (
              <TabsContent value="lead">
                <TeamspaceLeadTab
                  slug={slug}
                  teamspace={state.teamspace}
                  leadUserId={leadUserId}
                  onLeadChange={setLeadUserId}
                />
              </TabsContent>
            )}
          </Tabs>
        </DialogContent>
      </Dialog>
    );
  }

  return (
    <Dialog open={open} onOpenChange={(v) => !v && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>
            {isRename ? "Rename teamspace" : "New teamspace"}
          </DialogTitle>
          <DialogDescription>
            {isRename
              ? "Rename this teamspace. Projects stay where they are."
              : "Group projects in the sidebar under this teamspace."}
          </DialogDescription>
        </DialogHeader>
        {nameForm}
      </DialogContent>
    </Dialog>
  );
}
