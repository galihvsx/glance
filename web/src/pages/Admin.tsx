// Instance administration (C5T1): /admin.
//
// Gated by AdminGuard (route) and RequireAdmin (server). Tabs:
//   Overview   — stat cards from GET /api/v1/admin/stats
//   Users      — paginated user directory: admin toggle (self-toggle is
//                disabled — the server also rejects it with 409),
//                deactivate/reactivate behind a confirm
//   Workspaces — paginated usage table; delete behind a typed-name
//                confirmation (the server enforces 400 missing / 409
//                mismatch on ?confirm=<name>)
//   Audit log  — append-only admin audit trail: filters (action /
//                actor_id / entity_type), newest first, relative times
//   Backups    — scheduled backup config (interval / dir / retention),
//                "Run now" trigger, and the verified run history

import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  ChevronLeft,
  ChevronRight,
  DatabaseBackup,
  Play,
  ShieldAlert,
  Trash2,
  Users,
} from "lucide-react";
import { ApiError } from "../lib/api";
import { useAuth } from "../lib/auth";
import {
  ADMIN_KEYS,
  ADMIN_PER_PAGE,
  deactivateAdminUser,
  deleteAdminWorkspace,
  fetchAdminAuditLog,
  fetchAdminBackups,
  fetchAdminStats,
  fetchAdminUsers,
  fetchAdminWorkspaces,
  reactivateAdminUser,
  runAdminBackupNow,
  setAdminUser,
  totalPages,
  type AdminAuditEntry,
  type AdminAuditFilters,
  type AdminBackupRun,
  type AdminUser,
  type AdminWorkspace,
} from "../lib/admin";
import { formatBytes } from "../lib/format";
import { relativeTime } from "../lib/relativeTime";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "../components/ui/alert-dialog";
import { Alert, AlertDescription } from "../components/ui/alert";
import { Badge } from "../components/ui/badge";
import { Button } from "../components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "../components/ui/card";
import { Input } from "../components/ui/input";
import { Label } from "../components/ui/label";
import { Skeleton } from "../components/ui/skeleton";
import { Switch } from "../components/ui/switch";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "../components/ui/table";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "../components/ui/tabs";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "../components/ui/select";
import { toast } from "../components/ui/toast";

function AdminError({ error }: { error: unknown }) {
  return (
    <Alert variant="destructive" className="mb-4">
      <AlertDescription>
        {error instanceof ApiError ? error.message : "Failed to load data"}
      </AlertDescription>
    </Alert>
  );
}

function PageControls({
  page,
  total,
  perPage,
  onPage,
}: {
  page: number;
  total: number;
  perPage: number;
  onPage: (page: number) => void;
}) {
  const pages = totalPages(total, perPage);
  return (
    <div className="mt-4 flex items-center justify-end gap-2">
      <span className="mr-1 text-xs text-muted-foreground">
        Page {page} of {pages} · {total} total
      </span>
      <Button
        variant="outline"
        size="sm"
        disabled={page <= 1}
        onClick={() => onPage(page - 1)}
        aria-label="Previous page"
      >
        <ChevronLeft className="h-4 w-4" />
      </Button>
      <Button
        variant="outline"
        size="sm"
        disabled={page >= pages}
        onClick={() => onPage(page + 1)}
        aria-label="Next page"
      >
        <ChevronRight className="h-4 w-4" />
      </Button>
    </div>
  );
}

function OverviewTab() {
  const statsQuery = useQuery({ queryKey: ADMIN_KEYS.stats, queryFn: fetchAdminStats });
  const stats = statsQuery.data;

  const cards = [
    { label: "Users", value: stats?.users },
    { label: "Workspaces", value: stats?.workspaces },
    { label: "Projects", value: stats?.projects },
    { label: "Issues", value: stats?.issues },
    {
      label: "Attachment storage",
      value: stats ? formatBytes(stats.attachment_bytes) : undefined,
    },
  ];

  if (statsQuery.isError) return <AdminError error={statsQuery.error} />;

  return (
    <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-5">
      {cards.map((c) => (
        <Card key={c.label}>
          <CardHeader className="pb-2">
            <CardTitle className="text-xs font-medium uppercase tracking-wide text-muted-foreground">
              {c.label}
            </CardTitle>
          </CardHeader>
          <CardContent>
            {statsQuery.isLoading ? (
              <Skeleton className="h-8 w-16" />
            ) : (
              <div className="text-2xl font-semibold tracking-tight">
                {c.value ?? "—"}
              </div>
            )}
          </CardContent>
        </Card>
      ))}
    </div>
  );
}

