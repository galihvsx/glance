import { useState } from "react";
import { UserPlus } from "lucide-react";
import AssigneePicker from "../issue/AssigneePicker";
import { Button } from "../ui/button";
import { Checkbox } from "../ui/checkbox";
import { cn } from "../../lib/utils";
import type { IssueAssignee, Member } from "../../lib/types";

export type IntakeBulkAction = "accept" | "reject";

export interface IntakeBulkApply {
  action: IntakeBulkAction;
  /** Workspace member user id, accept only. Omitted when unset. */
  assignee_id?: string;
}

/** Staged accept/reject toggle, using the intake row's triage button
 *  language (compact, colored when armed). */
function actionBtnClass(kind: IntakeBulkAction, armed: boolean): string {
  return cn(
    "inline-flex h-7 shrink-0 items-center rounded-[4px] border px-2.5 text-xs font-medium transition-colors",
    "disabled:cursor-not-allowed disabled:opacity-50",
    kind === "accept" &&
      armed &&
      "border-accent-blue bg-accent-blue text-white hover:bg-accent-blue-hover",
    kind === "reject" &&
      armed &&
      "border-accent-red bg-accent-red text-white",
    !armed &&
      "border-border bg-surface text-ink-secondary hover:bg-raised hover:text-ink",
  );
}

/** Bulk triage bar (C25T2): appears above the intake inbox when at least
 *  one row is selected. Matches the BulkActionBar visual language
 *  (sticky bar, "N selected", select-all, Clear): stages one action
 *  (Accept | Reject) plus an optional assignee (accept only) and applies
 *  them to every selected intake row with a single POST
 *  .../intake/bulk-triage call. The server reports per-item results, so
 *  partial success is the norm, not an error. */
export default function IntakeBulkActionBar({
  selectedCount,
  loadedCount,
  allLoadedSelected,
  onToggleSelectAll,
  onClearSelection,
  members,
  onApply,
  applying,
  error,
}: {
  selectedCount: number;
  loadedCount: number;
  allLoadedSelected: boolean;
  onToggleSelectAll: () => void;
  onClearSelection: () => void;
  members: Member[] | null;
  onApply: (apply: IntakeBulkApply) => void;
  applying: boolean;
  error: string | null;
}) {
  const [action, setAction] = useState<IntakeBulkAction | null>(null);
  const [assigneeId, setAssigneeId] = useState<string | null>(null);

  const itemNoun = selectedCount === 1 ? "item" : "items";

  function toggleAssignee(userId: string, currentlyAssigned: boolean) {
    // Single-select in bulk mode: picking a member replaces, unchecking
    // the picked one clears.
    setAssigneeId(currentlyAssigned ? null : userId);
  }

  const assignedAssignees: IssueAssignee[] = assigneeId
    ? [
        {
          id: assigneeId,
          name:
            members?.find((m) => m.id === assigneeId)?.name ??
            members?.find((m) => m.id === assigneeId)?.email ??
            assigneeId,
        },
      ]
    : [];

  const assigneeDisabled = applying || action === "reject";

  return (
    <div
      className="sticky top-0 z-10 mb-4 rounded-lg border bg-background p-3 shadow-sm"
      role="region"
      aria-label="Bulk triage"
    >
      <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
        <span className="text-sm font-medium">
          {selectedCount} selected
        </span>
        <label className="flex cursor-pointer items-center gap-2 text-sm text-muted-foreground">
          <Checkbox
            checked={allLoadedSelected}
            onCheckedChange={() => onToggleSelectAll()}
            aria-label={`Select all ${loadedCount} loaded items`}
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
        <span className="flex items-center gap-2">
          <button
            type="button"
            className={actionBtnClass("accept", action === "accept")}
            onClick={() =>
              setAction((prev) => (prev === "accept" ? null : "accept"))
            }
            disabled={applying}
            aria-pressed={action === "accept"}
          >
            Accept
          </button>
          <button
            type="button"
            className={actionBtnClass("reject", action === "reject")}
            onClick={() =>
              setAction((prev) => (prev === "reject" ? null : "reject"))
            }
            disabled={applying}
            aria-pressed={action === "reject"}
          >
            Reject
          </button>
        </span>
        <span
          className="flex items-center gap-2 text-sm text-muted-foreground"
          title={
            action === "reject"
              ? "Assignee is ignored when rejecting"
              : undefined
          }
        >
          <AssigneePicker
            members={members}
            assigned={assignedAssignees}
            onToggle={toggleAssignee}
            disabled={assigneeDisabled}
            triggerContent={
              <>
                <UserPlus className="h-4 w-4" aria-hidden />
                {assigneeId ? "Assignee set" : "Assign…"}
              </>
            }
          />
        </span>
        <div className="ml-auto flex items-center gap-2">
          <Button
            size="sm"
            onClick={() => {
              if (action) {
                onApply(
                  assigneeId && action === "accept"
                    ? { action, assignee_id: assigneeId }
                    : { action },
                );
              }
            }}
            disabled={!action || applying}
          >
            {applying
              ? "Applying…"
              : `Apply to ${selectedCount} ${itemNoun}`}
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
