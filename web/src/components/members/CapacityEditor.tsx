import { useEffect, useState } from "react";
import { api, ApiError } from "../../lib/api";
import { Button } from "../ui/button";
import { Input } from "../ui/input";
import { Label } from "../ui/label";
import { Popover, PopoverContent, PopoverTrigger } from "../ui/popover";
import { cn } from "cn";

/**
 * CapacityEditor: standalone popover for editing one workspace member's
 * weekly capacity (story points) — the budget the workload "overloaded"
 * flag is computed against.
 *
 * PUT /api/v1/workspaces/:slug/members/:userId/capacity
 *   { "weekly_capacity_points": <int|null> }  — member (15)+.
 *
 * The caller owns the member list and passes `capacity` + `canEdit`;
 * `onSaved` receives the persisted value (number or null) so the caller
 * can update its local state.
 */
export default function CapacityEditor({
  workspaceSlug,
  userId,
  displayName,
  capacity,
  canEdit,
  onSaved,
  className,
}: {
  workspaceSlug: string;
  userId: string;
  displayName: string;
  /** Current weekly capacity; null = unset. */
  capacity: number | null;
  /** False for guests (PUT requires member 15+): read-only badge. */
  canEdit: boolean;
  onSaved: (capacity: number | null) => void;
  className?: string;
}) {
  const [open, setOpen] = useState(false);
  const [draft, setDraft] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);

  // Reset the draft every time the popover opens.
  useEffect(() => {
    if (open) {
      setDraft(capacity === null ? "" : String(capacity));
      setError(null);
    }
  }, [open, capacity]);

  const triggerLabel =
    capacity === null ? "—" : `${capacity} pts`;

  async function save(value: number | null) {
    setSaving(true);
    setError(null);
    try {
      const res = await api.put<{
        user_id: string;
        weekly_capacity_points: number | null;
      }>(
        `/api/v1/workspaces/${encodeURIComponent(workspaceSlug)}/members/${encodeURIComponent(userId)}/capacity`,
        { weekly_capacity_points: value },
      );
      onSaved(res.weekly_capacity_points);
      setOpen(false);
    } catch (e) {
      setError(
        e instanceof ApiError ? e.message : "Failed to save capacity",
      );
    } finally {
      setSaving(false);
    }
  }

  function handleSave() {
    const trimmed = draft.trim();
    if (trimmed === "") {
      // Empty input = clear back to null (unset).
      void save(null);
      return;
    }
    if (!/^\d+$/.test(trimmed)) {
      setError("Capacity must be a whole number 0–999, or empty to clear.");
      return;
    }
    const n = Number(trimmed);
    if (n > 999) {
      setError("Capacity must be 0–999.");
      return;
    }
    void save(n);
  }

  if (!canEdit) {
    return (
      <span
        className={cn(
          "inline-flex items-center rounded-md px-2 py-1 text-sm text-muted-foreground",
          className,
        )}
        title={`${displayName}'s weekly capacity${capacity === null ? " is not set" : `: ${capacity} pts`}`}
      >
        {triggerLabel}
      </span>
    );
  }

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger
        className={cn(
          "inline-flex items-center rounded-md border border-input bg-background px-2 py-1 text-sm shadow-xs transition-colors hover:bg-accent hover:text-accent-foreground",
          className,
        )}
        title={`Edit ${displayName}'s weekly capacity`}
      >
        {triggerLabel}
      </PopoverTrigger>
      <PopoverContent className="w-60 p-3" align="start">
        <div className="flex flex-col gap-2.5">
          <div className="flex flex-col gap-1">
            <Label htmlFor="capacity-input">Weekly capacity (pts)</Label>
            <p className="text-xs text-muted-foreground">
              {displayName} is flagged overloaded when open assigned work
              exceeds this. Empty = no budget tracked.
            </p>
          </div>
          <Input
            id="capacity-input"
            inputMode="numeric"
            placeholder="e.g. 8"
            value={draft}
            disabled={saving}
            onChange={(e) => setDraft(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter") handleSave();
            }}
          />
          {error !== null && (
            <p role="alert" className="text-xs text-destructive">
              {error}
            </p>
          )}
          <div className="flex justify-between gap-2">
            <Button
              variant="ghost"
              size="sm"
              disabled={saving}
              onClick={() => void save(null)}
            >
              Clear
            </Button>
            <Button size="sm" disabled={saving} onClick={handleSave}>
              {saving ? "Saving…" : "Save"}
            </Button>
          </div>
        </div>
      </PopoverContent>
    </Popover>
  );
}
