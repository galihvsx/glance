import { describe, expect, it } from "vitest";
import {
  buildCreateBody,
  buildPatchBody,
  cadenceSummary,
  nextRunRelative,
  validateScheduleForm,
  type RecurringSchedule,
  type ScheduleFormValues,
} from "./schedules";

const baseForm: ScheduleFormValues = {
  name: "Weekly retro",
  active: true,
  frequency: "weekly",
  interval: 1,
  daysOfWeek: [5],
  dayOfMonth: "",
  hour: 9,
  startsOn: "2026-10-12",
  endsOn: "",
  titleTemplate: "Retro {{date}}",
  descriptionTemplate: "",
  stateId: "",
  priority: 2,
  labelIds: [],
  assigneeIds: [],
};

const baseSchedule: RecurringSchedule = {
  id: "s1",
  workspace_id: "w1",
  project_id: "p1",
  name: "Weekly retro",
  active: true,
  frequency: "weekly",
  interval: 1,
  days_of_week: [5],
  day_of_month: null,
  hour: 9,
  starts_on: "2026-10-12T00:00:00Z",
  ends_on: null,
  next_run_at: "2026-10-16T09:00:00Z",
  last_run_at: null,
  run_count: 0,
  title_template: "Retro {{date}}",
  description_template: null,
  state_id: null,
  priority: 2,
  label_ids: [],
  assignee_ids: [],
  created_by: null,
  created_at: "2026-10-12T00:00:00Z",
  updated_at: "2026-10-12T00:00:00Z",
};

describe("cadenceSummary", () => {
  it("formats daily", () => {
    expect(
      cadenceSummary({ ...baseSchedule, frequency: "daily", days_of_week: [] }),
    ).toBe("Daily · 09:00 UTC");
  });

  it("formats daily with interval > 1", () => {
    expect(
      cadenceSummary({
        ...baseSchedule,
        frequency: "daily",
        interval: 3,
        days_of_week: [],
      }),
    ).toBe("Every 3 days · 09:00 UTC");
  });

  it("formats weekly on named weekdays", () => {
    expect(
      cadenceSummary({
        ...baseSchedule,
        frequency: "weekly",
        days_of_week: [1, 3],
      }),
    ).toBe("Weekly on Mon, Wed · 09:00 UTC");
  });

  it("formats weekly with interval > 1", () => {
    expect(
      cadenceSummary({
        ...baseSchedule,
        frequency: "weekly",
        interval: 2,
        days_of_week: [1, 3],
      }),
    ).toBe("Every 2 weeks on Mon, Wed · 09:00 UTC");
  });

  it("sorts weekday names Sunday-first", () => {
    expect(
      cadenceSummary({
        ...baseSchedule,
        frequency: "weekly",
        days_of_week: [5, 0],
      }),
    ).toBe("Weekly on Sun, Fri · 09:00 UTC");
  });

  it("formats monthly with an ordinal day", () => {
    expect(
      cadenceSummary({
        ...baseSchedule,
        frequency: "monthly",
        days_of_week: [],
        day_of_month: 31,
      }),
    ).toBe("Monthly on the 31st · 09:00 UTC");
  });

  it("formats monthly with interval > 1 and ordinals", () => {
    expect(
      cadenceSummary({
        ...baseSchedule,
        frequency: "monthly",
        interval: 3,
        days_of_week: [],
        day_of_month: 1,
      }),
    ).toBe("Every 3 months on the 1st · 09:00 UTC");
  });

  it("uses 2nd/3rd/21st/22nd/23rd ordinals", () => {
    const day = (d: number) =>
      cadenceSummary({
        ...baseSchedule,
        frequency: "monthly",
        days_of_week: [],
        day_of_month: d,
      });
    expect(day(2)).toContain("2nd");
    expect(day(3)).toContain("3rd");
    expect(day(21)).toContain("21st");
    expect(day(22)).toContain("22nd");
    expect(day(23)).toContain("23rd");
    expect(day(4)).toContain("4th");
  });

  it("handles weekly with no days gracefully", () => {
    expect(
      cadenceSummary({ ...baseSchedule, frequency: "weekly", days_of_week: [] }),
    ).toBe("Weekly · 09:00 UTC");
  });

  it("handles days_of_week arriving as JSON null", () => {
    expect(
      cadenceSummary({
        ...baseSchedule,
        frequency: "weekly",
        days_of_week: null,
      }),
    ).toBe("Weekly · 09:00 UTC");
  });

  it("pads single-digit hours", () => {
    expect(cadenceSummary({ ...baseSchedule, frequency: "daily", days_of_week: [], hour: 1 })).toBe(
      "Daily · 01:00 UTC",
    );
  });
});

