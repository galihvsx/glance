// Project Members section (C92T1, prototype-as-spec): the roster for
// Project Settings' new "Members" tab. Direct members show editable
// roles (Admin/Member/Guest) plus Remove; teamspace-derived members
// (members of the project's linked teamspace who hold no direct row)
// are marked derived and read-only here — their access comes from
// the linked teamspace, so they are managed in the teamspace, not
// here. Non-admins see the roster read-only (no Add / role select /
// Remove). The last direct admin cannot be removed or demoted (the
// server 409s it too; the UI disables the controls with a note).
// Management requires effective project Admin (20) — workspace
// Admin, or a direct Admin row for the caller (resolved from the
// roster + currentUserId). The server is the gate: a failed mutation
// leaves the roster on the old value with an honest error, never an
// optimistic lie. A 404 renders the same generic load error as any
// other missing project — never a "private" hint, never a member
// leak (the server returns the same 404 for a private project the
// caller cannot reach as for a missing one).

import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, ApiError } from "../../lib/api";
import type { Project } from "../../lib/types";
import {
  addProjectMember,
  changeProjectMemberRole,
  fetchProjectMembers,
  projectMemberKeys,
  removeProjectMember,
} from "../../lib/projectMembers";
import {
  fetchTeamspaceMembers,
  fetchTeamspaces,
  fetchWorkspaceMembers,
  teamspaceKeys,
  teamspaceMemberKeys,
  workspaceMembersKey,
} from "../../lib/teamspaces";
import { Alert, AlertDescription } from "../ui/alert";
import { Button } from "../ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "../ui/card";
import { Skeleton } from "../ui/skeleton";

function errMsg(err: unknown, fallback: string): string {
  return err instanceof ApiError ? err.message : fallback;
}

function roleLabel(role: number): string {
  switch (role) {
    case 20:
      return "Admin";
    case 15:
      return "Member";
    case 5:
      return "Guest";
    default:
      return `Role ${role}`;
  }
}

const ROLE_OPTIONS = [
  { value: 20, label: "Admin" },
  { value: 15, label: "Member" },
  { value: 5, label: "Guest" },
];

