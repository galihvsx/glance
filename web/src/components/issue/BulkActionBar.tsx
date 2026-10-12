import { useState } from "react";
import { Boxes, Repeat } from "lucide-react";
import StatePicker from "./StatePicker";
import PriorityPicker from "./PriorityPicker";
import LabelPicker from "./LabelPicker";
import AssigneePicker from "./AssigneePicker";
import BulkOrganizeDialog, { type OrganizeItem } from "./BulkOrganizeDialog";
import { Button } from "../ui/button";
import { Checkbox } from "../ui/checkbox";
import type {
  IssueState,
  Label as ProjectLabel,
  Member,
  Cycle,
  Module,
} from "../../lib/types";
import {
  buildBulkSetPayload,
  type BulkSetPayload,
} from "./bulkSelection";

/** Filter for the "Add to cycle…" dialog (C24T2): only upcoming and
 *  current cycles are addable; completed cycles are history. */
export function addableCycles(cycles: Cycle[] | null): Cycle[] {
  return (cycles ?? []).filter((c) => c.status !== "completed");
}

/** Bulk action bar (C5T8): appears above the issues list when at least
 *  one issue is selected. Stages one `set` (state / priority / labels /
 *  assignee) and applies it to every selected issue with a single
 *  PATCH /issues/bulk call. Untouched controls are omitted from the
 *  payload — "Labels" starts empty (untouched); toggling a label stages
 *  a full replacement, "Assignee" picks exactly one member (unchecking
 *  them stages an explicit clear). C24T2 adds "Add to cycle…" /
 *  "Add to module…" actions: each opens a picker dialog and POSTs the
 *  selected issue UUIDs to the membership endpoint (idempotent — the
 *  server skips already-members and returns {added: n}). List view only. */
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
  cycles,
  modules,
  structureLoading,
  onAddToCycle,
  onAddToModule,
  organizeApplying,
  organizeError,
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
  /** Project cycles/modules for the organize dialogs; null = not loaded. */
  cycles: Cycle[] | null;
  modules: Module[] | null;
  structureLoading: boolean;
  onAddToCycle: (cycleId: string) => void;
  onAddToModule: (moduleId: string) => void;
  /** True while a membership POST is in flight. */
  organizeApplying: boolean;
  organizeError: string | null;
}) {
  const [stateId, setStateId] = useState<string | null>(null);
  const [priority, setPriority] = useState<number | null>(null);
  const [labelIds, setLabelIds] = useState<string[] | null>(null);
  const [assigneeId, setAssigneeId] = useState<string | null | undefined>(
    undefined,
  );
  const [cycleOpen, setCycleOpen] = useState(false);
  const [moduleOpen, setModuleOpen] = useState(false);

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

  const busy = applying || organizeApplying;
  const issueNoun = selectedCount === 1 ? "issue" : "issues";

  const cycleItems: OrganizeItem[] = addableCycles(cycles).map((c) => ({
    id: c.id,
    name: c.name,
    meta: `${c.status === "current" ? "Active" : "Upcoming"} · ${c.start_date} → ${c.end_date}`,
  }));
  const moduleItems: OrganizeItem[] = (modules ?? []).map((m) => ({
    id: m.id,
    name: m.name,
    meta: `${m.status} · ${m.issue_count} ${m.issue_count === 1 ? "issue" : "issues"}`,
  }));

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
        <div
          className="mx-1 hidden h-6 w-px bg-border sm:block"
          aria-hidden
        />
        <Button
          variant="ghost"
          size="sm"
          onClick={() => setCycleOpen(true)}
          disabled={busy}
        >
          <Repeat className="mr-1 h-3.5 w-3.5" aria-hidden />
          Add to cycle…
        </Button>
        <Button
          variant="ghost"
          size="sm"
          onClick={() => setModuleOpen(true)}
          disabled={busy}
        >
          <Boxes className="mr-1 h-3.5 w-3.5" aria-hidden />
          Add to module…
        </Button>
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
      {(error || organizeError) && (
        <p className="mt-2 text-sm text-destructive" role="alert">
          {error ?? organizeError}
        </p>
      )}
      <BulkOrganizeDialog
        open={cycleOpen}
        onOpenChange={setCycleOpen}
        title="Add to cycle"
        description={`Add ${selectedCount} selected ${issueNoun} to a cycle. Issues already in the cycle are skipped.`}
        items={cycleItems}
        loading={structureLoading}
        emptyText="No upcoming or active cycles in this project."
        confirmLabel={`Add ${selectedCount} ${issueNoun}`}
        onConfirm={(id) => {
          setCycleOpen(false);
          onAddToCycle(id);
        }}
      />
      <BulkOrganizeDialog
        open={moduleOpen}
        onOpenChange={setModuleOpen}
        title="Add to module"
        description={`Add ${selectedCount} selected ${issueNoun} to a module. Issues already in the module are skipped.`}
        items={moduleItems}
        loading={structureLoading}
        emptyText="No modules in this project."
        confirmLabel={`Add ${selectedCount} ${issueNoun}`}
        onConfirm={(id) => {
          setModuleOpen(false);
          onAddToModule(id);
        }}
      />
    </div>
  );
}
