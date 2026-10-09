// Issue custom fields (C7T3): per-issue values for the project's typed
// custom fields, rendered by type with inline editing.
//
// The field definitions come from the project custom-fields endpoint; the
// issue's stored values arrive via ?include_custom=1 (same pattern as C5T5's
// ?include_children=1 — the list query is untouched). Each row validates
// locally first (lib/customFields), and the server's 400 message is shown
// verbatim when it disagrees. `required` is a visual marker only — the
// server does not enforce it on value writes (C7T2 contract).
//
// Reads: any member (guest 5+); value mutations are member (15)+ and the
// server is the gate — guests get a read-only rendering.

import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { X } from "lucide-react";
import { ApiError, api } from "../../lib/api";
import type { Issue, Workspace } from "../../lib/types";
import {
  CUSTOM_FIELD_TYPES,
  customFieldKeys,
  fetchCustomFields,
  formatCustomValue,
  setCustomValues,
  validateCustomValueInput,
  valueToDraft,
  type CustomField,
  type CustomValue,
  type CustomValueInput,
} from "../../lib/customFields";
import { Alert, AlertDescription } from "../ui/alert";
import { Badge } from "../ui/badge";
import { Button } from "../ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "../ui/card";
import { Checkbox } from "../ui/checkbox";
import { Input } from "../ui/input";
import { Label } from "../ui/label";
import {
  NativeSelect,
  NativeSelectOption,
} from "../ui/native-select";
import { Skeleton } from "../ui/skeleton";

const typeLabel = (t: CustomField["field_type"]) =>
  CUSTOM_FIELD_TYPES.find((x) => x.value === t)?.label ?? t;

function RowError({ message }: { message: string | null }) {
  if (!message) return null;
  return <p className="text-xs text-destructive">{message}</p>;
}

function FieldRow({
  field,
  stored,
  canEdit,
  saving,
  onSave,
}: {
  field: CustomField;
  stored?: CustomValue;
  canEdit: boolean;
  saving: boolean;
  onSave: (values: Record<string, CustomValueInput>) => void;
}) {
  const isCheckbox = field.field_type === "checkbox";
  // Initialized from the server value at mount; the parent remounts this
  // row (via key) whenever the stored value changes after a save, so no
  // resync effect is needed.
  const [draft, setDraft] = useState(() => valueToDraft(field, stored));
  const [checked, setChecked] = useState(() => stored?.value === true);
  const [baseline] = useState(() => valueToDraft(field, stored));
  const [baselineChecked] = useState(() => stored?.value === true);
  const [error, setError] = useState<string | null>(null);

  const dirty = isCheckbox ? checked !== baselineChecked : draft !== baseline;

  function handleSave() {
    if (isCheckbox) {
      setError(null);
      onSave({ [field.id]: checked });
      return;
    }
    const v = validateCustomValueInput(field, draft);
    if (!v.ok) {
      setError(v.error);
      return;
    }
    setError(null);
    onSave({ [field.id]: v.value });
  }

  function handleReset() {
    setDraft(baseline);
    setChecked(baselineChecked);
    setError(null);
  }

  const inputId = `cf-${field.id}`;
  const disabled = !canEdit || saving;

  return (
    <div className="space-y-1.5">
      <div className="flex items-center gap-2">
        <Label htmlFor={isCheckbox ? undefined : inputId} className="flex-1">
          {field.name}
          {field.required && (
            <span
              className="ml-0.5 text-destructive"
              title="Required is a visual indicator only — the API does not block saving without a value."
            >
              *
            </span>
          )}
        </Label>
        <Badge variant="outline" className="text-[10px]">
          {typeLabel(field.field_type)}
        </Badge>
      </div>

      {!canEdit ? (
        <p className="text-sm">{formatCustomValue(field, stored)}</p>
      ) : isCheckbox ? (
        <div className="flex items-center gap-2">
          <Checkbox
            id={inputId}
            checked={checked}
            onCheckedChange={(v) => setChecked(v === true)}
            disabled={disabled}
          />
          <Label htmlFor={inputId} className="font-normal text-muted-foreground">
            {checked ? "Checked" : "Unchecked"}
          </Label>
        </div>
      ) : field.field_type === "select" ? (
        <NativeSelect
          id={inputId}
          value={draft}
          onChange={(e) => setDraft(e.target.value)}
          disabled={disabled}
          className="w-full"
        >
          <NativeSelectOption value="">—</NativeSelectOption>
          {field.options.map((o) => (
            <NativeSelectOption key={o.value} value={o.value}>
              {o.value}
            </NativeSelectOption>
          ))}
        </NativeSelect>
      ) : (
        <Input
          id={inputId}
          value={draft}
          onChange={(e) => setDraft(e.target.value)}
          disabled={disabled}
          type={field.field_type === "date" ? "date" : field.field_type === "number" ? "number" : "text"}
          inputMode={field.field_type === "number" ? "decimal" : undefined}
          step={field.field_type === "number" ? "any" : undefined}
          placeholder={stored ? undefined : "—"}
        />
      )}

      <RowError message={error} />

      {canEdit && (
        <div className="flex items-center gap-1">
          {dirty ? (
            <>
              <Button
                size="sm"
                onClick={handleSave}
                disabled={saving}
              >
                {saving ? "Saving…" : "Save"}
              </Button>
              <Button
                size="sm"
                variant="ghost"
                onClick={handleReset}
                disabled={saving}
              >
                Reset
              </Button>
            </>
          ) : (
            stored && (
              <Button
                size="sm"
                variant="ghost"
                className="h-7 gap-1 px-2 text-muted-foreground"
                onClick={() => onSave({ [field.id]: null })}
                disabled={saving}
                title={`Clear the value of "${field.name}"`}
              >
                <X className="h-3.5 w-3.5" /> Clear
              </Button>
            )
          )}
        </div>
      )}
    </div>
  );
}

