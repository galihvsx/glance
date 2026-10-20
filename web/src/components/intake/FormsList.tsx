// FormsList (C82T1): the manage surface for a project's intake forms
// (the admin-only "Forms" tab of the Intake page). Rows show the name,
// truncated description, enabled/disabled badge, and field count; per-row
// actions: Copy link (the token comes from this authenticated manage GET
// — never the public endpoint), Regenerate link (confirm dialog, warns the
// old link dies immediately), Edit, Delete (confirm). "New form" opens
// the FormBuilderDialog.

import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  ClipboardCopy,
  Inbox,
  Pencil,
  Plus,
  RefreshCw,
  Trash2,
} from "lucide-react";
import { ApiError } from "../../lib/api";
import { copyText } from "../../lib/clipboard";
import {
  fetchCustomFields,
} from "../../lib/customFields";
import {
  deleteIntakeForm,
  fetchIntakeForms,
  intakeFormPublicUrl,
  intakeFormsKey,
  regenerateIntakeFormToken,
  type IntakeForm,
} from "../../lib/intakeForms";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "../ui/alert-dialog";
import { Alert, AlertDescription } from "../ui/alert";
import { Badge } from "../ui/badge";
import { Button } from "../ui/button";
import { Skeleton } from "../ui/skeleton";
import { toast } from "../ui/toast";
import FormBuilderDialog from "./FormBuilderDialog";

function FormRow({
  form,
  onEdit,
  onCopyLink,
  onRegenerate,
  onDelete,
  busy,
}: {
  form: IntakeForm;
  onEdit: () => void;
  onCopyLink: () => void;
  onRegenerate: () => void;
  onDelete: () => void;
  busy: boolean;
}) {
  return (
    <div className="flex items-center gap-3 border-b border-border-hairline py-3">
      <div className="min-w-0 flex-1">
        <div className="flex items-center gap-2">
          <span className="truncate text-sm font-medium">{form.name}</span>
          <Badge variant={form.is_enabled ? "secondary" : "outline"}>
            {form.is_enabled ? "Enabled" : "Disabled"}
          </Badge>
          <span className="shrink-0 text-xs text-ink-muted">
            {form.fields.length} {form.fields.length === 1 ? "field" : "fields"}
          </span>
        </div>
        {form.description && (
          <p className="mt-0.5 truncate text-xs text-ink-muted" title={form.description}>
            {form.description}
          </p>
        )}
      </div>
      <div className="flex shrink-0 items-center gap-1">
        <Button
          type="button"
          variant="ghost"
          size="sm"
          onClick={onCopyLink}
          title="Copy public link"
        >
          <ClipboardCopy className="mr-1.5 h-4 w-4" aria-hidden />
          Copy link
        </Button>
        <Button
          type="button"
          variant="ghost"
          size="sm"
          onClick={onRegenerate}
          disabled={busy}
          title="Regenerate share link"
        >
          <RefreshCw className="mr-1.5 h-4 w-4" aria-hidden />
          Regenerate link
        </Button>
        <Button
          type="button"
          variant="ghost"
          size="sm"
          onClick={onEdit}
          disabled={busy}
        >
          <Pencil className="mr-1.5 h-4 w-4" aria-hidden />
          Edit
        </Button>
        <Button
          type="button"
          variant="ghost"
          size="sm"
          onClick={onDelete}
          disabled={busy}
          className="text-accent-red hover:text-accent-red"
        >
          <Trash2 className="mr-1.5 h-4 w-4" aria-hidden />
          Delete
        </Button>
      </div>
    </div>
  );
}

