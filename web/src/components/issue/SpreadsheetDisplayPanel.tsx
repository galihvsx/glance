import { SlidersHorizontal } from "lucide-react";
import { buttonVariants } from "../ui/button";
import { Label } from "../ui/label";
import { Popover, PopoverContent, PopoverTrigger } from "../ui/popover";
import { Separator } from "../ui/separator";
import { Skeleton } from "../ui/skeleton";
import { Switch } from "../ui/switch";
import { cn } from "cn";
import {
  CUSTOM_FIELD_TYPES,
  type CustomField,
} from "../../lib/customFields";

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
}: {
  fields: CustomField[];
  isLoading: boolean;
  isError: boolean;
  onRetry: () => void;
  /** Enabled field ids (sparse map). */
  visible: Record<string, boolean>;
  onToggle: (fieldId: string) => void;
  onReset: () => void;
}) {
  const enabledCount = fields.filter((f) => visible[f.id]).length;

  return (
    <Popover>
      {/* Base-UI Trigger renders its own button (no asChild) — style it
          with the shadcn button variants directly. */}
      <PopoverTrigger
        className={cn(buttonVariants({ variant: "outline", size: "sm" }), "gap-2")}
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
