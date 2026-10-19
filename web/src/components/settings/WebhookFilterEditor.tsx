// Webhook event filter editor (C69T1).
//
// DESIGN DECISION 1 (implementer): the editor lives in this co-located
// file, not inline in WebhooksSection.tsx — that file is already 962
// lines and the editor owns its own draft state, picker loading, and
// save/clear flows. WebhooksSection only renders the row badge and
// mounts this dialog.
//
// DESIGN DECISION 2 (implementer): target_date supports BOTH ops, not
// eq-only — the backend validates `in` for target_date, so dropping it
// would lose a real capability. eq gets a native date input; in gets a
// comma-separated text input (parsed + validated client-side on save),
// which matches the free-text way date lists are entered elsewhere.
//
// The editor edits a draft condition list; nothing touches the server
// until Save (PATCH {filter: [...]}) or Clear all (PATCH {filter: null}
// — the tri-state from internal/api/notify_handler.go; see
// buildFilterPatch in lib/webhookFilter.ts).

import { useEffect, useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Plus, Trash2, X } from "lucide-react";
import { api } from "../../lib/api";
import {
  buildFilterPatch,
  conditionSummary,
  fieldOptions,
  filterSummaryText,
  isUuidField,
  validateWebhookCondition,
  WEBHOOK_FILTER_FIELDS,
  WEBHOOK_FILTER_OPS,
  type LookupMaps,
  type WebhookCondition,
  type WebhookFilter,
  type WebhookFilterOp,
} from "../../lib/webhookFilter";
import {
  updateWebhook,
  type Webhook,
  type WebhookUpdateBody,
} from "../../lib/webhooks";
import type { Label, Member, Project } from "../../lib/types";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "../ui/dialog";
import { Alert, AlertDescription } from "../ui/alert";
import { Badge } from "../ui/badge";
import { Button } from "../ui/button";
import { Checkbox } from "../ui/checkbox";
import { Input } from "../ui/input";
import { Label as UiLabel } from "../ui/label";
import {
  NativeSelect,
  NativeSelectOption,
} from "../ui/native-select";
import { toast } from "../ui/toast";

const WEBHOOKS_KEY = (slug: string) => ["webhooks", slug];

/** One editor row. value is a scalar for eq, a string[] for in — except
 *  target_date+in, where it is the raw comma-separated text (parsed on
 *  save). */
interface DraftRow {
  key: number;
  field: string;
  op: WebhookFilterOp;
  value: string | string[];
}

let rowKey = 0;
const nextKey = () => ++rowKey;

function blankRow(): DraftRow {
  return { key: nextKey(), field: "priority", op: "eq", value: "" };
}

function draftFromFilter(filter: WebhookFilter | null): DraftRow[] {
  return (filter ?? []).map((c) => ({
    key: nextKey(),
    field: c.field,
    op: c.op,
    value: Array.isArray(c.value) ? [...c.value] : c.value,
  }));
}

function defaultValue(field: string, op: WebhookFilterOp): string | string[] {
  if (op === "in") {
    // target_date+in is the raw comma-separated text (parsed on save).
    return field === "target_date" ? "" : [];
  }
  return "";
}

/** Parses a target_date+in text input into the wire array. */
export function parseDateList(raw: string): string[] {
  return raw
    .split(",")
    .map((s) => s.trim())
    .filter((s) => s !== "");
}

function normalizeRow(row: DraftRow): WebhookCondition | { error: string } {
  let value = row.value;
  if (row.field === "target_date" && row.op === "in") {
    value = parseDateList(value as string);
  }
  const cond: WebhookCondition = {
    field: row.field,
    op: row.op,
    value: value as string | string[],
  };
  const err = validateWebhookCondition(cond);
  if (err) return { error: err };
  return cond;
}

