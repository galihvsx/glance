// C83T1: dashboard sharing modal (Plane parity). Owner-only: the
// header renders the Share button only when my_access === 'owner',
// and the server 403s share management for everyone else. Members
// come from the existing workspace members endpoint (no new
// endpoint); the dashboard owner and guests (role < 15) can never be
// added. Edits are staged locally and committed with ONE batch PUT
// (replaceDashboardShares) on Save — Cancel discards everything.
import { useEffect, useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Search, Trash2, UserPlus } from "lucide-react";
import { api, ApiError } from "../../lib/api";
import type { WorkspaceMember } from "../../lib/types";
import {
  dashboardKey,
  dashboardsKey,
  fetchDashboardShares,
  replaceDashboardShares,
  type DashboardShare,
  type ShareAccess,
  type ShareInput,
} from "../../lib/dashboards";
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
import { NativeSelect } from "../ui/native-select";
import { Alert, AlertDescription } from "../ui/alert";
import { Skeleton } from "../ui/skeleton";

/** A staged share row: an existing grant or a pending addition. */
interface DraftShare {
  member_id: string;
  member_name: string;
  access: ShareAccess;
}

function AccessSelect({
  value,
  onChange,
  label,
}: {
  value: ShareAccess;
  onChange: (v: ShareAccess) => void;
  label: string;
}) {
  return (
    <NativeSelect
      aria-label={label}
      value={value}
      onChange={(e) => onChange(e.target.value as ShareAccess)}
      className="h-8 w-auto text-xs"
    >
      <option value="view">View</option>
      <option value="edit">Edit</option>
    </NativeSelect>
  );
}