export default function ProjectMembersSection({
  slug,
  identifier,
  isWorkspaceAdmin,
  currentUserId,
}: {
  slug: string;
  identifier: string;
  /** Workspace Admin (role 20) — always an effective project Admin. */
  isWorkspaceAdmin: boolean;
  /** The caller's user id, for direct-Admin detection + the
   *  last-admin self guard. Undefined when unknown: only the
   *  workspace-Admin signal gates controls then. */
  currentUserId?: string | null;
}) {
  const queryClient = useQueryClient();
  const [selectedUserId, setSelectedUserId] = useState("");
  const [selectedRole, setSelectedRole] = useState(15);
  const [error, setError] = useState<string | null>(null);

  const rosterQuery = useQuery({
    queryKey: projectMemberKeys(slug, identifier).list,
    queryFn: () => fetchProjectMembers(slug, identifier),
  });
  const projectQuery = useQuery({
    queryKey: ["project", slug, identifier],
    queryFn: () =>
      api.get<Project>(
        `/api/v1/workspaces/${encodeURIComponent(slug)}/projects/${encodeURIComponent(identifier)}`,
      ),
  });
  const teamspacesQuery = useQuery({
    queryKey: teamspaceKeys(slug).list,
    queryFn: () => fetchTeamspaces(slug),
  });

  const roster = rosterQuery.data ?? [];
  // Effective project Admin: workspace Admin, or a direct Admin row
  // for the caller. The server re-checks on every mutation.
  const canManage =
    isWorkspaceAdmin ||
    (currentUserId != null &&
      roster.some((m) => m.user_id === currentUserId && m.role === 20));

  const workspaceMembersQuery = useQuery({
    queryKey: workspaceMembersKey(slug),
    queryFn: () => fetchWorkspaceMembers(slug),
    enabled: canManage,
  });

  const teamspaceId = projectQuery.data?.teamspace_id ?? null;
  const teamspaceName =
    teamspaceId != null
      ? (teamspacesQuery.data ?? []).find((t) => t.id === teamspaceId)?.name
      : null;
  // Derived access is Member-level (15) via the linked teamspace.
  // The teamspace roster read is itself gated (teamspace members +
  // Admins; anyone else gets 404), so a failed fetch just omits the
  // derived rows — never an error state, never a leak. retry: false
  // because a 404 here is an expected outcome, not a transient.
  const teamspaceMembersQuery = useQuery({
    queryKey: teamspaceId
      ? teamspaceMemberKeys(slug, teamspaceId).list
      : ["teamspaces", slug, "none", "members"],
    queryFn: () => fetchTeamspaceMembers(slug, teamspaceId as string),
    enabled: teamspaceId != null,
    retry: false,
  });
  const directIds = new Set(roster.map((m) => m.user_id));
  const derived =
    teamspaceId != null && !teamspaceMembersQuery.isError
      ? (teamspaceMembersQuery.data ?? []).filter((m) => !directIds.has(m.id))
      : [];

  const invalidate = () => {
    void queryClient.invalidateQueries({
      queryKey: projectMemberKeys(slug, identifier).list,
    });
  };

  const addMutation = useMutation({
    mutationFn: () =>
      addProjectMember(slug, identifier, selectedUserId, selectedRole),
    onSuccess: () => {
      setError(null);
      setSelectedUserId("");
      setSelectedRole(15);
      invalidate();
    },
    onError: (e) => setError(errMsg(e, "Failed to add member")),
  });
  const roleMutation = useMutation({
    mutationFn: ({ userId, role }: { userId: string; role: number }) =>
      changeProjectMemberRole(slug, identifier, userId, role),
    onSuccess: () => {
      setError(null);
      invalidate();
    },
    onError: (e) => setError(errMsg(e, "Failed to change role")),
  });
  const removeMutation = useMutation({
    mutationFn: (userId: string) =>
      removeProjectMember(slug, identifier, userId),
    onSuccess: () => {
      setError(null);
      invalidate();
    },
    onError: (e) => setError(errMsg(e, "Failed to remove member")),
  });

  if (rosterQuery.isPending) {
    return <Skeleton className="h-64 w-full" />;
  }
  if (rosterQuery.isError) {
    return (
      <Alert variant="destructive">
        <AlertDescription>
          {errMsg(rosterQuery.error, "Failed to load members")}
        </AlertDescription>
      </Alert>
    );
  }

  const adminCount = roster.filter((m) => m.role === 20).length;
  const candidates = (workspaceMembersQuery.data ?? []).filter(
    (m) => !directIds.has(m.id),
  );

  return (
    <Card className="rounded-lg border-border-hairline">
      <CardHeader>
        <CardTitle className="text-xs font-semibold">Members</CardTitle>
      </CardHeader>
      <CardContent className="space-y-4">
        {roster.length === 0 && derived.length === 0 && (
          <p className="text-[13px] text-ink-secondary">
            {canManage
              ? "No members yet. Add a workspace member below."
              : "No direct members."}
          </p>
        )}
        {roster.map((m) => {
          const isSoleAdmin = m.role === 20 && adminCount <= 1;
          const display = m.name?.trim() ? m.name : m.email;
          return (
            <div key={m.user_id} className="flex items-center gap-2.5">
              <span className="min-w-0 flex-1">
                <span className="block truncate text-[13px]">
                  {display}{" "}
                  <span className="text-[12px] text-ink-secondary">
                    {roleLabel(m.role)}
                  </span>
                </span>
                {m.name?.trim() && (
                  <span className="block truncate text-[12px] text-ink-secondary">
                    {m.email}
                  </span>
                )}
                {isSoleAdmin && canManage && (
                  <span className="block text-[12px] text-ink-secondary">
                    Last project admin — promote another admin before
                    removing or demoting this member.
                  </span>
                )}
              </span>
              {canManage && (
                <>
                  <select
                    aria-label={`Role for ${display}`}
                    className="h-9 rounded-[6px] border border-input bg-background px-2 text-[13px]"
                    value={m.role}
                    disabled={isSoleAdmin || roleMutation.isPending}
                    onChange={(e) =>
                      roleMutation.mutate({
                        userId: m.user_id,
                        role: Number(e.target.value),
                      })
                    }
                  >
                    {ROLE_OPTIONS.map((o) => (
                      <option key={o.value} value={o.value}>
                        {o.label}
                      </option>
                    ))}
                    {/* A stored ladder value outside Admin/Member/
                        Guest (e.g. Viewer 10) stays selectable so the
                        select never shows a blank value. */}
                    {!ROLE_OPTIONS.some((o) => o.value === m.role) && (
                      <option value={m.role}>{roleLabel(m.role)}</option>
                    )}
                  </select>
                  <Button
                    variant="outline"
                    size="sm"
                    disabled={isSoleAdmin || removeMutation.isPending}
                    onClick={() => removeMutation.mutate(m.user_id)}
                  >
                    Remove
                  </Button>
                </>
              )}
            </div>
          );
        })}
        {derived.map((m) => {
          const display = m.name?.trim() ? m.name : m.email;
          return (
            <div key={`derived-${m.id}`} className="flex items-center gap-2.5">
              <span className="min-w-0 flex-1">
                <span className="block truncate text-[13px]">
                  {display}{" "}
                  <span className="text-[12px] text-ink-secondary">
                    Member
                  </span>
                  <span className="text-[12px] text-ink-secondary">
                    {teamspaceName
                      ? ` (via teamspace “${teamspaceName}”)`
                      : " (via teamspace)"}
                  </span>
                </span>
                {m.name?.trim() && (
                  <span className="block truncate text-[12px] text-ink-secondary">
                    {m.email}
                  </span>
                )}
              </span>
              <span className="text-[12px] text-ink-secondary">
                Derived access — manage in the teamspace
              </span>
            </div>
          );
        })}
        {canManage && (
          <div className="flex gap-2">
            <select
              aria-label="Add member"
              className="h-9 flex-1 rounded-[6px] border border-input bg-background px-2 text-[13px]"
              value={selectedUserId}
              disabled={
                workspaceMembersQuery.isLoading || candidates.length === 0
              }
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
            <select
              aria-label="Role for new member"
              className="h-9 rounded-[6px] border border-input bg-background px-2 text-[13px]"
              value={selectedRole}
              onChange={(e) => setSelectedRole(Number(e.target.value))}
            >
              {ROLE_OPTIONS.map((o) => (
                <option key={o.value} value={o.value}>
                  {o.label}
                </option>
              ))}
            </select>
            <Button
              disabled={!selectedUserId || addMutation.isPending}
              onClick={() => addMutation.mutate()}
            >
              {addMutation.isPending ? "Adding…" : "Add"}
            </Button>
          </div>
        )}
        {canManage &&
          !workspaceMembersQuery.isLoading &&
          candidates.length === 0 && (
            <p className="text-[12px] text-ink-secondary">
              All workspace members are already direct members of this
              project.
            </p>
          )}
        {!canManage && (
          <p className="text-[12px] text-ink-secondary">
            Only project admins can add, remove, or change member roles.
          </p>
        )}
        {error && (
          <Alert variant="destructive">
            <AlertDescription>{error}</AlertDescription>
          </Alert>
        )}
        <p className="text-[12px] text-ink-secondary">
          Direct members are listed with their project role. Members of
          the linked teamspace get Member access automatically and
          appear as derived — add them directly to give them a
          different role.
        </p>
      </CardContent>
    </Card>
  );
}