function ValuePicker({
  row,
  projects,
  members,
  labels,
  onChange,
}: {
  row: DraftRow;
  projects: Project[];
  members: Member[];
  labels: Label[];
  onChange: (value: string | string[]) => void;
}) {
  const { field, op, value } = row;

  // Closed vocabularies (priority / state_group).
  const opts = fieldOptions(field);
  if (opts) {
    if (op === "eq") {
      return (
        <NativeSelect
          size="sm"
          value={value as string}
          onChange={(e) => onChange(e.target.value)}
          aria-label={`${field} value`}
        >
          <NativeSelectOption value="">Select…</NativeSelectOption>
          {opts.map((o) => (
            <NativeSelectOption key={o.value} value={o.value}>
              {o.label}
            </NativeSelectOption>
          ))}
        </NativeSelect>
      );
    }
    const selected = new Set(value as string[]);
    return (
      <div className="flex max-h-28 flex-wrap gap-1 overflow-y-auto rounded-md border border-input p-1.5">
        {opts.map((o) => (
          <label
            key={o.value}
            className="flex cursor-pointer items-center gap-1.5 rounded px-1.5 py-1 text-xs hover:bg-muted"
          >
            <Checkbox
              checked={selected.has(o.value)}
              onCheckedChange={(c) => {
                const next = c === true
                  ? [...selected, o.value]
                  : [...selected].filter((v) => v !== o.value);
                onChange(next);
              }}
            />
            {o.label}
          </label>
        ))}
      </div>
    );
  }

  // Entity pickers (assignee / label / project).
  if (isUuidField(field)) {
    const items: Array<{ id: string; label: string; hint?: string }> =
      field === "assignee_id"
        ? members.map((m) => ({
            id: m.id,
            label: m.name?.trim() || m.email,
            hint: m.email,
          }))
        : field === "label_id"
          ? labels.map((l) => ({ id: l.id, label: l.name }))
          : projects.map((p) => ({ id: p.id, label: p.name }));
    if (items.length === 0) {
      return (
        <p className="text-xs text-muted-foreground">
          Nothing to pick yet.
        </p>
      );
    }
    if (op === "eq") {
      return (
        <NativeSelect
          size="sm"
          value={value as string}
          onChange={(e) => onChange(e.target.value)}
          aria-label={`${field} value`}
        >
          <NativeSelectOption value="">Select…</NativeSelectOption>
          {items.map((i) => (
            <NativeSelectOption key={i.id} value={i.id}>
              {i.label}
            </NativeSelectOption>
          ))}
        </NativeSelect>
      );
    }
    const selected = new Set(value as string[]);
    return (
      <div className="max-h-28 space-y-0.5 overflow-y-auto rounded-md border border-input p-1">
        {items.map((i) => (
          <label
            key={i.id}
            className="flex cursor-pointer items-center gap-2 rounded px-1.5 py-1 text-xs hover:bg-muted"
          >
            <Checkbox
              checked={selected.has(i.id)}
              onCheckedChange={(c) => {
                const next = c === true
                  ? [...selected, i.id]
                  : [...selected].filter((v) => v !== i.id);
                onChange(next);
              }}
            />
            <span className="flex-1 truncate">{i.label}</span>
            {i.hint && i.hint !== i.label && (
              <span className="truncate text-[11px] text-muted-foreground">
                {i.hint}
              </span>
            )}
          </label>
        ))}
      </div>
    );
  }

  // target_date: eq → native date input; in → comma-separated list.
  if (op === "eq") {
    return (
      <Input
        type="date"
        value={value as string}
        onChange={(e) => onChange(e.target.value)}
        className="h-8 text-xs"
        aria-label="Target date value"
      />
    );
  }
  return (
    <Input
      value={value as string}
      onChange={(e) => onChange(e.target.value)}
      placeholder="2026-10-20, 2026-10-21"
      className="h-8 font-mono text-xs"
      aria-label="Target dates, comma-separated"
    />
  );
}