function UsersTab({ selfId }: { selfId: string }) {
  const [page, setPage] = useState(1);
  const queryClient = useQueryClient();
  const usersQuery = useQuery({
    queryKey: ADMIN_KEYS.users(page),
    queryFn: () => fetchAdminUsers(page),
  });

  const toggleAdmin = useMutation({
    mutationFn: ({ id, isAdmin }: { id: string; isAdmin: boolean }) =>
      setAdminUser(id, isAdmin),
    onSuccess: (_, vars) => {
      toast.add({
        title: vars.isAdmin ? "User promoted to admin" : "Admin rights revoked",
        type: "success",
      });
    },
    onError: (e) => {
      toast.add({
        title: "Failed to change admin status",
        description: e instanceof ApiError ? e.message : undefined,
        type: "error",
      });
    },
    onSettled: () => {
      void queryClient.invalidateQueries({ queryKey: ["admin", "users"] });
    },
  });

  const toggleActive = useMutation({
    mutationFn: ({ id, active }: { id: string; active: boolean }) =>
      active ? reactivateAdminUser(id) : deactivateAdminUser(id),
    onSuccess: (_, vars) => {
      toast.add({
        title: vars.active ? "Account reactivated" : "Account deactivated",
        type: "success",
      });
    },
    onError: (e) => {
      toast.add({
        title: "Failed to change account status",
        description: e instanceof ApiError ? e.message : undefined,
        type: "error",
      });
    },
    onSettled: () => {
      void queryClient.invalidateQueries({ queryKey: ["admin", "users"] });
    },
  });

  const confirmDeactivate = (u: AdminUser) => {
    // House confirm pattern (window.confirm, cf. WorkspaceSettings).
    const label = u.name ?? u.email;
    const ok = u.is_active
      ? window.confirm(
          `Deactivate ${label}? They will be signed out everywhere and cannot log in until reactivated.`,
        )
      : window.confirm(`Reactivate ${label}? They will be able to log in again.`);
    if (!ok) return;
    toggleActive.mutate({ id: u.id, active: !u.is_active });
  };

  const rows = usersQuery.data?.items ?? [];

  return (
    <div>
      {usersQuery.isError && <AdminError error={usersQuery.error} />}
      <Card>
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>User</TableHead>
              <TableHead>Admin</TableHead>
              <TableHead>Status</TableHead>
              <TableHead className="text-right">Workspaces</TableHead>
              <TableHead>Joined</TableHead>
              <TableHead className="text-right">Actions</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {usersQuery.isLoading &&
              Array.from({ length: 5 }).map((_, i) => (
                <TableRow key={i}>
                  <TableCell colSpan={6}>
                    <Skeleton className="h-5 w-full" />
                  </TableCell>
                </TableRow>
              ))}
            {!usersQuery.isLoading &&
              rows.map((u) => {
                const isSelf = u.id === selfId;
                return (
                  <TableRow key={u.id}>
                    <TableCell>
                      <div className="font-medium">{u.email}</div>
                      {u.name && (
                        <div className="text-xs text-muted-foreground">
                          {u.name}
                        </div>
                      )}
                    </TableCell>
                    <TableCell>
                      <Switch
                        size="sm"
                        checked={u.is_admin}
                        // Self-toggle is disabled here AND rejected by the
                        // server (409 self-demotion) — belt and suspenders.
                        disabled={isSelf || toggleAdmin.isPending}
                        onCheckedChange={(checked) =>
                          toggleAdmin.mutate({ id: u.id, isAdmin: checked })
                        }
                        aria-label={`Admin status for ${u.email}`}
                        title={
                          isSelf
                            ? "You cannot change your own admin status"
                            : undefined
                        }
                      />
                    </TableCell>
                    <TableCell>
                      <Badge variant={u.is_active ? "secondary" : "outline"}>
                        {u.is_active ? "Active" : "Deactivated"}
                      </Badge>
                    </TableCell>
                    <TableCell className="text-right tabular-nums">
                      {u.workspace_count}
                    </TableCell>
                    <TableCell
                      className="text-sm text-muted-foreground"
                      title={u.created_at}
                    >
                      {relativeTime(u.created_at)}
                    </TableCell>
                    <TableCell className="text-right">
                      <Button
                        variant="ghost"
                        size="sm"
                        disabled={isSelf || toggleActive.isPending}
                        onClick={() => confirmDeactivate(u)}
                        title={
                          isSelf
                            ? "You cannot deactivate your own account"
                            : undefined
                        }
                      >
                        {u.is_active ? "Deactivate" : "Reactivate"}
                      </Button>
                    </TableCell>
                  </TableRow>
                );
              })}
          </TableBody>
        </Table>
      </Card>
      {!usersQuery.isLoading && rows.length === 0 && (
        <div className="mt-4 flex items-center gap-3 rounded-lg border border-dashed p-8 text-sm text-muted-foreground">
          <Users className="h-5 w-5" />
          No users on this instance.
        </div>
      )}
      <PageControls
        page={page}
        total={usersQuery.data?.total ?? 0}
        perPage={ADMIN_PER_PAGE}
        onPage={setPage}
      />
    </div>
  );
}

