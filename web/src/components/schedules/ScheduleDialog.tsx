import { useEffect, useMemo, useState } from "react";
import { X } from "lucide-react";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "../ui/dialog";
import { Button } from "../ui/button";
import { Input } from "../ui/input";
import { Label } from "../ui/label";
import { Textarea } from "../ui/textarea";
import { Switch } from "../ui/switch";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "../ui/select";
import { Alert, AlertDescription } from "../ui/alert";
import StatePicker from "../issue/StatePicker";
import PriorityPicker from "../issue/PriorityPicker";
import LabelPicker from "../issue/LabelPicker";
import AssigneePicker from "../issue/AssigneePicker";
import type {
  IssueState,
  Label as ProjectLabel,
  Member,
} from "../../lib/types";
import {
  WEEKDAY_NAMES,
  blankForm,
  cadenceSummary,
  formFromSchedule,
  validateScheduleForm,
  type RecurringSchedule,
  type ScheduleFormValues,
  type ScheduleFrequency,
} from "../../lib/schedules";

const FREQUENCIES: { value: ScheduleFrequency; label: string }[] = [
  { value: "daily", label: "Daily" },
  { value: "weekly", label: "Weekly" },
  { value: "monthly", label: "Monthly" },
];

function intervalUnit(frequency: ScheduleFrequency): string {
  switch (frequency) {
    case "daily":
      return "days";
    case "weekly":
      return "weeks";
    case "monthly":
      return "months";
  }
}

