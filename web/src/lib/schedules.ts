// schedules: API types, cadence formatting, builder validation, and the
// tri-state PATCH body builder for recurring issue schedules (C28T3).

export type ScheduleFrequency = "daily" | "weekly" | "monthly";

/** A recurring issue schedule as returned by the API (never null arrays). */
export interface RecurringSchedule {
  id: string;
  workspace_id: string;
  project_id: string;
  name: string;
  active: boolean;
  frequency: ScheduleFrequency;
  interval: number;
  // days_of_week may arrive as JSON null (no omitempty on the Go slice).
  days_of_week: number[] | null;
  day_of_month?: number | null; // omitted when null (omitempty)
  hour: number;
  starts_on: string; // ISO timestamp (date part is the UTC date)
  ends_on?: string | null; // ISO timestamp; omitted when null (omitempty)
  next_run_at: string; // ISO timestamp; year 0001 when exhausted
  last_run_at?: string | null;
  run_count: number;
  title_template: string;
  description_template?: string | null;
  state_id?: string | null;
  priority: number; // 0..4
  // label/assignee lists have no omitempty: they arrive as null when empty.
  label_ids: string[] | null;
  assignee_ids: string[] | null;
  created_by?: string | null;
  created_at: string;
  updated_at: string;
}

/** Builder form model. Empty strings mean "unset" for the nullable fields. */
export interface ScheduleFormValues {
  name: string;
  active: boolean;
  frequency: ScheduleFrequency;
  interval: number;
  daysOfWeek: number[];
  dayOfMonth: string; // "" = unset
  hour: number;
  startsOn: string; // YYYY-MM-DD
  endsOn: string; // "" = none
  titleTemplate: string;
  descriptionTemplate: string; // "" = none
  stateId: string; // "" = project default (backlog)
  priority: number;
  labelIds: string[];
  assigneeIds: string[];
}

export const WEEKDAY_NAMES = [
  "Sun",
  "Mon",
  "Tue",
  "Wed",
  "Thu",
  "Fri",
  "Sat",
] as const;

function ordinal(n: number): string {
  const mod100 = n % 100;
  if (mod100 >= 11 && mod100 <= 13) return `${n}th`;
  switch (n % 10) {
    case 1:
      return `${n}st`;
    case 2:
      return `${n}nd`;
    case 3:
      return `${n}rd`;
    default:
      return `${n}th`;
  }
}

function hourLabel(hour: number): string {
  return `${String(hour).padStart(2, "0")}:00 UTC`;
}

/**
 * Human cadence summary, e.g. "Every 2 weeks on Mon, Wed · 09:00 UTC",
 * "Monthly on the 31st · 09:00 UTC", "Daily · 09:00 UTC".
 */
export function cadenceSummary(s: {
  frequency: ScheduleFrequency;
  interval: number;
  days_of_week: number[] | null; // null at runtime when no days
  day_of_month?: number | null; // omitted when null (omitempty)
  hour: number;
}): string {
  const time = hourLabel(s.hour);
  switch (s.frequency) {
    case "daily":
      return s.interval <= 1 ? `Daily · ${time}` : `Every ${s.interval} days · ${time}`;
    case "weekly": {
      const days = [...new Set(s.days_of_week ?? [])]
        .filter((d) => d >= 0 && d <= 6)
        .sort((a, b) => a - b)
        .map((d) => WEEKDAY_NAMES[d])
        .join(", ");
      const head =
        s.interval <= 1 ? "Weekly" : `Every ${s.interval} weeks`;
      return days ? `${head} on ${days} · ${time}` : `${head} · ${time}`;
    }
    case "monthly": {
      const head =
        s.interval <= 1 ? "Monthly" : `Every ${s.interval} months`;
      const day =
        s.day_of_month != null && s.day_of_month >= 1 && s.day_of_month <= 31
          ? ` on the ${ordinal(s.day_of_month)}`
          : "";
      return `${head}${day} · ${time}`;
    }
  }
}

const ZERO_TIME_PREFIX = "0001-01-01";

/**
 * Compact relative label for a next run: "in 12m", "in 3h", "Overdue by
 * 1h", "No further runs" for an exhausted schedule (zero time), or an
 * absolute date for distant runs.
 */