function WorkspacesTab() {
  const [page, setPage] = useState(1);
  const queryClient = useQueryClient();
  // Typed-name delete state: which workspace the dialog is for, and what
  // the admin has typed so far. The server still validates ?confirm= (400
  // missing / 409 mismatch); the dialog just avoids accidental clicks.
  const [deleteTarget, setDeleteTarget] = useState<AdminWorkspace | null>(null);
  const [confirmName, setConfirmName] = useState("");
  const [deleteError, setDeleteError] = useState<string | null>(null);

  const workspacesQuery = useQuery({
    queryKey: ADMIN_KEYS.workspaces(page),
    queryFn: () => fetchAdminWorkspaces(page),
  });

  const deleteWorkspace = useMutation({
    mutationFn: ({ id, name }: { id: string; name: string }) =>
      deleteAdminWorkspace(id, name),
    onSuccess: (_, vars) => {
      setDeleteTarget(null);
      setConfirmName("");
      setDeleteError(null);
      toast.add({ title: `Workspace "${vars.name}" deleted`, type: "success" });
    },
    onError: (e) => {
      setDeleteError(
        e instanceof ApiError ? e.message : "Failed to delete workspace",
      );
    },
    onSettled: () => {
      void queryClient.invalidateQueries({ queryKey: ["admin", "workspaces"] });
    },
  });

  const openDelete = (w: AdminWorkspace) => {
    setDeleteTarget(w);
    setConfirmName("");
    setDeleteError(null);
  };

  const rows = workspacesQuery.data?.items ?? [];

  return (
    <div>
      {workspacesQuery.isError && <AdminError error={workspacesQuery.error} />}
      <Alert className="mb-4">
        <ShieldAlert className="h-4 w-4" />
        <AlertDescription>
          Deleting a workspace permanently removes it and everything in it
          (projects, issues, attachments). This cannot be undone.
        </AlertDescription>
      </Alert>
      <Card>
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Workspace</TableHead>
              <TableHead className="text-right">Members</TableHead>
              <TableHead className="text-right">Projects</TableHead>
              <TableHead className="text-right">Issues</TableHead>
              <TableHead>Created</TableHead>
              <TableHead className="text-right">Actions</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {workspacesQuery.isLoading &&
              Array.from({ length: 5 }).map((_, i) => (
                <TableRow key={i}>
                  <TableCell colSpan={6}>
                    <Skeleton className="h-5 w-full" />
                  </TableCell>
                </TableRow>
              ))}
            {!workspacesQuery.isLoading &&
              rows.map((w) => (
                <TableRow key={w.id}>
                  <TableCell>
                    <div className="font-medium">{w.name}</div>
                    <div className="font-mono text-xs text-muted-foreground">
                      {w.slug}
                    </div>
                  </TableCell>
                  <TableCell className="text-right tabular-nums">
                    {w.member_count}
                  </TableCell>
                  <TableCell className="text-right tabular-nums">
                    {w.project_count}
                  </TableCell>
                  <TableCell className="text-right tabular-nums">
                    {w.issue_count}
                  </TableCell>
                  <TableCell
                    className="text-sm text-muted-foreground"
                    title={w.created_at}
                  >
                    {relativeTime(w.created_at)}
                  </TableCell>
                  <TableCell className="text-right">
                    <Button
                      variant="ghost"
                      size="sm"
                      className="text-destructive hover:text-destructive"
                      onClick={() => openDelete(w)}
                      aria-label={`Delete workspace ${w.name}`}
                    >
                      <Trash2 className="mr-1.5 h-4 w-4" />
                      Delete
                    </Button>
                  </TableCell>
                </TableRow>
              ))}
          </TableBody>
        </Table>
      </Card>
      {!workspacesQuery.isLoading && rows.length === 0 && (
        <div className="mt-4 rounded-lg border border-dashed p-8 text-sm text-muted-foreground">
          No workspaces on this instance.
        </div>
      )}
      <PageControls
        page={page}
        total={workspacesQuery.data?.total ?? 0}
        perPage={ADMIN_PER_PAGE}
        onPage={setPage}
      />

      <AlertDialog
        open={deleteTarget !== null}
        onOpenChange={(open) => {
          if (!open) {
            setDeleteTarget(null);
            setConfirmName("");
            setDeleteError(null);
          }
        }}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>
              Delete workspace “{deleteTarget?.name}”?
            </AlertDialogTitle>
            <AlertDialogDescription>
              This permanently deletes the workspace and all of its projects,
              issues, cycles, and attachments. To confirm, type the workspace
              name exactly.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <div className="space-y-2">
            <Label htmlFor="admin-delete-confirm">Workspace name</Label>
            <Input
              id="admin-delete-confirm"
              value={confirmName}
              onChange={(e) => {
                setConfirmName(e.target.value);
                setDeleteError(null);
              }}
              placeholder={deleteTarget?.name}
              autoComplete="off"
            />
            {deleteError && (
              <p className="text-sm text-destructive">{deleteError}</p>
            )}
          </div>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction
              className="bg-destructive text-destructive-foreground hover:bg-destructive/90"
              // The typed name must match before the server even sees it —
              // the server re-checks and answers 409 on mismatch anyway.
              disabled={
                !deleteTarget ||
                confirmName !== deleteTarget.name ||
                deleteWorkspace.isPending
              }
              onClick={() =>
                deleteTarget &&
                deleteWorkspace.mutate({
                  id: deleteTarget.id,
                  name: confirmName,
                })
              }
            >
              {deleteWorkspace.isPending ? "Deleting…" : "Delete workspace"}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  );
}

