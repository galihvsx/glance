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
import { Switch } from "../ui/switch";
import { cn } from "cn";
import type {
  DisplayFields,
  DisplaySettings,
  GroupBy,
  OrderBy,
  SubGroupBy,
} from "./useDisplaySettings";

const FIELD_ROWS: { key: keyof DisplayFields; label: string; hint: string }[] = [
  { key: "state", label: "State", hint: "State badge on list rows" },
  { key: "priority", label: "Priority", hint: "Priority label on cards" },
  { key: "labels", label: "Labels", hint: "Label chips on cards" },
  { key: "assignees", label: "Assignees", hint: "Assignee chips on cards" },
  { key: "dates", label: "Dates", hint: "Start → target date line" },
  // C121T1: Plane's panel order after the existing rows.
  { key: "id", label: "ID", hint: "Display ID badge on cards" },
  { key: "subitems", label: "Sub-items", hint: "Subtask progress bar on cards" },
  { key: "attachments", label: "Attachments", hint: "Attachment count on cards" },
  { key: "links", label: "Link", hint: "Link count on cards" },
  { key: "dependencies", label: "Dependencies", hint: "Dependency count on cards" },
  { key: "modules", label: "Module", hint: "Module chips on cards" },
  { key: "cycle", label: "Cycle", hint: "Cycle chip on cards" },
  { key: "release", label: "Releases", hint: "Release chip on cards" },
];

const GROUP_OPTIONS: { value: GroupBy; label: string }[] = [
  { value: "state", label: "State" },
  { value: "priority", label: "Priority" },
  { value: "epic", label: "Epic" },
  { value: "assignees", label: "Assignees" },
  { value: "none", label: "None" },
];

// C123T0: second grouping dimension inside each primary group. Same
// labels as the primary options; the active primary dimension's option
// is disabled (greyed out, not hidden — Plane greys it out).
const SUB_GROUP_OPTIONS: { value: SubGroupBy; label: string }[] = [
  { value: "none", label: "None" },
  { value: "state", label: "State" },
  { value: "priority", label: "Priority" },
  { value: "epic", label: "Epic" },
];

const ORDER_OPTIONS: { value: OrderBy; label: string }[] = [
  { value: "-updated_at", label: "Recently updated" },
  { value: "updated_at", label: "Least recently updated" },
  { value: "-created_at", label: "Recently created" },
  { value: "created_at", label: "Oldest first" },
  { value: "-priority", label: "Priority: high to low" },
  { value: "priority", label: "Priority: low to high" },
  { value: "sequence_id", label: "Sequence: low to high" },
  { value: "-sequence_id", label: "Sequence: high to low" },
  { value: "sort_order", label: "Manual rank" },
];

function SectionTitle({ children }: { children: string }) {
  return (
    <p className="px-2 text-[11px] font-medium uppercase tracking-wide text-muted-foreground">
      {children}
    </p>
  );
}

/** Plane-style Display properties: a non-modal popover. Every control
 *  applies live to the current view — there is no Apply button. */