describe("nextRunRelative", () => {
  const now = new Date("2026-10-12T06:00:00Z");

  it("formats minutes and hours in the future", () => {
    expect(nextRunRelative("2026-10-12T06:12:00Z", now)).toBe("in 12m");
    expect(nextRunRelative("2026-10-12T09:00:00Z", now)).toBe("in 3h");
  });

  it("formats seconds in the future", () => {
    expect(nextRunRelative("2026-10-12T06:00:30Z", now)).toBe("in 30s");
  });

  it("formats days and weeks in the future", () => {
    expect(nextRunRelative("2026-10-14T09:00:00Z", now)).toBe("in 2d");
    expect(nextRunRelative("2026-10-26T09:00:00Z", now)).toBe("in 2w");
  });

  it("marks a past run as overdue", () => {
    expect(nextRunRelative("2026-10-12T04:30:00Z", now)).toBe(
      "Overdue by 1h",
    );
  });

  it("treats a zero time as no further runs", () => {
    expect(nextRunRelative("0001-01-01T00:00:00Z", now)).toBe(
      "No further runs",
    );
  });

  it("falls back to a date for distant runs", () => {
    const far = nextRunRelative("2027-03-05T09:00:00Z", now);
    expect(far).toMatch(/Mar 5/);
  });
});

describe("validateScheduleForm", () => {
  it("accepts a valid form", () => {
    expect(validateScheduleForm(baseForm)).toEqual([]);
  });

  it("requires a name", () => {
    expect(validateScheduleForm({ ...baseForm, name: "  " })).toContain(
      "Name is required.",
    );
  });

  it("requires a title template", () => {
    expect(validateScheduleForm({ ...baseForm, titleTemplate: "" })).toContain(
      "Title template is required.",
    );
  });

  it("requires starts_on", () => {
    expect(validateScheduleForm({ ...baseForm, startsOn: "" })).toContain(
      "Start date is required.",
    );
  });

  it("requires at least one weekday for weekly", () => {
    expect(validateScheduleForm({ ...baseForm, daysOfWeek: [] })).toContain(
      "Weekly schedules need at least one day of the week.",
    );
  });

  it("requires a day of month for monthly", () => {
    const f = { ...baseForm, frequency: "monthly" as const, dayOfMonth: "" };
    expect(validateScheduleForm(f)).toContain(
      "Monthly schedules need a day of the month.",
    );
  });

  it("rejects day of month outside 1..31", () => {
    const f = {
      ...baseForm,
      frequency: "monthly" as const,
      dayOfMonth: "32",
    };
    expect(validateScheduleForm(f)).toContain(
      "Day of the month must be 1..31.",
    );
  });

  it("rejects ends_on before starts_on", () => {
    const f = { ...baseForm, endsOn: "2026-10-01" };
    expect(validateScheduleForm(f)).toContain(
      "End date cannot be before the start date.",
    );
  });

  it("rejects intervals below 1 and hours outside 0..23", () => {
    expect(validateScheduleForm({ ...baseForm, interval: 0 })).toContain(
      "Interval must be at least 1.",
    );
    expect(validateScheduleForm({ ...baseForm, hour: 24 })).toContain(
      "Hour must be 0..23.",
    );
  });

  it("does not require weekdays for daily", () => {
    const f = { ...baseForm, frequency: "daily" as const, daysOfWeek: [] };
    expect(validateScheduleForm(f)).toEqual([]);
  });
});