const AUDIT_ACTIONS = [
  "user.role_changed",
  "user.deactivated",
  "user.reactivated",
  "workspace.deleted",
];

function AuditTab() {
  const [page, setPage] = useState(1);
  // Draft = what the inputs show; applied = what the query actually uses.
  // Filters only hit the server on Apply, so typing doesn't refetch.
  const [draft, setDraft] = useState<AdminAuditFilters>({});
  const [applied, setApplied] = useState<AdminAuditFilters>({});

  const auditQuery = useQuery({
    queryKey: ADMIN_KEYS.auditLog(page, applied),
    queryFn: () => fetchAdminAuditLog(page, applied),
  });

  const applyFilters = () => {
    setApplied(draft);
    setPage(1);
  };
  const clearFilters = () => {
    setDraft({});
    setApplied({});
    setPage(1);
  };

  const rows = auditQuery.data?.items ?? [];
  const short = (id: string) =>
    id.length > 8 ? `${id.slice(0, 8)}…` : id;

  return (
    <div>
      {auditQuery.isError && <AdminError error={auditQuery.error} />}
      <Card className="mb-4">
        <CardContent className="pt-6">
          <div className="grid gap-3 sm:grid-cols-[1fr_1fr_1fr_auto]">
            <div className="space-y-1.5">
              <Label htmlFor="audit-action">Action</Label>
              <Input
                id="audit-action"
                placeholder="e.g. user.deactivated"
                value={draft.action ?? ""}
                onChange={(e) =>
                  setDraft({ ...draft, action: e.target.value || undefined })
                }
                list="audit-actions"
                autoComplete="off"
              />
              <datalist id="audit-actions">
                {AUDIT_ACTIONS.map((a) => (
                  <option key={a} value={a} />
                ))}
              </datalist>
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="audit-actor">Actor ID</Label>
              <Input
                id="audit-actor"
                placeholder="Actor UUID"
                value={draft.actorId ?? ""}
                onChange={(e) =>
                  setDraft({ ...draft, actorId: e.target.value || undefined })
                }
                autoComplete="off"
              />
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="audit-entity-type">Entity type</Label>
              <Select
                value={draft.entityType ?? "__any"}
                onValueChange={(v) =>
                  setDraft({
                    ...draft,
                    entityType:
                      v === "__any" || v == null ? undefined : v,
                  })
                }
              >
                <SelectTrigger id="audit-entity-type" className="w-full">
                  <SelectValue placeholder="Any type" />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="__any">Any type</SelectItem>
                  <SelectItem value="user">User</SelectItem>
                  <SelectItem value="workspace">Workspace</SelectItem>
                </SelectContent>
              </Select>
            </div>
            <div className="flex items-end gap-2">
              <Button size="sm" onClick={applyFilters}>
                Apply
              </Button>
              <Button size="sm" variant="ghost" onClick={clearFilters}>
                Clear
              </Button>
            </div>
          </div>
        </CardContent>
      </Card>
      <Card>
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Time</TableHead>
              <TableHead>Actor</TableHead>
              <TableHead>Action</TableHead>
              <TableHead>Entity</TableHead>
              <TableHead>IP</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {auditQuery.isLoading &&
              Array.from({ length: 5 }).map((_, i) => (
                <TableRow key={i}>
                  <TableCell colSpan={5}>
                    <Skeleton className="h-5 w-full" />
                  </TableCell>
                </TableRow>
              ))}
            {!auditQuery.isLoading &&
              rows.map((a: AdminAuditEntry) => (
                <TableRow key={a.id}>
                  <TableCell
                    className="whitespace-nowrap text-sm text-muted-foreground"
                    title={a.at}
                  >
                    {relativeTime(a.at)}
                  </TableCell>
                  <TableCell>
                    <div className="font-medium">{a.actor_email ?? "—"}</div>
                    {a.actor_id && (
                      <div
                        className="font-mono text-xs text-muted-foreground"
                        title={a.actor_id}
                      >
                        {short(a.actor_id)}
                      </div>
                    )}
                  </TableCell>
                  <TableCell>
                    <Badge variant="secondary" className="font-mono text-xs">
                      {a.action}
                    </Badge>
                  </TableCell>
                  <TableCell>
                    <div className="font-mono text-xs">{a.entity_type}</div>
                    <div
                      className="font-mono text-xs text-muted-foreground"
                      title={a.entity_id}
                    >
                      {short(a.entity_id)}
                    </div>
                  </TableCell>
                  <TableCell className="font-mono text-xs text-muted-foreground">
                    {a.ip ?? "—"}
                  </TableCell>
                </TableRow>
              ))}
          </TableBody>
        </Table>
      </Card>
      {!auditQuery.isLoading && rows.length === 0 && (
        <div className="mt-4 rounded-lg border border-dashed p-8 text-sm text-muted-foreground">
          No audit entries match these filters.
        </div>
      )}
      <PageControls
        page={page}
        total={auditQuery.data?.total ?? 0}
        perPage={ADMIN_PER_PAGE}
        onPage={setPage}
      />
    </div>
  );
}