export function nextRunRelative(nextRunAt: string, now: Date = new Date()): string {
  if (!nextRunAt || nextRunAt.startsWith(ZERO_TIME_PREFIX)) {
    return "No further runs";
  }
  const then = new Date(nextRunAt).getTime();
  if (Number.isNaN(then)) return "No further runs";
  const diffSec = Math.floor((then - now.getTime()) / 1000);
  const abs = Math.abs(diffSec);
  const unit =
    abs < 60
      ? `${abs}s`
      : abs < 3600
        ? `${Math.floor(abs / 60)}m`
        : abs < 86400
          ? `${Math.floor(abs / 3600)}h`
          : abs < 7 * 86400
            ? `${Math.floor(abs / 86400)}d`
            : abs < 30 * 86400
              ? `${Math.floor(abs / (7 * 86400))}w`
              : null;
  if (unit) return diffSec >= 0 ? `in ${unit}` : `Overdue by ${unit}`;
  const d = new Date(then);
  return d.toLocaleDateString("en-US", { month: "short", day: "numeric" });
}

function isValidDate(s: string): boolean {
  return /^\d{4}-\d{2}-\d{2}$/.test(s) && !Number.isNaN(new Date(s).getTime());
}

/**
 * Frontend validation mirroring the backend messages (recurring.go):
 * weekly needs ≥1 weekday, monthly needs day 1..31, title required,
 * ends_on ≥ starts_on. Backend 400s are still surfaced verbatim as a
 * backstop — this is the fast path before the round trip.
 */
export function validateScheduleForm(v: ScheduleFormValues): string[] {
  const errors: string[] = [];
  if (!v.name.trim()) errors.push("Name is required.");
  if (!v.titleTemplate.trim()) errors.push("Title template is required.");
  if (!isValidDate(v.startsOn)) errors.push("Start date is required.");
  if (v.interval < 1) errors.push("Interval must be at least 1.");
  if (v.hour < 0 || v.hour > 23) errors.push("Hour must be 0..23.");
  if (v.frequency === "weekly" && v.daysOfWeek.length === 0) {
    errors.push("Weekly schedules need at least one day of the week.");
  }
  if (v.frequency === "monthly") {
    if (!v.dayOfMonth.trim()) {
      errors.push("Monthly schedules need a day of the month.");
    } else {
      const d = Number(v.dayOfMonth);
      if (!Number.isInteger(d) || d < 1 || d > 31) {
        errors.push("Day of the month must be 1..31.");
      }
    }
  }
  if (
    v.endsOn &&
    isValidDate(v.endsOn) &&
    isValidDate(v.startsOn) &&
    v.endsOn < v.startsOn
  ) {
    errors.push("End date cannot be before the start date.");
  }
  return errors;
}

/** POST body for creating a schedule; empty optionals are omitted. */
export function buildCreateBody(v: ScheduleFormValues): Record<string, unknown> {
  const body: Record<string, unknown> = {
    name: v.name.trim(),
    active: v.active,
    frequency: v.frequency,
    interval: v.interval,
    ...(v.frequency === "weekly"
      ? { days_of_week: [...v.daysOfWeek].sort((a, b) => a - b) }
      : {}),
    ...(v.frequency === "monthly" && v.dayOfMonth.trim()
      ? { day_of_month: Number(v.dayOfMonth) }
      : {}),
    hour: v.hour,
    starts_on: v.startsOn,
    title_template: v.titleTemplate,
    priority: v.priority,
    label_ids: v.labelIds,
    assignee_ids: v.assigneeIds,
  };
  if (v.endsOn) body.ends_on = v.endsOn;
  if (v.descriptionTemplate.trim()) body.description_template = v.descriptionTemplate;
  if (v.stateId) body.state_id = v.stateId;
  return body;
}

function sameIdSet(a: string[], b: string[]): boolean {
  if (a.length !== b.length) return false;
  const set = new Set(a);
  return b.every((x) => set.has(x));
}