export default function IssueCustomFields({
  slug,
  identifier,
  issue,
  issueKey,
}: {
  slug: string;
  identifier: string;
  issue: Issue;
  issueKey: readonly unknown[];
}) {
  const queryClient = useQueryClient();
  const [apiError, setApiError] = useState<string | null>(null);

  const keys = customFieldKeys(slug, identifier);
  const fieldsQuery = useQuery({
    queryKey: keys.fields,
    queryFn: () => fetchCustomFields(slug, identifier),
  });

  // Member (15)+ may write values; guests read. Server is the gate.
  const roleQuery = useQuery({
    queryKey: ["workspace-role", slug],
    queryFn: () =>
      api
        .get<Workspace & { role: number }>(
          `/api/v1/workspaces/${encodeURIComponent(slug)}`,
        )
        .then((w) => w.role ?? 0),
    staleTime: 60_000,
  });
  const canEdit = (roleQuery.data ?? 0) >= 15;

  const saveMutation = useMutation({
    mutationFn: (values: Record<string, CustomValueInput>) =>
      setCustomValues(slug, identifier, issue.id, values),
    onSuccess: () => {
      setApiError(null);
      void queryClient.invalidateQueries({ queryKey: issueKey });
    },
    onError: (e) =>
      setApiError(
        e instanceof ApiError ? e.message : "Failed to save custom value.",
      ),
  });

  const fields = fieldsQuery.data ?? [];
  const values = issue.custom_values ?? {};

  return (
    <Card>
      <CardHeader className="pb-3">
        <CardTitle className="text-base">Custom fields</CardTitle>
      </CardHeader>
      <CardContent className="space-y-4">
        {apiError && (
          <Alert variant="destructive">
            <AlertDescription>{apiError}</AlertDescription>
          </Alert>
        )}
        {fieldsQuery.isLoading || roleQuery.isLoading ? (
          <div className="space-y-3">
            <Skeleton className="h-9 w-full" />
            <Skeleton className="h-9 w-full" />
          </div>
        ) : fieldsQuery.isError ? (
          <Alert variant="destructive">
            <AlertDescription>
              Failed to load custom fields.
              <Button
                variant="link"
                size="sm"
                onClick={() => void fieldsQuery.refetch()}
              >
                Retry
              </Button>
            </AlertDescription>
          </Alert>
        ) : fields.length === 0 ? (
          <p className="text-sm text-muted-foreground">
            No custom fields in this project yet.
            {canEdit &&
              " Add them in Project settings → Custom fields."}
          </p>
        ) : (
          <>
            {fields.map((f) => (
              <FieldRow
                // Remount when the server value changes (after a save
                // refetch) so the editable state resyncs without effects.
                key={`${f.id}:${JSON.stringify(values[f.id]?.value ?? null)}`}
                field={f}
                stored={values[f.id]}
                canEdit={canEdit}
                saving={saveMutation.isPending}
                onSave={(v) => saveMutation.mutate(v)}
              />
            ))}
            <p className="text-xs text-muted-foreground">
              Fields marked * are required by convention — the API does not
              block saving without a value.
            </p>
          </>
        )}
      </CardContent>
    </Card>
  );
}