export default function ScheduleDialog({
  open,
  onOpenChange,
  mode,
  initial,
  states,
  labels,
  members,
  today,
  saving,
  error,
  onSubmit,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  mode: "create" | "edit";
  initial: RecurringSchedule | null;
  states: IssueState[];
  labels: ProjectLabel[] | null;
  members: Member[] | null;
  today: string;
  saving: boolean;
  error: string | null;
  onSubmit: (values: ScheduleFormValues) => void;
}) {
  const [form, setForm] = useState<ScheduleFormValues>(() =>
    initial ? formFromSchedule(initial) : blankForm(today),
  );
  const [localErrors, setLocalErrors] = useState<string[]>([]);

  // Reset the form every time the dialog opens (fresh for create,
  // seeded from the schedule for edit).
  useEffect(() => {
    if (open) {
      setForm(initial ? formFromSchedule(initial) : blankForm(today));
      setLocalErrors([]);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open]);

  const preview = useMemo(
    () =>
      cadenceSummary({
        frequency: form.frequency,
        interval: form.interval,
        days_of_week: form.daysOfWeek,
        day_of_month: form.dayOfMonth.trim() ? Number(form.dayOfMonth) : null,
        hour: form.hour,
      }),
    [form.frequency, form.interval, form.daysOfWeek, form.dayOfMonth, form.hour],
  );

  const set = <K extends keyof ScheduleFormValues>(
    key: K,
    value: ScheduleFormValues[K],
  ) => setForm((f) => ({ ...f, [key]: value }));

  const toggleDay = (day: number) =>
    setForm((f) => ({
      ...f,
      daysOfWeek: f.daysOfWeek.includes(day)
        ? f.daysOfWeek.filter((d) => d !== day)
        : [...f.daysOfWeek, day].sort((a, b) => a - b),
    }));

  const toggleId = (ids: string[], id: string) =>
    ids.includes(id) ? ids.filter((x) => x !== id) : [...ids, id];

  const submit = () => {
    const errors = validateScheduleForm(form);
    setLocalErrors(errors);
    if (errors.length === 0) onSubmit(form);
  };

  const appliedLabels = useMemo(
    () =>
      (labels ?? [])
        .filter((l) => form.labelIds.includes(l.id))
        .map((l) => ({ id: l.id, name: l.name, color: l.color })),
    [labels, form.labelIds],
  );
  const appliedAssignees = useMemo(
    () =>
      (members ?? [])
        .filter((m) => form.assigneeIds.includes(m.id))
        .map((m) => ({ id: m.id, name: m.name ?? m.email })),
    [members, form.assigneeIds],
  );

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[90vh] overflow-y-auto sm:max-w-[600px]">
        <DialogHeader>
          <DialogTitle>
            {mode === "create" ? "New recurring rule" : "Edit recurring rule"}
          </DialogTitle>
          <DialogDescription>
            Create issues on a schedule — standups, reports, recurring chores.
            Times are UTC in this version.
          </DialogDescription>
        </DialogHeader>

        <div className="space-y-5">
          {(localErrors.length > 0 || error) && (
            <Alert variant="destructive">
              <AlertDescription>
                {error ?? localErrors.join(" ")}
              </AlertDescription>
            </Alert>
          )}

          {/* Identity */}
          <div className="flex items-end gap-4">
            <div className="flex-1 space-y-2">
              <Label htmlFor="sched-name">Name</Label>
              <Input
                id="sched-name"
                value={form.name}
                onChange={(e) => set("name", e.target.value)}
                placeholder="Weekly retro"
                maxLength={120}
              />
            </div>
            <div className="flex items-center gap-2 pb-2">
              <Switch
                id="sched-active"
                checked={form.active}
                onCheckedChange={(v) => set("active", v)}
              />
              <Label htmlFor="sched-active" className="text-[13px]">
                Active
              </Label>
            </div>
          </div>

          {/* Cadence */}
          <div className="space-y-3">
            <Label>Cadence</Label>
            <div className="flex gap-1 rounded-[6px] border border-border bg-wash p-1">
              {FREQUENCIES.map((f) => (
                <button
                  key={f.value}
                  type="button"
                  onClick={() => set("frequency", f.value)}
                  className={`flex-1 rounded-[4px] px-3 py-1.5 text-[13px] font-medium transition-colors ${
                    form.frequency === f.value
                      ? "bg-raised text-ink shadow-sm"
                      : "text-ink-muted hover:text-ink"
                  }`}
                >
                  {f.label}
                </button>
              ))}
            </div>

            <div className="flex flex-wrap items-center gap-x-6 gap-y-3">
              <div className="flex items-center gap-2">
                <span className="text-[13px] text-ink-muted">Every</span>
                <div className="flex items-center rounded-[6px] border border-border">
                  <button
                    type="button"
                    aria-label="Decrease interval"
                    onClick={() =>
                      set("interval", Math.max(1, form.interval - 1))
                    }
                    className="px-2 py-1 text-[13px] text-ink-muted hover:text-ink"
                  >
                    −
                  </button>
                  <span className="min-w-[2ch] text-center text-[13px] font-semibold text-ink">
                    {form.interval}
                  </span>
                  <button
                    type="button"
                    aria-label="Increase interval"
                    onClick={() =>
                      set("interval", Math.min(99, form.interval + 1))
                    }
                    className="px-2 py-1 text-[13px] text-ink-muted hover:text-ink"
                  >
                    +
                  </button>
                </div>
                <span className="text-[13px] text-ink-muted">
                  {intervalUnit(form.frequency)}
                </span>
              </div>

              <div className="flex items-center gap-2">
                <span className="text-[13px] text-ink-muted">at</span>
                <Select
                  value={String(form.hour)}
                  onValueChange={(v) => set("hour", Number(v))}
                >
                  <SelectTrigger className="w-28">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {Array.from({ length: 24 }, (_, h) => (
                      <SelectItem key={h} value={String(h)}>
                        {String(h).padStart(2, "0")}:00 UTC
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>
            </div>
            <p className="text-[12px] text-ink-muted">
              Schedule times are UTC in this version — 01:00 UTC is 09:00
              WITA.
            </p>

            {form.frequency === "weekly" && (
              <div className="flex flex-wrap gap-1.5">
                {WEEKDAY_NAMES.map((name, day) => {
                  const on = form.daysOfWeek.includes(day);
                  return (
                    <button
                      key={day}
                      type="button"
                      aria-pressed={on}
                      onClick={() => toggleDay(day)}
                      className={`rounded-[6px] border px-2.5 py-1 text-[13px] font-medium transition-colors ${
                        on
                          ? "border-accent bg-accent/15 text-ink"
                          : "border-border bg-wash text-ink-muted hover:text-ink"
                      }`}
                    >
                      {name}
                    </button>
                  );
                })}
              </div>
            )}

            {form.frequency === "monthly" && (
              <div className="flex items-center gap-2">
                <Label htmlFor="sched-dom" className="text-[13px]">
                  Day of month
                </Label>
                <Input
                  id="sched-dom"
                  type="number"
                  min={1}
                  max={31}
                  value={form.dayOfMonth}
                  onChange={(e) => set("dayOfMonth", e.target.value)}
                  className="w-20"
                  placeholder="1–31"
                />
                <span className="text-[12px] text-ink-muted">
                  Day 31 in a short month lands on its last day.
                </span>
              </div>
            )}

            <p className="text-[12px] font-medium text-ink-muted">
              Preview: {preview}
            </p>
          </div>

          {/* Window */}
          <div className="flex flex-wrap gap-4">
            <div className="space-y-2">
              <Label htmlFor="sched-starts">Starts on</Label>
              <Input
                id="sched-starts"
                type="date"
                value={form.startsOn}
                onChange={(e) => set("startsOn", e.target.value)}
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="sched-ends">Ends on (optional)</Label>
              <div className="flex items-center gap-2">
                <Input
                  id="sched-ends"
                  type="date"
                  value={form.endsOn}
                  onChange={(e) => set("endsOn", e.target.value)}
                />
                {form.endsOn && (
                  <button
                    type="button"
                    onClick={() => set("endsOn", "")}
                    className="text-ink-muted hover:text-ink"
                    aria-label="Clear end date"
                  >
                    <X className="size-4" />
                  </button>
                )}
              </div>
            </div>
          </div>

          {/* Templates */}
          <div className="space-y-2">
            <Label htmlFor="sched-title">Issue title template</Label>
            <Input
              id="sched-title"
              value={form.titleTemplate}
              onChange={(e) => set("titleTemplate", e.target.value)}
              placeholder="Sprint retro {{date}}"
              maxLength={255}
            />
            <p className="text-[12px] text-ink-muted">
              <code className="rounded bg-wash px-1">{"{{date}}"}</code>{" "}
              renders the run date (YYYY-MM-DD).
            </p>
          </div>
          <div className="space-y-2">
            <Label htmlFor="sched-desc">Description template (optional)</Label>
            <Textarea
              id="sched-desc"
              value={form.descriptionTemplate}
              onChange={(e) => set("descriptionTemplate", e.target.value)}
              rows={3}
              placeholder={"What went well?\n\nRun at {{datetime}}"}
            />
            <p className="text-[12px] text-ink-muted">
              <code className="rounded bg-wash px-1">{"{{date}}"}</code> and{" "}
              <code className="rounded bg-wash px-1">{"{{datetime}}"}</code>{" "}
              render the run time.
            </p>
          </div>

          {/* Target */}
          <div className="flex flex-wrap gap-4">
            <div className="space-y-2">
              <Label>State</Label>
              <div className="flex items-center gap-2">
                <StatePicker
                  states={states}
                  value={form.stateId}
                  onChange={(v) => set("stateId", v)}
                />
                {form.stateId && (
                  <button
                    type="button"
                    onClick={() => set("stateId", "")}
                    className="text-[12px] text-ink-muted hover:text-ink"
                  >
                    Default
                  </button>
                )}
              </div>
            </div>
            <div className="space-y-2">
              <Label>Priority</Label>
              <PriorityPicker
                value={form.priority}
                onChange={(v) => set("priority", v)}
              />
            </div>
            <div className="space-y-2">
              <Label>Labels</Label>
              <LabelPicker
                labels={labels}
                applied={appliedLabels}
                onToggle={(id, currentlyApplied) =>
                  set(
                    "labelIds",
                    currentlyApplied
                      ? form.labelIds.filter((x) => x !== id)
                      : toggleId(form.labelIds, id),
                  )
                }
              />
            </div>
            <div className="space-y-2">
              <Label>Assignees</Label>
              <AssigneePicker
                members={members}
                assigned={appliedAssignees}
                onToggle={(id, currentlyAssigned) =>
                  set(
                    "assigneeIds",
                    currentlyAssigned
                      ? form.assigneeIds.filter((x) => x !== id)
                      : toggleId(form.assigneeIds, id),
                  )
                }
              />
            </div>
          </div>
        </div>

        <DialogFooter>
          <Button variant="ghost" onClick={() => onOpenChange(false)}>
            Cancel
          </Button>
          <Button onClick={submit} disabled={saving}>
            {saving
              ? "Saving…"
              : mode === "create"
                ? "Create rule"
                : "Save changes"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
