// Webhook event filter logic (C69T1). Pure client-side mirrors of the
// backend contract in internal/service/webhook_filter.go — the tests pin
// the vocabulary so backend drift fails loudly.
import { describe, expect, it } from "vitest";
import {
  buildFilterPatch,
  conditionSummary,
  filterSummaryText,
  STATE_GROUP_OPTIONS,
  validateWebhookCondition,
  WEBHOOK_FILTER_FIELDS,
  type LookupMaps,
  type WebhookCondition,
} from "./webhookFilter";

const LOOKUPS: LookupMaps = {
  projects: new Map([["p1", "Website"]]),
  members: new Map([
    ["u1", "Alice"],
    ["u2", "Bob"],
  ]),
  labels: new Map([["l1", "Feature"]]),
};

describe("WEBHOOK_FILTER_FIELDS vocabulary", () => {
  it("pins the UI-exposed field set (state_id is deliberately omitted)", () => {
    expect(WEBHOOK_FILTER_FIELDS.map((f) => f.value)).toEqual([
      "priority",
      "state_group",
      "assignee_id",
      "label_id",
      "project_id",
      "target_date",
    ]);
  });
});

describe("STATE_GROUP_OPTIONS vocabulary", () => {
  it("mirrors Plane's 5 groups — triage is NOT filterable (backend rejects)", () => {
    expect(STATE_GROUP_OPTIONS.map((o) => o.value)).toEqual([
      "backlog",
      "unstarted",
      "started",
      "completed",
      "cancelled",
    ]);
  });
});

describe("validateWebhookCondition", () => {
  it("accepts a valid eq condition", () => {
    expect(
      validateWebhookCondition({
        field: "priority",
        op: "eq",
        value: "high",
      }),
    ).toBeNull();
  });

  it("accepts a valid in condition", () => {
    expect(
      validateWebhookCondition({
        field: "state_group",
        op: "in",
        value: ["started", "completed"],
      }),
    ).toBeNull();
  });

  it("rejects an unknown field", () => {
    expect(
      validateWebhookCondition({ field: "color", op: "eq", value: "red" }),
    ).toMatch(/[Uu]nknown field/);
  });

  it("rejects an unknown op", () => {
    expect(
      validateWebhookCondition({
        field: "priority",
        // The typed op vocabulary only allows eq/in; an untyped payload
        // (hand-rolled JSON, server data) can still carry anything.
        op: "ne" as WebhookCondition["op"],
        value: "high",
      }),
    ).toMatch(/[Uu]nknown op/);
  });

  it("rejects an empty in-array (backend requires non-empty)", () => {
    expect(
      validateWebhookCondition({ field: "priority", op: "in", value: [] }),
    ).toMatch(/non-empty/);
  });

  it("rejects triage as a state_group value (backend 400s it)", () => {
    expect(
      validateWebhookCondition({
        field: "state_group",
        op: "eq",
        value: "triage",
      }),
    ).toMatch(/State group/);
  });

  it("rejects a malformed target_date", () => {
    expect(
      validateWebhookCondition({
        field: "target_date",
        op: "eq",
        value: "tomorrow",
      }),
    ).toMatch(/YYYY-MM-DD/);
  });

  it("rejects a non-UUID assignee_id", () => {
    expect(
      validateWebhookCondition({
        field: "assignee_id",
        op: "eq",
        value: "alice",
      }),
    ).toMatch(/UUID/);
  });
});

describe("conditionSummary", () => {
  it("names a priority eq condition", () => {
    expect(
      conditionSummary(
        { field: "priority", op: "eq", value: "high" },
        LOOKUPS,
      ),
    ).toBe("Priority is High");
  });

  it("names a multi-value state_group condition", () => {
    expect(
      conditionSummary(
        { field: "state_group", op: "in", value: ["started", "completed"] },
        LOOKUPS,
      ),
    ).toBe("State group is one of: In progress, Done");
  });

  it("resolves assignee names, falling back to the raw id", () => {
    expect(
      conditionSummary(
        { field: "assignee_id", op: "in", value: ["u1", "unknown-id"] },
        LOOKUPS,
      ),
    ).toBe("Assignee is one of: Alice, unknown-id");
  });

  it("names a target_date condition", () => {
    expect(
      conditionSummary(
        { field: "target_date", op: "eq", value: "2026-10-20" },
        LOOKUPS,
      ),
    ).toBe("Target date is 2026-10-20");
  });
});

describe("filterSummaryText", () => {
  it("reports no filters for null or empty", () => {
    expect(filterSummaryText(null)).toBe("No filters");
    expect(filterSummaryText([])).toBe("No filters");
  });

  it("counts conditions", () => {
    expect(
      filterSummaryText([
        { field: "priority", op: "eq", value: "high" },
        { field: "label_id", op: "eq", value: "l1" },
      ]),
    ).toBe("2 filters");
  });
});

describe("buildFilterPatch tri-state", () => {
  it("sends the array to set a filter", () => {
    const conds = [{ field: "priority", op: "eq", value: "high" }] as const;
    const body = buildFilterPatch([...conds]);
    expect(body).toEqual({ filter: [...conds] });
  });

  it("sends explicit null to clear, and JSON keeps the key", () => {
    const json = JSON.stringify(buildFilterPatch(null));
    expect(json).toBe('{"filter":null}');
  });

  it("omits the key entirely when untouched (JSON drops undefined)", () => {
    const json = JSON.stringify(buildFilterPatch(undefined));
    expect(json).toBe("{}");
  });
});