export default function FormsList({
  slug,
  identifier,
}: {
  slug: string;
  identifier: string;
}) {
  const queryClient = useQueryClient();
  const formsKey = intakeFormsKey(slug, identifier);

  const formsQuery = useQuery({
    queryKey: formsKey,
    queryFn: () => fetchIntakeForms(slug, identifier),
  });

  // Custom fields feed the builder's field picker.
  const customFieldsQuery = useQuery({
    queryKey: ["custom-fields", slug, identifier],
    queryFn: () => fetchCustomFields(slug, identifier),
  });

  const [builderOpen, setBuilderOpen] = useState(false);
  const [editing, setEditing] = useState<IntakeForm | null>(null);
  const [regenerating, setRegenerating] = useState<IntakeForm | null>(null);
  const [deleting, setDeleting] = useState<IntakeForm | null>(null);
  const [rowBusy, setRowBusy] = useState(false);

  function invalidateForms() {
    void queryClient.invalidateQueries({ queryKey: formsKey });
  }

  async function copyLink(form: IntakeForm) {
    const ok = await copyText(intakeFormPublicUrl(form.share_token));
    toast.add({
      title: ok ? "Link copied" : "Copy failed",
      description: ok
        ? "The public submission link is on your clipboard."
        : intakeFormPublicUrl(form.share_token),
      type: ok ? "success" : "error",
    });
  }

  const regenerateMutation = useMutation({
    mutationFn: (form: IntakeForm) =>
      regenerateIntakeFormToken(slug, identifier, form.id),
    onSuccess: (updated) => {
      invalidateForms();
      setRegenerating(null);
      setRowBusy(false);
      toast.add({
        title: "Link regenerated",
        description: `The old link is dead. New link: ${intakeFormPublicUrl(updated.share_token)}`,
        type: "success",
      });
    },
    onError: (e) => {
      setRowBusy(false);
      toast.add({
        title: "Regenerate failed",
        description: e instanceof ApiError ? e.message : "Could not regenerate the link.",
        type: "error",
      });
    },
  });

  const deleteMutation = useMutation({
    mutationFn: (form: IntakeForm) =>
      deleteIntakeForm(slug, identifier, form.id),
    onSuccess: (_v, form) => {
      invalidateForms();
      setDeleting(null);
      setRowBusy(false);
      toast.add({
        title: "Form deleted",
        description: `"${form.name}" and its public link are gone.`,
        type: "success",
      });
    },
    onError: (e) => {
      setRowBusy(false);
      toast.add({
        title: "Delete failed",
        description: e instanceof ApiError ? e.message : "Could not delete the form.",
        type: "error",
      });
    },
  });

  function openCreate() {
    setEditing(null);
    setBuilderOpen(true);
  }

  function openEdit(form: IntakeForm) {
    setEditing(form);
    setBuilderOpen(true);
  }

  const customFields = customFieldsQuery.data ?? [];

  return (
    <div>
      <div className="mb-2 flex items-center justify-between">
        <p className="text-sm text-ink-muted">
          Shareable forms that file into this project&apos;s intake queue —
          anyone with the link can submit, no account needed.
        </p>
        <Button size="sm" onClick={openCreate}>
          <Plus className="mr-1.5 h-4 w-4" aria-hidden />
          New form
        </Button>
      </div>

      {formsQuery.isError && (
        <Alert variant="destructive" className="mb-4">
          <AlertDescription>
            {formsQuery.error instanceof ApiError
              ? formsQuery.error.message
              : "Failed to load intake forms."}
          </AlertDescription>
        </Alert>
      )}

      {formsQuery.isLoading ? (
        <div className="space-y-2">
          {[0, 1].map((i) => (
            <Skeleton key={i} className="h-[60px] w-full" />
          ))}
        </div>
      ) : (formsQuery.data ?? []).length === 0 ? (
        <div className="flex flex-col items-center gap-2 rounded-md border border-dashed border-border py-10 text-center">
          <Inbox className="h-6 w-6 text-ink-muted" aria-hidden />
          <p className="text-sm font-medium">No intake forms yet</p>
          <p className="max-w-sm text-xs text-ink-muted">
            Create a form to collect structured bug reports or feature
            requests from people who don&apos;t use the tracker.
          </p>
          <Button size="sm" className="mt-1" onClick={openCreate}>
            <Plus className="mr-1.5 h-4 w-4" aria-hidden />
            New form
          </Button>
        </div>
      ) : (
        <div>
          {(formsQuery.data ?? []).map((form) => (
            <FormRow
              key={form.id}
              form={form}
              busy={rowBusy}
              onEdit={() => openEdit(form)}
              onCopyLink={() => void copyLink(form)}
              onRegenerate={() => setRegenerating(form)}
              onDelete={() => setDeleting(form)}
            />
          ))}
        </div>
      )}

      <FormBuilderDialog
        open={builderOpen}
        onOpenChange={setBuilderOpen}
        slug={slug}
        identifier={identifier}
        form={editing}
        customFields={customFields}
        onSaved={(saved) => {
          invalidateForms();
          toast.add({
            title: editing ? "Form updated" : "Form created",
            description: `"${saved.name}" ${editing ? "updated" : "is live at"} ${intakeFormPublicUrl(saved.share_token)}`,
            type: "success",
          });
        }}
      />

      {/* Regenerate confirm — warns the old link dies immediately. */}
      <AlertDialog
        open={regenerating !== null}
        onOpenChange={(open) => {
          if (!open) setRegenerating(null);
        }}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Regenerate share link?</AlertDialogTitle>
            <AlertDialogDescription>
              The current link for &ldquo;{regenerating?.name}&rdquo; dies
              immediately — anyone holding it will see an unavailable page.
              Only the new link will accept submissions.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel disabled={rowBusy}>Cancel</AlertDialogCancel>
            <AlertDialogAction
              disabled={rowBusy}
              onClick={() => {
                if (!regenerating) return;
                setRowBusy(true);
                regenerateMutation.mutate(regenerating);
              }}
            >
              {rowBusy ? "Regenerating…" : "Regenerate link"}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>

      {/* Delete confirm. */}
      <AlertDialog
        open={deleting !== null}
        onOpenChange={(open) => {
          if (!open) setDeleting(null);
        }}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Delete this form?</AlertDialogTitle>
            <AlertDialogDescription>
              &ldquo;{deleting?.name}&rdquo; and its public link are deleted
              permanently. Issues already submitted through it stay in the
              intake queue.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel disabled={rowBusy}>Cancel</AlertDialogCancel>
            <AlertDialogAction
              disabled={rowBusy}
              onClick={() => {
                if (!deleting) return;
                setRowBusy(true);
                deleteMutation.mutate(deleting);
              }}
            >
              {rowBusy ? "Deleting…" : "Delete form"}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  );
}