function BackupsTab() {
  const [page, setPage] = useState(1);
  // Run-now target: empty = all workspaces.
  const [runSlug, setRunSlug] = useState("");
  // History filter: draft = input value, applied = what the query uses.
  const [filterDraft, setFilterDraft] = useState("");
  const [filter, setFilter] = useState<string | undefined>(undefined);
  const queryClient = useQueryClient();

  const backupsQuery = useQuery({
    queryKey: ADMIN_KEYS.backups(page, filter),
    queryFn: () => fetchAdminBackups(page, filter),
  });

  const runNow = useMutation({
    mutationFn: (workspaceSlug: string | undefined) =>
      runAdminBackupNow(workspaceSlug),
    onSuccess: (data) => {
      const n = data.backups.length;
      if (data.error) {
        toast.add({
          title: "Backup partially failed",
          description: data.error,
          type: "warning",
        });
      } else {
        toast.add({
          title: "Backup complete",
          description: `${n} workspace${n === 1 ? "" : "s"} backed up and verified`,
          type: "success",
        });
      }
    },
    onError: (e) => {
      toast.add({
        title: "Backup failed",
        description: e instanceof ApiError ? e.message : undefined,
        type: "error",
      });
    },
    onSettled: () => {
      void queryClient.invalidateQueries({ queryKey: ["admin", "backups"] });
    },
  });

  const config = backupsQuery.data?.config;
  const rows = backupsQuery.data?.items ?? [];
  const shortHash = (h: string) => (h.length > 12 ? `${h.slice(0, 12)}…` : h);

  const statusBadge = (s: AdminBackupRun["status"]) => {
    switch (s) {
      case "ok":
        return <Badge variant="secondary">ok</Badge>;
      case "failed":
        return <Badge variant="destructive">failed</Badge>;
      case "pruned":
        return <Badge variant="outline">pruned</Badge>;
    }
  };

  const verifyBadge = (r: AdminBackupRun) => {
    if (r.verify_ok == null)
      return <span className="text-muted-foreground">—</span>;
    return r.verify_ok ? (
      <Badge variant="secondary" title={r.verified_at ?? undefined}>
        verified
      </Badge>
    ) : (
      <Badge variant="destructive" title={r.verify_error ?? undefined}>
        failed
      </Badge>
    );
  };

  return (
    <div>
      {backupsQuery.isError && <AdminError error={backupsQuery.error} />}
      <Card className="mb-4">
        <CardHeader>
          <CardTitle className="flex items-center gap-2 text-base">
            <DatabaseBackup className="h-4 w-4" />
            Schedule
          </CardTitle>
        </CardHeader>
        <CardContent>
          {backupsQuery.isLoading ? (
            <Skeleton className="h-5 w-full" />
          ) : (
            <dl className="grid gap-2 text-sm sm:grid-cols-[160px_1fr]">
              <dt className="text-muted-foreground">Status</dt>
              <dd>
                {config?.enabled ? (
                  <Badge>enabled · every {config.interval}</Badge>
                ) : (
                  <Badge variant="outline">disabled</Badge>
                )}
              </dd>
              <dt className="text-muted-foreground">Target directory</dt>
              <dd className="font-mono text-xs">{config?.dir ?? "—"}</dd>
              <dt className="text-muted-foreground">Retention</dt>
              <dd>
                {config == null
                  ? "—"
                  : config.retention === 0
                    ? "Keep forever"
                    : `Newest ${config.retention} per workspace`}
              </dd>
            </dl>
          )}
          <p className="mt-3 text-xs text-muted-foreground">
            Configured via GLANCE_BACKUP_INTERVAL (empty = disabled),
            GLANCE_BACKUP_DIR and GLANCE_BACKUP_RETENTION. Backups are
            gzipped glance-export/1 archives, checksummed and verified
            after every run; older runs are pruned per the retention
            policy.
          </p>
          <div className="mt-4 flex flex-wrap items-end gap-2">
            <div className="space-y-1.5">
              <Label htmlFor="backup-run-slug">
                Workspace slug (optional)
              </Label>
              <Input
                id="backup-run-slug"
                placeholder="All workspaces when empty"
                value={runSlug}
                onChange={(e) => setRunSlug(e.target.value)}
                autoComplete="off"
                className="w-64"
              />
            </div>
            <Button
              onClick={() => runNow.mutate(runSlug.trim() || undefined)}
              disabled={runNow.isPending}
            >
              <Play className="h-4 w-4" />
              {runNow.isPending ? "Backing up…" : "Run now"}
            </Button>
          </div>
        </CardContent>
      </Card>
      <Card>
        <CardHeader>
          <div className="flex flex-wrap items-end justify-between gap-2">
            <CardTitle className="text-base">History</CardTitle>
            <div className="flex items-end gap-2">
              <div className="space-y-1.5">
                <Label htmlFor="backup-filter-slug" className="sr-only">
                  Filter by workspace slug
                </Label>
                <Input
                  id="backup-filter-slug"
                  placeholder="Filter by workspace slug"
                  value={filterDraft}
                  onChange={(e) => setFilterDraft(e.target.value)}
                  onKeyDown={(e) => {
                    if (e.key === "Enter") {
                      setFilter(filterDraft.trim() || undefined);
                      setPage(1);
                    }
                  }}
                  autoComplete="off"
                  className="w-56"
                />
              </div>
              <Button
                size="sm"
                variant="secondary"
                onClick={() => {
                  setFilter(filterDraft.trim() || undefined);
                  setPage(1);
                }}
              >
                Apply
              </Button>
            </div>
          </div>
        </CardHeader>
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Time</TableHead>
              <TableHead>Workspace</TableHead>
              <TableHead className="text-right">Size</TableHead>
              <TableHead>Status</TableHead>
              <TableHead>Verified</TableHead>
              <TableHead>SHA-256</TableHead>
              <TableHead>Triggered by</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {backupsQuery.isLoading &&
              Array.from({ length: 5 }).map((_, i) => (
                <TableRow key={i}>
                  <TableCell colSpan={7}>
                    <Skeleton className="h-5 w-full" />
                  </TableCell>
                </TableRow>
              ))}
            {!backupsQuery.isLoading &&
              rows.map((r: AdminBackupRun) => (
                <TableRow key={r.id}>
                  <TableCell
                    className="whitespace-nowrap text-sm text-muted-foreground"
                    title={r.at}
                  >
                    {relativeTime(r.at)}
                  </TableCell>
                  <TableCell className="font-mono text-xs">
                    {r.workspace_slug}
                  </TableCell>
                  <TableCell className="text-right tabular-nums">
                    {r.status === "pruned" ? "—" : formatBytes(r.byte_size)}
                  </TableCell>
                  <TableCell>{statusBadge(r.status)}</TableCell>
                  <TableCell>{verifyBadge(r)}</TableCell>
                  <TableCell
                    className="font-mono text-xs text-muted-foreground"
                    title={r.sha256 || undefined}
                  >
                    {r.sha256 ? shortHash(r.sha256) : "—"}
                  </TableCell>
                  <TableCell className="text-sm text-muted-foreground">
                    {r.triggered_by}
                  </TableCell>
                </TableRow>
              ))}
          </TableBody>
        </Table>
      </Card>
      {!backupsQuery.isLoading && rows.length === 0 && (
        <div className="mt-4 rounded-lg border border-dashed p-8 text-sm text-muted-foreground">
          No backups yet. Run one now, or set GLANCE_BACKUP_INTERVAL to
          schedule them.
        </div>
      )}
      <PageControls
        page={page}
        total={backupsQuery.data?.total ?? 0}
        perPage={ADMIN_PER_PAGE}
        onPage={setPage}
      />
    </div>
  );
}