describe("buildCreateBody", () => {
  it("maps form fields to the API body", () => {
    expect(buildCreateBody(baseForm)).toEqual({
      name: "Weekly retro",
      active: true,
      frequency: "weekly",
      interval: 1,
      days_of_week: [5],
      hour: 9,
      starts_on: "2026-10-12",
      title_template: "Retro {{date}}",
      priority: 2,
      label_ids: [],
      assignee_ids: [],
    });
  });

  it("includes optional fields when set", () => {
    const body = buildCreateBody({
      ...baseForm,
      endsOn: "2026-12-31",
      descriptionTemplate: "Hello {{datetime}}",
      stateId: "state-1",
    });
    expect(body.ends_on).toBe("2026-12-31");
    expect(body.description_template).toBe("Hello {{datetime}}");
    expect(body.state_id).toBe("state-1");
  });

  it("includes day_of_month for monthly", () => {
    const body = buildCreateBody({
      ...baseForm,
      frequency: "monthly",
      daysOfWeek: [],
      dayOfMonth: "15",
    });
    expect(body.day_of_month).toBe(15);
  });
});

describe("buildPatchBody", () => {
  it("is empty when nothing changed", () => {
    expect(buildPatchBody(baseSchedule, baseForm)).toEqual({});
  });

  it("includes changed scalars", () => {
    const body = buildPatchBody(baseSchedule, {
      ...baseForm,
      name: "New name",
      active: false,
      priority: 0,
    });
    expect(body).toEqual({ name: "New name", active: false, priority: 0 });
  });

  it("clears nullable fields with explicit null (tri-state clear)", () => {
    const s = { ...baseSchedule, description_template: "old", ends_on: "2026-12-31T00:00:00Z" };
    const body = buildPatchBody(s, baseForm);
    expect(body.description_template).toBeNull();
    expect(body.ends_on).toBeNull();
  });

  it("sets nullable fields with values (tri-state set)", () => {
    const body = buildPatchBody(baseSchedule, {
      ...baseForm,
      endsOn: "2026-12-31",
      descriptionTemplate: "new",
      stateId: "state-9",
    });
    expect(body.ends_on).toBe("2026-12-31");
    expect(body.description_template).toBe("new");
    expect(body.state_id).toBe("state-9");
  });

  it("omits untouched nullable fields (tri-state absent)", () => {
    const s = {
      ...baseSchedule,
      description_template: "keep me",
      day_of_month: 15,
      ends_on: "2026-12-31T00:00:00Z",
    };
    const f = {
      ...baseForm,
      descriptionTemplate: "keep me",
      dayOfMonth: "15",
      endsOn: "2026-12-31",
    };
    const body = buildPatchBody(s, f);
    expect("description_template" in body).toBe(false);
    expect("day_of_month" in body).toBe(false);
    expect("ends_on" in body).toBe(false);
  });

  it("detects label/assignee changes as sets", () => {
    const f = { ...baseForm, labelIds: ["l1"], assigneeIds: ["u1"] };
    const body = buildPatchBody(baseSchedule, f);
    expect(body).toEqual({ label_ids: ["l1"], assignee_ids: ["u1"] });
  });

  it("ignores label ordering", () => {
    const s = { ...baseSchedule, label_ids: ["l1", "l2"] };
    const f = { ...baseForm, labelIds: ["l2", "l1"] };
    expect(buildPatchBody(s, f)).toEqual({});
  });

  it("handles starts_on date comparison against an ISO timestamp", () => {
    const body = buildPatchBody(baseSchedule, {
      ...baseForm,
      startsOn: "2026-10-13",
    });
    expect(body.starts_on).toBe("2026-10-13");
  });
});
