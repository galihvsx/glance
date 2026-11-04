import { SlidersHorizontal } from "lucide-react";
import { buttonVariants } from "../ui/button";
import { Label } from "../ui/label";
import { Popover, PopoverContent, PopoverTrigger } from "../ui/popover";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "../ui/select";
import { Separator } from "../ui/separator";
import { Skeleton } from "../ui/skeleton";
import { Switch } from "../ui/switch";
import { cn } from "cn";
import {
  CUSTOM_FIELD_TYPES,
  type CustomField,
} from "../../lib/customFields";
import type { GroupBy, SubGroupBy } from "./useDisplaySettings";

// C147T3: same option order as DisplayPanel's GROUP_OPTIONS /
// SUB_GROUP_OPTIONS (the list/board precedent). The sub-group list
// leads with None and otherwise mirrors the primary labels; the
// active primary dimension's option is disabled (greyed out, not
// hidden — Plane greys it out), and the whole sub-group control is
// disabled while groupBy is "none".
const GROUP_OPTIONS: { value: GroupBy; label: string }[] = [
  { value: "state", label: "State" },
  { value: "priority", label: "Priority" },
  { value: "epic", label: "Epic" },
  { value: "assignees", label: "Assignees" },
  { value: "labels", label: "Labels" },
  { value: "modules", label: "Modules" },
  { value: "cycles", label: "Cycles" },
  { value: "releases", label: "Releases" },
  { value: "createdBy", label: "Created by" },
  { value: "stateGroups", label: "State groups" },
  { value: "none", label: "None" },
];

const SUB_GROUP_OPTIONS: { value: SubGroupBy; label: string }[] = [
  { value: "none", label: "None" },
  { value: "state", label: "State" },
  { value: "priority", label: "Priority" },
  { value: "epic", label: "Epic" },
  { value: "assignees", label: "Assignees" },
  { value: "labels", label: "Labels" },
  { value: "createdBy", label: "Created by" },
  { value: "stateGroups", label: "State groups" },
  { value: "cycles", label: "Cycles" },
  { value: "modules", label: "Modules" },
  { value: "releases", label: "Releases" },
];

const typeLabel = (t: CustomField["field_type"]) =>
  CUSTOM_FIELD_TYPES.find((x) => x.value === t)?.label ?? t;

/**
 * C9T4: spreadsheet display settings — per custom-field column toggles.
 * Every column is off by default; enabling one adds a read-only column to
 * the grid (values format exactly as in the issue-detail custom-fields
 * section). Editing stays in the issue detail / peek drawer, where the
 * validation and write paths already live.
 */