export default function ShareDialog({
  open,
  onOpenChange,
  slug,
  dashboardId,
  dashboardName,
  ownerId,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  slug: string;
  dashboardId: string;
  dashboardName: string;
  /** The dashboard creator — excluded from search and the list. */
  ownerId: string;
}) {
  const queryClient = useQueryClient();
  const [search, setSearch] = useState("");
  const [draft, setDraft] = useState<DraftShare[] | null>(null);
  const [addAccess, setAddAccess] = useState<Record<string, ShareAccess>>({});
  const [serverError, setServerError] = useState<string | null>(null);

  const membersQuery = useQuery({
    queryKey: ["workspace-members", slug],
    queryFn: () =>
      api
        .get<{ members: WorkspaceMember[] }>(
          `/api/v1/workspaces/${encodeURIComponent(slug)}/members`,
        )
        .then((d) => d.members),
    enabled: open,
  });

  const sharesQuery = useQuery({
    queryKey: ["dashboard-shares", slug, dashboardId],
    queryFn: () => fetchDashboardShares(slug, dashboardId),
    enabled: open,
  });

  // Stage the fetched grants into the editable draft once per open;
  // Save commits the whole list, Cancel throws the draft away.
  useEffect(() => {
    if (open && sharesQuery.data && draft === null) {
      setDraft(
        sharesQuery.data.map((s: DashboardShare) => ({
          member_id: s.member_id,
          member_name: s.member_name,
          access: s.access,
        })),
      );
    }
    if (!open) {
      setDraft(null);
      setSearch("");
      setAddAccess({});
      setServerError(null);
    }
  }, [open, sharesQuery.data, draft]);

  const saveMutation = useMutation({
    mutationFn: (shares: ShareInput[]) =>
      replaceDashboardShares(slug, dashboardId, shares),
    onSuccess: () => {
      void queryClient.invalidateQueries({
        queryKey: dashboardKey(slug, dashboardId),
      });
      void queryClient.invalidateQueries({
        queryKey: dashboardsKey(slug),
      });
      void queryClient.invalidateQueries({
        queryKey: ["dashboard-shares", slug, dashboardId],
      });
      onOpenChange(false);
    },
    onError: (e) =>
      setServerError(
        e instanceof ApiError ? e.message : "Failed to save shares",
      ),
  });

  const draftIds = useMemo(
    () => new Set((draft ?? []).map((d) => d.member_id)),
    [draft],
  );

  // Search candidates: workspace members minus the owner, minus
  // guests (role < 15 — Plane never lets guests be added), minus
  // anyone already in the draft.
  const candidates = useMemo(() => {
    const q = search.trim().toLowerCase();
    return (membersQuery.data ?? []).filter((m) => {
      if (m.id === ownerId) return false;
      if (m.role < 15) return false;
      if (draftIds.has(m.id)) return false;
      if (!q) return true;
      return (
        (m.name ?? "").toLowerCase().includes(q) ||
        m.email.toLowerCase().includes(q)
      );
    });
  }, [membersQuery.data, ownerId, draftIds, search]);

  const memberName = (m: WorkspaceMember) => m.name?.trim() || m.email;

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>Share “{dashboardName}”</DialogTitle>
          <DialogDescription>
            Shared members get View or Edit access to this dashboard.
            Only you, the owner, can manage sharing.
          </DialogDescription>
        </DialogHeader>

        <div className="flex flex-col gap-4 py-2">
          <div className="flex flex-col gap-1.5">
            <div className="relative">
              <Search
                className="pointer-events-none absolute left-2.5 top-1/2 size-4 -translate-y-1/2 text-muted-foreground"
                aria-hidden="true"
              />
              <Input
                aria-label="Search members"
                placeholder="Search members by name or email"
                value={search}
                onChange={(e) => setSearch(e.target.value)}
                className="pl-9"
              />
            </div>
            {membersQuery.isPending ? (
              <Skeleton className="h-8 w-full" />
            ) : candidates.length === 0 ? (
              <p className="text-xs text-muted-foreground">
                {search.trim()
                  ? "No matching members. Guests can't be added."
                  : "No more members to add. Guests can't be added."}
              </p>
            ) : (
              <ul className="flex max-h-44 flex-col gap-1 overflow-auto">
                {candidates.map((m) => (
                  <li
                    key={m.id}
                    className="flex items-center gap-2 rounded-md border border-border/60 px-2.5 py-1.5"
                  >
                    <span className="min-w-0 flex-1 truncate text-sm text-ink">
                      {memberName(m)}
                      <span className="ml-2 text-xs text-muted-foreground">
                        {m.email}
                      </span>
                    </span>
                    <AccessSelect
                      label={`Access for ${memberName(m)}`}
                      value={addAccess[m.id] ?? "view"}
                      onChange={(v) =>
                        setAddAccess((prev) => ({ ...prev, [m.id]: v }))
                      }
                    />
                    <Button
                      type="button"
                      size="sm"
                      variant="outline"
                      aria-label={`Add ${memberName(m)}`}
                      onClick={() =>
                        setDraft((prev) => [
                          ...(prev ?? []),
                          {
                            member_id: m.id,
                            member_name: memberName(m),
                            access: addAccess[m.id] ?? "view",
                          },
                        ])
                      }
                    >
                      <UserPlus className="size-3.5" aria-hidden="true" />
                      Add
                    </Button>
                  </li>
                ))}
              </ul>
            )}
          </div>

          <div className="flex flex-col gap-1.5">
            <p className="text-sm font-medium">Who has access</p>
            {sharesQuery.isPending || draft === null ? (
              <Skeleton className="h-8 w-full" />
            ) : draft.length === 0 ? (
              <p className="text-xs text-muted-foreground">
                Not shared with anyone yet.
              </p>
            ) : (
              <ul className="flex flex-col gap-1">
                {draft.map((s) => (
                  <li
                    key={s.member_id}
                    className="flex items-center gap-2 rounded-md border border-border/60 px-2.5 py-1.5"
                  >
                    <span className="min-w-0 flex-1 truncate text-sm text-ink">
                      {s.member_name}
                    </span>
                    <AccessSelect
                      label={`Access for ${s.member_name}`}
                      value={s.access}
                      onChange={(v) =>
                        setDraft((prev) =>
                          (prev ?? []).map((d) =>
                            d.member_id === s.member_id
                              ? { ...d, access: v }
                              : d,
                          ),
                        )
                      }
                    />
                    <Button
                      type="button"
                      size="sm"
                      variant="ghost"
                      aria-label={`Remove ${s.member_name}`}
                      className="text-destructive hover:text-destructive"
                      onClick={() =>
                        setDraft((prev) =>
                          (prev ?? []).filter(
                            (d) => d.member_id !== s.member_id,
                          ),
                        )
                      }
                    >
                      <Trash2 className="size-3.5" aria-hidden="true" />
                      Remove
                    </Button>
                  </li>
                ))}
              </ul>
            )}
          </div>

          {serverError && (
            <Alert variant="destructive">
              <AlertDescription>{serverError}</AlertDescription>
            </Alert>
          )}
        </div>

        <DialogFooter>
          <Button
            type="button"
            variant="outline"
            onClick={() => onOpenChange(false)}
          >
            Cancel
          </Button>
          <Button
            type="button"
            disabled={draft === null || saveMutation.isPending}
            onClick={() =>
              saveMutation.mutate(
                (draft ?? []).map((d) => ({
                  member_id: d.member_id,
                  access: d.access,
                })),
              )
            }
          >
            {saveMutation.isPending ? "Saving…" : "Save"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
