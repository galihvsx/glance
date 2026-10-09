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

import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  ChevronLeft,
  ChevronRight,
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
  fetchAdminStats,
  fetchAdminUsers,
  fetchAdminWorkspaces,
  reactivateAdminUser,
  setAdminUser,
  totalPages,
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
      </Tabs>
    </div>
  );
}