export default function SpreadsheetDisplayPanel({
  fields,
  isLoading,
  isError,
  onRetry,
  visible,
  onToggle,
  onReset,
  showSubWorkItems,
  onToggleShowSubWorkItems,
  onResetShowSubWorkItems,
  groupBy,
  subGroupBy,
  onGroupByChange,
  onSubGroupByChange,
  onResetGrouping,
  triggerClassName,
}: {
  fields: CustomField[];
  isLoading: boolean;
  isError: boolean;
  onRetry: () => void;
  /** Enabled field ids (sparse map). */
  visible: Record<string, boolean>;
  onToggle: (fieldId: string) => void;
  onReset: () => void;
  /** C143T1: spreadsheet "Show sub-work items" — own persisted key
   *  (useSpreadsheetSubWorkItems), default ON, never shared with the
   *  per-project useDisplaySettings used by list/board. */
  showSubWorkItems: boolean;
  onToggleShowSubWorkItems: () => void;
  onResetShowSubWorkItems: () => void;
  /** C147T3: spreadsheet grouping (useSpreadsheetGrouping — its own
   *  localStorage key, never useDisplaySettings, so a selection here
   *  never flips list/board rendering). Changes apply live to the
   *  grid: no reload, no refetch. */
  groupBy: GroupBy;
  subGroupBy: SubGroupBy;
  onGroupByChange: (next: GroupBy) => void;
  onSubGroupByChange: (next: SubGroupBy) => void;
  onResetGrouping: () => void;
  /** Extra classes for the popover trigger (C19T4: PageHeader restyles it). */
  triggerClassName?: string;
}) {
  const enabledCount = fields.filter((f) => visible[f.id]).length;

  return (
    <Popover>
      {/* Base-UI Trigger renders its own button (no asChild) — style it
          with the shadcn button variants directly. */}
      <PopoverTrigger
        className={cn(
          buttonVariants({ variant: "outline", size: "sm" }),
          "gap-2",
          triggerClassName,
        )}
        aria-label="Display properties"
      >
        <SlidersHorizontal className="h-4 w-4" />
        Display
        {enabledCount > 0 && (
          <span className="rounded-full bg-primary/15 px-1.5 text-[11px] font-medium text-primary">
            {enabledCount}
          </span>
        )}
      </PopoverTrigger>
      <PopoverContent className="w-72 p-2" align="end">
        <div className="space-y-3 py-1">
          {/* C147T3: grouping — same section style (uppercase label +
              controls) as the sections below. Select is the shared
              shadcn/Base-UI Select DisplayPanel uses for Order by;
              no new UI dependency. The sub-group control is disabled
              entirely while groupBy is "none", and the option equal
              to the active groupBy is disabled (DisplayPanel
              precedent); the same-dimension render rule itself
              lives in effectiveSubGroupBy. */}
          <div className="space-y-1.5">
            <p className="px-2 text-[11px] font-medium uppercase tracking-wide text-muted-foreground">
              Grouping
            </p>
            <div className="space-y-1 px-2">
              <p className="text-xs font-medium text-muted-foreground">
                Group by
              </p>
              <Select
                value={groupBy}
                onValueChange={(v) => onGroupByChange(v as GroupBy)}
              >
                <SelectTrigger className="w-full" aria-label="Group issues by">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {GROUP_OPTIONS.map((o) => (
                    <SelectItem key={o.value} value={o.value}>
                      {o.label}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
            <div className="space-y-1 px-2">
              <p className="text-xs font-medium text-muted-foreground">
                Sub-group by
              </p>
              <Select
                value={subGroupBy}
                onValueChange={(v) => onSubGroupByChange(v as SubGroupBy)}
                disabled={groupBy === "none"}
              >
                <SelectTrigger
                  className="w-full"
                  aria-label="Sub-group issues by"
                >
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {SUB_GROUP_OPTIONS.map((o) => (
                    <SelectItem
                      key={o.value}
                      value={o.value}
                      disabled={o.value === groupBy}
                    >
                      {o.label}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
              {groupBy === "none" && (
                <p className="text-xs text-muted-foreground">
                  Choose a Group by first
                </p>
              )}
            </div>
          </div>

          <Separator />

          {/* C143T1: sub-work items — same switch component + section
              style as the custom-field toggles below. */}
          <div className="space-y-1">
            <p className="px-2 text-[11px] font-medium uppercase tracking-wide text-muted-foreground">
              Sub-work items
            </p>
            <Label className="flex cursor-pointer items-center justify-between gap-2 rounded-md px-2 py-1.5 text-sm hover:bg-accent">
              <span>Show sub-work items</span>
              <Switch
                size="sm"
                checked={showSubWorkItems}
                onCheckedChange={() => onToggleShowSubWorkItems()}
                aria-label="Show sub-work items"
              />
            </Label>
          </div>

          <Separator />

          <div className="space-y-1">
            <p className="px-2 text-[11px] font-medium uppercase tracking-wide text-muted-foreground">
              Custom field columns
            </p>
            {isLoading ? (
              <div className="space-y-2 px-2 py-1">
                <Skeleton className="h-7 w-full" />
                <Skeleton className="h-7 w-full" />
              </div>
            ) : isError ? (
              <p className="px-2 py-1 text-sm text-muted-foreground">
                Failed to load custom fields.{" "}
                <button
                  type="button"
                  onClick={onRetry}
                  className="text-primary hover:underline"
                >
                  Retry
                </button>
              </p>
            ) : fields.length === 0 ? (
              <p className="px-2 py-1 text-sm text-muted-foreground">
                No custom fields in this project yet. Add them in Project
                settings → Custom fields.
              </p>
            ) : (
              fields.map((f) => (
                <Label
                  key={f.id}
                  className="flex cursor-pointer items-center justify-between gap-2 rounded-md px-2 py-1.5 text-sm hover:bg-accent"
                  title={`${typeLabel(f.field_type)} field — read-only in the spreadsheet`}
                >
                  <span className="min-w-0 flex-1 truncate">{f.name}</span>
                  <span className="shrink-0 text-[11px] text-muted-foreground">
                    {typeLabel(f.field_type)}
                  </span>
                  <Switch
                    size="sm"
                    checked={visible[f.id] === true}
                    onCheckedChange={() => onToggle(f.id)}
                    aria-label={`Show ${f.name} column`}
                  />
                </Label>
              ))
            )}
          </div>

          <Separator />

          <p className="px-2 text-xs text-muted-foreground">
            Columns are read-only — edit values in the issue detail or peek
            drawer.
          </p>

          <button
            type="button"
            onClick={() => {
              // Reset to defaults covers the whole panel: custom-field
              // columns off, sub-work items back to its default ON,
              // and grouping back to none/none.
              onReset();
              onResetShowSubWorkItems();
              onResetGrouping();
            }}
            className="w-full rounded-md px-2 py-1.5 text-left text-xs text-muted-foreground hover:bg-accent hover:text-foreground"
          >
            Reset to defaults
          </button>
        </div>
      </PopoverContent>
    </Popover>
  );
}