export default function Admin() {
  const { user } = useAuth();

  return (
    <div className="mx-auto w-full max-w-5xl p-6">

      <h1 className="mb-1 text-2xl font-semibold tracking-tight">
        Administration
      </h1>
      <p className="mb-6 text-sm text-muted-foreground">
        Instance-wide stats, user management, and workspace cleanup.
      </p>

      <Tabs defaultValue="overview">
        <TabsList className="mb-6">
          <TabsTrigger value="overview">Overview</TabsTrigger>
          <TabsTrigger value="users">Users</TabsTrigger>
          <TabsTrigger value="workspaces">Workspaces</TabsTrigger>
          <TabsTrigger value="audit-log">Audit log</TabsTrigger>
          <TabsTrigger value="backups">Backups</TabsTrigger>
        </TabsList>
        <TabsContent value="overview">
          <OverviewTab />
        </TabsContent>
        <TabsContent value="users">
          <UsersTab selfId={user?.id ?? ""} />
        </TabsContent>
        <TabsContent value="workspaces">
          <WorkspacesTab />
        </TabsContent>
        <TabsContent value="audit-log">
          <AuditTab />
        </TabsContent>
        <TabsContent value="backups">
          <BackupsTab />
        </TabsContent>
      </Tabs>
    </div>
  );
}