export default function WebhookFilterEditor({
  slug,
  webhook,
  open,
  onClose,
}: {
  slug: string;
  webhook: Webhook;
  open: boolean;
  onClose: () => void;
}) {
  const queryClient = useQueryClient();
  const [rows, setRows] = useState<DraftRow[]>([]);
  const [error, setError] = useState<string | null>(null);

  // Seed the draft from the stored filter each time the dialog opens.
  useEffect(() => {
    if (open) {
      setRows(draftFromFilter(webhook.filter));
      setError(null);
    }
  }, [open, webhook.filter]);

  const projectsQuery = useQuery({
    queryKey: ["projects", slug],
    queryFn: () =>
      api
        .get<{ projects: Project[] }>(
          `/api/v1/workspaces/${encodeURIComponent(slug)}/projects`,
        )
        .then((d) => d.projects ?? []),
    enabled: open,
  });
  const membersQuery = useQuery({
    queryKey: ["members", slug],
    queryFn: () =>
      api
        .get<{ members: Member[] }>(
          `/api/v1/workspaces/${encodeURIComponent(slug)}/members`,
        )
        .then((d) => d.members ?? []),
    enabled: open,
  });
  // Labels are workspace-scoped in the DB (the project path only
  // resolves scope/authz), so one project's labels endpoint returns
  // the whole workspace's labels.
  const labelsQuery = useQuery({
    queryKey: ["labels", slug],
    queryFn: () => {
      const first = projectsQuery.data?.[0];
      if (!first) return Promise.resolve([] as Label[]);
      return api
        .get<{ labels: Label[] }>(
          `/api/v1/workspaces/${encodeURIComponent(slug)}/projects/${encodeURIComponent(first.identifier)}/labels`,
        )
        .then((d) => d.labels ?? []);
    },
    enabled: open && (projectsQuery.data?.length ?? 0) > 0,
  });

  const projects = projectsQuery.data ?? [];
  const members = membersQuery.data ?? [];
  const labels = labelsQuery.data ?? [];

  const lookups: LookupMaps = useMemo(
    () => ({
      projects: new Map(projects.map((p) => [p.id, p.name])),
      members: new Map(
        members.map((m) => [m.id, m.name?.trim() || m.email]),
      ),
      labels: new Map(labels.map((l) => [l.id, l.name])),
    }),
    [projects, members, labels],
  );

  const invalidate = () =>
    void queryClient.invalidateQueries({ queryKey: WEBHOOKS_KEY(slug) });

  const saveMutation = useMutation({
    mutationFn: (body: WebhookUpdateBody) =>
      updateWebhook(slug, webhook.id, body),
    onSuccess: (_, body) => {
      toast.add({
        title:
          body.filter === null ? "Event filters cleared" : "Event filters updated",
        type: "success",
      });
      invalidate();
      onClose();
    },
    onError: (e) =>
      setError(
        e instanceof Error && "body" in e
          ? String((e as { body: string }).body || "Failed to save filters")
          : "Failed to save filters",
      ),
    onSettled: invalidate,
  });

  function updateRow(key: number, patch: Partial<DraftRow>) {
    setRows((prev) => prev.map((r) => (r.key === key ? { ...r, ...patch } : r)));
  }

  function onFieldChange(key: number, field: string) {
    // Reset the value when the field changes — pickers differ per field.
    const row = rows.find((r) => r.key === key);
    if (!row) return;
    updateRow(key, {
      field,
      value: defaultValue(field, row.op),
    });
  }

  function onOpChange(key: number, op: WebhookFilterOp) {
    const row = rows.find((r) => r.key === key);
    if (!row) return;
    updateRow(key, { op, value: defaultValue(row.field, op) });
  }

  function onSave() {
    setError(null);
    const conds: WebhookCondition[] = [];
    for (let i = 0; i < rows.length; i++) {
      const res = normalizeRow(rows[i]);
      if ("error" in res) {
        setError(`Condition ${i + 1}: ${res.error}`);
        return;
      }
      conds.push(res);
    }
    saveMutation.mutate(buildFilterPatch(conds));
  }

  function onClearAll() {
    // House confirm pattern (window.confirm, cf. WebhooksSection).
    if (
      !window.confirm(
        "Clear all event filters on this webhook? It will receive every event again.",
      )
    )
      return;
    setError(null);
    saveMutation.mutate(buildFilterPatch(null));
  }

  const storedCount = webhook.filter?.length ?? 0;

  return (
    <Dialog open={open} onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="max-h-[90vh] max-w-xl overflow-y-auto">
        <DialogHeader>
          <DialogTitle>Event filters</DialogTitle>
          <DialogDescription>
            Only deliver events whose work item matches{" "}
            <strong>all</strong> conditions. Non-matching events are never
            enqueued. Applies to work-item events (issue.*, comment.*) —
            other events always deliver.
          </DialogDescription>
        </DialogHeader>

        <div className="space-y-3">
          {rows.map((row, i) => (
            <div
              key={row.key}
              className="space-y-2 rounded-md border border-border p-3"
            >
              <div className="flex items-center justify-between gap-2">
                <UiLabel className="text-xs font-medium">
                  Condition {i + 1}
                </UiLabel>
                <Button
                  variant="ghost"
                  size="icon"
                  className="h-7 w-7"
                  onClick={() =>
                    setRows((prev) => prev.filter((r) => r.key !== row.key))
                  }
                  aria-label={`Remove condition ${i + 1}`}
                >
                  <X className="h-3.5 w-3.5" />
                </Button>
              </div>
              <div className="flex flex-wrap items-center gap-2">
                <NativeSelect
                  size="sm"
                  value={row.field}
                  onChange={(e) => onFieldChange(row.key, e.target.value)}
                  aria-label={`Condition ${i + 1} field`}
                >
                  {WEBHOOK_FILTER_FIELDS.map((f) => (
                    <NativeSelectOption key={f.value} value={f.value}>
                      {f.label}
                    </NativeSelectOption>
                  ))}
                </NativeSelect>
                <NativeSelect
                  size="sm"
                  value={row.op}
                  onChange={(e) =>
                    onOpChange(row.key, e.target.value as WebhookFilterOp)
                  }
                  aria-label={`Condition ${i + 1} operator`}
                >
                  {WEBHOOK_FILTER_OPS.map((o) => (
                    <NativeSelectOption key={o.value} value={o.value}>
                      {o.label}
                    </NativeSelectOption>
                  ))}
                </NativeSelect>
                <div className="min-w-0 flex-1">
                  <ValuePicker
                    row={row}
                    projects={projects}
                    members={members}
                    labels={labels}
                    onChange={(value) => updateRow(row.key, { value })}
                  />
                </div>
              </div>
              <p className="text-xs text-muted-foreground">
                {conditionSummary(
                  row.field === "target_date" && row.op === "in"
                    ? {
                        field: row.field,
                        op: row.op,
                        value: parseDateList(row.value as string),
                      }
                    : {
                        field: row.field,
                        op: row.op,
                        value: row.value as string | string[],
                      },
                  lookups,
                )}
              </p>
            </div>
          ))}

          {rows.length === 0 && (
            <p className="rounded-md border border-dashed p-4 text-center text-xs text-muted-foreground">
              {filterSummaryText(webhook.filter)} — add a condition below to
              filter which events this webhook receives.
            </p>
          )}

          <Button
            type="button"
            variant="outline"
            size="sm"
            onClick={() => setRows((prev) => [...prev, blankRow()])}
          >
            <Plus className="h-3.5 w-3.5" />
            Add condition
          </Button>

          {error && (
            <Alert variant="destructive">
              <AlertDescription>{error}</AlertDescription>
            </Alert>
          )}
        </div>

        <DialogFooter className="flex-row justify-between sm:justify-between">
          <Button
            type="button"
            variant="ghost"
            size="sm"
            className="text-destructive hover:text-destructive"
            disabled={storedCount === 0 || saveMutation.isPending}
            onClick={onClearAll}
          >
            <Trash2 className="h-3.5 w-3.5" />
            Clear all
          </Button>
          <div className="flex gap-2">
            <Button type="button" variant="outline" onClick={onClose}>
              Cancel
            </Button>
            <Button
              type="button"
              onClick={onSave}
              disabled={rows.length === 0 || saveMutation.isPending}
            >
              {saveMutation.isPending ? "Saving…" : "Save filters"}
            </Button>
          </div>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

/** Row badge for the webhook list: "N filters" opens the editor. */
export function FilterBadge({
  webhook,
  onOpen,
}: {
  webhook: Webhook;
  onOpen: () => void;
}) {
  const count = webhook.filter?.length ?? 0;
  return (
    <button
      type="button"
      onClick={onOpen}
      title={
        count === 0
          ? "No event filters — click to add"
          : webhook.filter!
              .map((c) =>
                conditionSummary(
                  c,
                  { projects: new Map(), members: new Map(), labels: new Map() },
                ),
              )
              .join(" AND ")
      }
      aria-label={
        count === 0
          ? `Add event filters to ${webhook.url}`
          : `Edit ${count} event filters on ${webhook.url}`
      }
    >
      <Badge variant={count === 0 ? "outline" : "secondary"}>
        {filterSummaryText(webhook.filter)}
      </Badge>
    </button>
  );
}
