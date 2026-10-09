import { useState } from "react";
import StatePicker from "./StatePicker";
import PriorityPicker from "./PriorityPicker";
import LabelPicker from "./LabelPicker";
import AssigneePicker from "./AssigneePicker";
import { Button } from "../ui/button";
import { Checkbox } from "../ui/checkbox";
import type {
  IssueState,
  Label as ProjectLabel,
  Member,
} from "../../lib/types";
import {
  buildBulkSetPayload,
  type BulkSetPayload,
} from "./bulkSelection";

/** Bulk action bar (C5T8): appears above the issues list when at least
 *  one issue is selected. Stages one `set` (state / priority / labels /
 *  assignee) and applies it to every selected issue with a single
 *  PATCH /issues/bulk call. Untouched controls are omitted from the
 *  payload — "Labels" starts empty (untouched); toggling a label stages
 *  a full replacement, "Assignee" picks exactly one member (unchecking
 *  them stages an explicit clear). List view only. */
export default function BulkActionBar({
  states,
  members,
  labels,
  selectedCount,
  loadedCount,
  allLoadedSelected,
  onToggleSelectAll,
  onClearSelection,
  onApply,
  applying,
  error,
}: {
  states: IssueState[];
  members: Member[] | null;
  labels: ProjectLabel[] | null;
  selectedCount: number;
  loadedCount: number;
  allLoadedSelected: boolean;
  onToggleSelectAll: () => void;
  onClearSelection: () => void;
  onApply: (set: BulkSetPayload) => void;
  applying: boolean;
  error: string | null;
}) {
  const [stateId, setStateId] = useState<string | null>(null);
  const [priority, setPriority] = useState<number | null>(null);
  const [labelIds, setLabelIds] = useState<string[] | null>(null);
  const [assigneeId, setAssigneeId] = useState<string | null | undefined>(
    undefined,
  );

  const payload = buildBulkSetPayload({
    stateId,
    priority,
    labelIds,
    assigneeId,
  });

  function resetDraft() {
    setStateId(null);
    setPriority(null);
    setLabelIds(null);
    setAssigneeId(undefined);
  }

  function toggleLabel(labelId: string, currentlyApplied: boolean) {
    setLabelIds((prev) => {
      const base = prev ?? [];
      return currentlyApplied
        ? base.filter((id) => id !== labelId)
        : [...base, labelId];
    });
  }

  function toggleAssignee(userId: string, currentlyAssigned: boolean) {
    // Single-select in bulk mode: picking a member replaces, unchecking
    // the picked one stages an explicit clear (assignee_id: null).
    setAssigneeId(currentlyAssigned ? null : userId);
  }

  const appliedLabels = (labelIds ?? []).map((id) => {
    const l = labels?.find((x) => x.id === id);
    return { id, name: l?.name ?? id, color: l?.color ?? "#6b7280" };
  });
  const assigneeMember = assigneeId
    ? members?.find((m) => m.id === assigneeId)
    : undefined;
  const assignedAssignees = assigneeId
    ? [{ id: assigneeId, name: assigneeMember?.name ?? assigneeMember?.email ?? assigneeId }]
    : [];

  return (
    <div
      className="sticky top-0 z-10 mb-4 rounded-lg border bg-background p-3 shadow-sm"
      role="region"
      aria-label="Bulk actions"
    >
      <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
        <span className="text-sm font-medium">
          {selectedCount} selected
        </span>
        <label className="flex cursor-pointer items-center gap-2 text-sm text-muted-foreground">
          <Checkbox
            checked={allLoadedSelected}
            onCheckedChange={() => onToggleSelectAll()}
            aria-label={`Select all ${loadedCount} loaded issues`}
          />
          Select all {loadedCount} loaded
        </label>
        <Button variant="ghost" size="sm" onClick={onClearSelection}>
          Clear
        </Button>
        <div
          className="mx-1 hidden h-6 w-px bg-border sm:block"
          aria-hidden
        />
        <label className="flex items-center gap-2 text-sm text-muted-foreground">
          State
          <StatePicker
            states={states}
            value={stateId ?? ""}
            onChange={(v) => setStateId(v)}
            disabled={applying}
          />
        </label>
        <label className="flex items-center gap-2 text-sm text-muted-foreground">
          Priority
          <PriorityPicker
            value={priority ?? -1}
            onChange={(v) => setPriority(v)}
            disabled={applying}
          />
        </label>
        <span className="flex items-center gap-2 text-sm text-muted-foreground">
          Labels
          <LabelPicker
            labels={labels}
            applied={appliedLabels}
            onToggle={toggleLabel}
            disabled={applying}
          />
        </span>
        <span className="flex items-center gap-2 text-sm text-muted-foreground">
          Assignee
          <AssigneePicker
            members={members}
            assigned={assignedAssignees}
            onToggle={toggleAssignee}
            disabled={applying}
          />
        </span>
        <div className="ml-auto flex items-center gap-2">
          <Button
            variant="ghost"
            size="sm"
            onClick={resetDraft}
            disabled={!payload || applying}
          >
            Reset
          </Button>
          <Button
            size="sm"
            onClick={() => {
              if (payload) onApply(payload);
            }}
            disabled={!payload || applying}
          >
            {applying ? "Applying…" : "Apply"}
          </Button>
        </div>
      </div>
      {error && (
        <p className="mt-2 text-sm text-destructive" role="alert">
          {error}
        </p>
      )}
    </div>
  );
}