/**
 * PATCH body with the backend's tri-state semantics:
 * - absent key = untouched (field unchanged)
 * - explicit null = clear (nullable field the form emptied)
 * - value = set (nullable or scalar that changed)
 * Empty result means "nothing to update" — the dialog disables Save then.
 */
export function buildPatchBody(
  orig: RecurringSchedule,
  v: ScheduleFormValues,
): Record<string, unknown> {
  const body: Record<string, unknown> = {};
  const set = (k: string, value: unknown) => {
    body[k] = value;
  };

  if (v.name.trim() !== orig.name) set("name", v.name.trim());
  if (v.active !== orig.active) set("active", v.active);
  if (v.frequency !== orig.frequency) set("frequency", v.frequency);
  if (v.interval !== orig.interval) set("interval", v.interval);
  if (
    JSON.stringify([...v.daysOfWeek].sort((a, b) => a - b)) !==
    JSON.stringify([...(orig.days_of_week ?? [])].sort((a, b) => a - b))
  ) {
    set("days_of_week", [...v.daysOfWeek].sort((a, b) => a - b));
  }
  // Tri-state: day_of_month
  {
    const formDay = v.dayOfMonth.trim();
    if (formDay === "") {
      if (orig.day_of_month != null) set("day_of_month", null);
    } else if (Number(formDay) !== orig.day_of_month) {
      set("day_of_month", Number(formDay));
    }
  }
  if (v.hour !== orig.hour) set("hour", v.hour);
  if (v.startsOn && v.startsOn !== orig.starts_on.slice(0, 10)) {
    set("starts_on", v.startsOn);
  }
  // Tri-state: ends_on
  {
    const formEnds = v.endsOn.trim();
    const origEnds = orig.ends_on ? orig.ends_on.slice(0, 10) : null;
    if (formEnds === "") {
      if (origEnds != null) set("ends_on", null);
    } else if (formEnds !== origEnds) {
      set("ends_on", formEnds);
    }
  }
  if (v.titleTemplate !== orig.title_template) set("title_template", v.titleTemplate);
  // Tri-state: description_template
  {
    const formDesc = v.descriptionTemplate;
    if (formDesc === "") {
      if (orig.description_template != null) set("description_template", null);
    } else if (formDesc !== (orig.description_template ?? "")) {
      set("description_template", formDesc);
    }
  }
  // Tri-state: state_id
  {
    if (v.stateId === "") {
      if (orig.state_id != null) set("state_id", null);
    } else if (v.stateId !== (orig.state_id ?? "")) {
      set("state_id", v.stateId);
    }
  }
  if (v.priority !== orig.priority) set("priority", v.priority);
  if (!sameIdSet(v.labelIds, orig.label_ids ?? [])) set("label_ids", v.labelIds);
  if (!sameIdSet(v.assigneeIds, orig.assignee_ids ?? []))
    set("assignee_ids", v.assigneeIds);
  return body;
}

/** Seed a builder form from an existing schedule (edit mode). */
export function formFromSchedule(s: RecurringSchedule): ScheduleFormValues {
  return {
    name: s.name,
    active: s.active,
    frequency: s.frequency,
    interval: s.interval,
    daysOfWeek: [...(s.days_of_week ?? [])],
    dayOfMonth: s.day_of_month != null ? String(s.day_of_month) : "",
    hour: s.hour,
    startsOn: s.starts_on.slice(0, 10),
    endsOn: s.ends_on ? s.ends_on.slice(0, 10) : "",
    titleTemplate: s.title_template,
    descriptionTemplate: s.description_template ?? "",
    stateId: s.state_id ?? "",
    priority: s.priority,
    labelIds: [...(s.label_ids ?? [])],
    assigneeIds: [...(s.assignee_ids ?? [])],
  };
}

/** Default form for creating a schedule. */
export function blankForm(today: string): ScheduleFormValues {
  return {
    name: "",
    active: true,
    frequency: "weekly",
    interval: 1,
    daysOfWeek: [],
    dayOfMonth: "",
    hour: 9,
    startsOn: today,
    endsOn: "",
    titleTemplate: "",
    descriptionTemplate: "",
    stateId: "",
    priority: 0,
    labelIds: [],
    assigneeIds: [],
  };
}