export default function DisplayPanel({
  settings,
  onUpdate,
  onUpdateFields,
  onReset,
  view,
  triggerClassName,
}: {
  settings: DisplaySettings;
  onUpdate: (patch: Partial<DisplaySettings>) => void;
  onUpdateFields: (patch: Partial<DisplayFields>) => void;
  onReset: () => void;
  /** "board" hides options that don't apply to the kanban (order, none-group). */
  view: "list" | "board";
  /** Extra classes for the popover trigger (C19T4: PageHeader restyles it). */
  triggerClassName?: string;
}) {
  const groupOptions =
    view === "board"
      ? // C72T1: "epic" grouping is implemented on the board (epic
        // columns); only "none" stays list-only.
        GROUP_OPTIONS.filter((o) => o.value !== "none")
      : GROUP_OPTIONS;
  // The board's State field is meaningless (columns already imply it).
  const fieldRows =
    view === "board"
      ? FIELD_ROWS.filter((r) => r.key !== "state")
      : FIELD_ROWS;

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
      </PopoverTrigger>
      <PopoverContent className="w-72 p-2" align="end">
        <div className="space-y-3 py-1">
          <div className="space-y-1">
            <SectionTitle>Fields</SectionTitle>
            {fieldRows.map((row) => (
              <Label
                key={row.key}
                className="flex cursor-pointer items-center justify-between gap-2 rounded-md px-2 py-1.5 text-sm hover:bg-accent"
                title={row.hint}
              >
                <span>{row.label}</span>
                <Switch
                  size="sm"
                  checked={settings.fields[row.key]}
                  onCheckedChange={(v) => onUpdateFields({ [row.key]: v })}
                  aria-label={`Show ${row.label.toLowerCase()}`}
                />
              </Label>
            ))}
          </div>

          <Separator />

          <div className="space-y-1.5">
            <SectionTitle>Group by</SectionTitle>
            <div className="flex gap-1 px-2" role="radiogroup" aria-label="Group by">
              {groupOptions.map((o) => (
                <button
                  key={o.value}
                  type="button"
                  role="radio"
                  aria-checked={settings.groupBy === o.value}
                  onClick={() => onUpdate({ groupBy: o.value })}
                  className={cn(
                    "flex-1 rounded-md border px-2 py-1.5 text-xs font-medium transition-colors",
                    settings.groupBy === o.value
                      ? "border-primary bg-primary/10 text-primary"
                      : "border-transparent text-muted-foreground hover:bg-accent hover:text-foreground",
                  )}
                >
                  {o.label}
                </button>
              ))}
            </div>
            {/* C123T0: "Sub-group by" lives in the Group by section,
                directly under the Group by radios, same button style.
                The active primary dimension's option is disabled (not
                hidden); when groupBy is "none" the whole control is
                disabled with a hint. The effective rule (same-as-
                primary renders as "none") lives in subGroup.ts. */}
            <div className="space-y-1 pt-1">
              <p className="px-2 text-xs font-medium text-muted-foreground">
                Sub-group by
              </p>
              <div
                className="flex gap-1 px-2"
                role="radiogroup"
                aria-label="Sub-group by"
              >
                {SUB_GROUP_OPTIONS.map((o) => {
                  const disabled =
                    settings.groupBy === "none" ||
                    o.value === settings.groupBy;
                  return (
                    <button
                      key={o.value}
                      type="button"
                      role="radio"
                      aria-checked={settings.subGroupBy === o.value}
                      disabled={disabled}
                      onClick={() => onUpdate({ subGroupBy: o.value })}
                      className={cn(
                        "flex-1 rounded-md border px-2 py-1.5 text-xs font-medium transition-colors disabled:cursor-not-allowed disabled:opacity-50",
                        settings.subGroupBy === o.value
                          ? "border-primary bg-primary/10 text-primary"
                          : "border-transparent text-muted-foreground hover:bg-accent hover:text-foreground",
                      )}
                    >
                      {o.label}
                    </button>
                  );
                })}
              </div>
              {settings.groupBy === "none" && (
                <p className="px-2 text-xs text-muted-foreground">
                  Choose a Group by first
                </p>
              )}
            </div>
            <Label className="flex cursor-pointer items-center justify-between gap-2 rounded-md px-2 py-1.5 text-sm hover:bg-accent">
              <span>Show empty groups</span>
              <Switch
                size="sm"
                checked={settings.showEmptyGroups}
                onCheckedChange={(v) => onUpdate({ showEmptyGroups: v })}
                aria-label="Show empty groups"
              />
            </Label>
            {/* C122T1: board-only in effect (the list view stays flat
                this cycle), but the toggle lives with the other
                grouping controls — display settings are per-project
                and shared across views, so a list-view toggle still
                takes effect when the user switches to the board. */}
            <Label className="flex cursor-pointer items-center justify-between gap-2 rounded-md px-2 py-1.5 text-sm hover:bg-accent">
              <span>Show sub-work items</span>
              <Switch
                size="sm"
                checked={settings.showSubWorkItems}
                onCheckedChange={(v) => onUpdate({ showSubWorkItems: v })}
                aria-label="Show sub-work items"
              />
            </Label>
          </div>

          {view === "list" && (
            <>
              <Separator />
              <div className="space-y-1.5">
                <SectionTitle>Order by</SectionTitle>
                <div className="px-2">
                  <Select
                    value={settings.orderBy}
                    onValueChange={(v) => onUpdate({ orderBy: v as OrderBy })}
                  >
                    <SelectTrigger className="w-full" aria-label="Order issues by">
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      {ORDER_OPTIONS.map((o) => (
                        <SelectItem key={o.value} value={o.value}>
                          {o.label}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                </div>
              </div>
            </>
          )}

          <Separator />

          <button
            type="button"
            onClick={onReset}
            className="w-full rounded-md px-2 py-1.5 text-left text-xs text-muted-foreground hover:bg-accent hover:text-foreground"
          >
            Reset to defaults
          </button>
        </div>
      </PopoverContent>
    </Popover>
  );
}
