// Unit tests for the automation builder draft model (C16T4): draft <->
// API shape conversion and validation, all backend-shape-true.

import { describe, expect, it } from "vitest";
import {
  draftFromRule,
  draftToInput,
  emptyDraftAction,
  emptyRuleDraft,
  type DraftAction,
  type RuleDraft,
} from "./automationDraft";
import type { AutomationRule } from "./automations";

const base: AutomationRule = {
  id: "rule-1",
  project_id: "p1",
  name: "Escalate stale",
  trigger: { type: "issue.stale", from_states: null, to_states: null, stale_days: 3 },
  actions: [{ type: "add_comment", body: "stale!" }],
  enabled: true,
  created_by: "u1",
  created_at: "2026-10-10T00:00:00Z",
  updated_at: "2026-10-10T00:00:00Z",
};

function draftOf(rule: AutomationRule): RuleDraft {
  return draftFromRule(rule);
}

function okDraft(rule: AutomationRule): RuleDraft {
  const d = draftFromRule(rule);
  d.name = d.name || "r";
  return d;
}

describe("draftFromRule", () => {
  it("round-trips a scheduled trigger with a window", () => {
    const d = draftOf(base);
    expect(d.trigger.type).toBe("issue.stale");
    expect(d.trigger.staleDays).toBe("3");
    expect(d.trigger.windowHours).toBe("");
  });

  it("loads label filters, priorities and the due-date mode", () => {
    const d = draftOf({
      ...base,
      trigger: {
        type: "issue.labels_changed",
        from_states: null,
        to_states: null,
        label_ids: ["l1", "l2"],
      },
      actions: [{ type: "set_due_date", due_date: "+7d" }],
    });
    expect(d.trigger.labelIds).toEqual(["l1", "l2"]);
    expect(d.actions[0].dueDateMode).toBe("relative");
    expect(d.actions[0].dueDateOffset).toBe("7");
    expect(d.actions[0].dueDate).toBe("");
  });

  it("loads an absolute due date", () => {
    const d = draftOf({
      ...base,
      actions: [{ type: "set_due_date", due_date: "2026-11-01" }],
    });
    expect(d.actions[0].dueDateMode).toBe("absolute");
    expect(d.actions[0].dueDate).toBe("2026-11-01");
  });
});

describe("draftToInput", () => {
  it("rejects an empty name", () => {
    const d = emptyRuleDraft();
    const r = draftToInput(d);
    expect(r.ok).toBe(false);
  });

  it("converts due_soon with an empty window to a nil window_hours (server default)", () => {
    const d = okDraft(base);
    d.name = "x";
    d.trigger.type = "issue.due_soon";
    d.trigger.windowHours = "";
    const r = draftToInput(d);
    expect(r.ok).toBe(true);
    if (r.ok) {
      expect(r.input.trigger.type).toBe("issue.due_soon");
      expect(r.input.trigger.window_hours).toBeNull();
    }
  });

  it("converts due_soon with an explicit window", () => {
    const d = okDraft(base);
    d.name = "x";
    d.trigger.type = "issue.due_soon";
    d.trigger.windowHours = "24";
    const r = draftToInput(d);
    expect(r.ok).toBe(true);
    if (r.ok) expect(r.input.trigger.window_hours).toBe(24);
  });

  it("rejects a non-positive window_hours with a human message", () => {
    const d = okDraft(base);
    d.name = "x";
    d.trigger.type = "issue.due_soon";
    d.trigger.windowHours = "0";
    const r = draftToInput(d);
    expect(r.ok).toBe(false);
    if (!r.ok) expect(r.error).toMatch(/hours/);
  });

  it("converts priority_changed filters", () => {
    const d = okDraft(base);
    d.name = "x";
    d.trigger.type = "issue.priority_changed";
    d.trigger.fromPriority = "4";
    d.trigger.toPriority = "";
    const r = draftToInput(d);
    expect(r.ok).toBe(true);
    if (r.ok) {
      expect(r.input.trigger.from_priorities).toEqual([4]);
      expect(r.input.trigger.to_priorities).toBeNull();
    }
  });

  it("converts labels_changed filters", () => {
    const d = okDraft(base);
    d.name = "x";
    d.trigger.type = "issue.labels_changed";
    d.trigger.labelIds = ["l1"];
    const r = draftToInput(d);
    expect(r.ok).toBe(true);
    if (r.ok) expect(r.input.trigger.label_ids).toEqual(["l1"]);
  });

  it("sends no filter fields for filterless triggers", () => {
    const d = okDraft(base);
    d.name = "x";
    d.trigger.type = "issue.comment_added";
    d.trigger.labelIds = [];
    const r = draftToInput(d);
    expect(r.ok).toBe(true);
    if (r.ok) {
      expect(r.input.trigger.label_ids).toBeUndefined();
      expect(r.input.trigger.window_hours).toBeUndefined();
    }
  });

  it("converts all new actions with their inputs", () => {
    const d = okDraft(base);
    d.name = "x";
    d.actions = [
      { ...emptyDraftAction(), type: "remove_label", label_id: "l1" },
      { ...emptyDraftAction(), type: "unassign" },
      { ...emptyDraftAction(), type: "set_estimate", estimate: "pt-1" },
      {
        ...emptyDraftAction(),
        type: "set_due_date",
        dueDateMode: "absolute",
        dueDate: "2026-11-01",
      },
      {
        ...emptyDraftAction(),
        type: "set_due_date",
        dueDateMode: "relative",
        dueDateOffset: "5",
      },
      { ...emptyDraftAction(), type: "move_to_cycle", cycle_id: "c1" },
      { ...emptyDraftAction(), type: "move_to_module", module_id: "m1" },
      { ...emptyDraftAction(), type: "add_watcher", user_id: "u9" },
    ];
    const r = draftToInput(d);
    expect(r.ok).toBe(true);
    if (r.ok) {
      const types = r.input.actions.map((a) => a.type);
      expect(types).toEqual([
        "remove_label",
        "unassign",
        "set_estimate",
        "set_due_date",
        "set_due_date",
        "move_to_cycle",
        "move_to_module",
        "add_watcher",
      ]);
      expect(r.input.actions[0].label_id).toBe("l1");
      expect(r.input.actions[2].estimate).toBe("pt-1");
      expect(r.input.actions[3].due_date).toBe("2026-11-01");
      expect(r.input.actions[4].due_date).toBe("+5d");
      expect(r.input.actions[5].cycle_id).toBe("c1");
      expect(r.input.actions[6].module_id).toBe("m1");
      expect(r.input.actions[7].user_id).toBe("u9");
      // unassign carries no parameters
      expect(r.input.actions[1]).toEqual({ type: "unassign" });
    }
  });

  it("blocks incomplete actions with a message instead of dropping them", () => {
    const d = okDraft(base);
    d.name = "x";
    d.actions = [{ ...emptyDraftAction(), type: "move_to_cycle", cycle_id: "" }];
    const r = draftToInput(d);
    expect(r.ok).toBe(false);
    if (!r.ok) expect(r.error).toMatch(/pick a cycle/);
  });

  it("rejects a bad relative due-date offset", () => {
    const d = okDraft(base);
    d.name = "x";
    d.actions = [
      {
        ...emptyDraftAction(),
        type: "set_due_date",
        dueDateMode: "relative",
        dueDateOffset: "abc",
      },
    ];
    const r = draftToInput(d);
    expect(r.ok).toBe(false);
    if (!r.ok) expect(r.error).toMatch(/offset/);
  });
});

describe("emptyRuleDraft", () => {
  it("starts as issue.state_changed with one add_comment action", () => {
    const d = emptyRuleDraft();
    expect(d.trigger.type).toBe("issue.state_changed");
    expect(d.actions).toHaveLength(1);
  });
});
function webhookDraft(over: Partial<DraftAction> = {}): RuleDraft {
  const d = emptyRuleDraft();
  d.name = "Ping webhook";
  d.actions = [
    {
      ...emptyDraftAction(),
      type: "send_webhook",
      webhookUrl: "https://hooks.example.com/deploy",
      ...over,
    },
  ];
  return d;
}

function webhookRule(over: Partial<AutomationRule["actions"][number]> = {}): AutomationRule {
  return {
    ...base,
    id: "rule-9",
    name: "Ping webhook",
    actions: [
      {
        type: "send_webhook",
        url: "https://hooks.example.com/deploy",
        method: "PUT",
        headers: { Authorization: "********", "X-Sig": "********" },
        payload_template: '{"title": {{issue.title}}}',
        ...over,
      },
    ],
  };
}

describe("send_webhook draft (C77T1)", () => {
  it("converts a full send_webhook draft to the API shape", () => {
    const r = draftToInput(
      webhookDraft({
        webhookMethod: "PUT",
        webhookHeaders: [
          { name: "Authorization", value: "Bearer s3cret" },
          { name: "X-Sig", value: "abc" },
        ],
        webhookPayloadTemplate: '{"title": {{issue.title}}}',
      }),
    );
    expect(r.ok).toBe(true);
    if (!r.ok) return;
    expect(r.input.actions).toEqual([
      {
        type: "send_webhook",
        url: "https://hooks.example.com/deploy",
        method: "PUT",
        headers: { Authorization: "Bearer s3cret", "X-Sig": "abc" },
        payload_template: '{"title": {{issue.title}}}',
      },
    ]);
  });

  it("trims the URL and omits a blank payload template", () => {
    const r = draftToInput(
      webhookDraft({
        webhookUrl: "  https://hooks.example.com/deploy  ",
        webhookPayloadTemplate: "   ",
      }),
    );
    expect(r.ok).toBe(true);
    if (!r.ok) return;
    const a = r.input.actions[0];
    expect(a.url).toBe("https://hooks.example.com/deploy");
    expect(a.method).toBe("POST");
    expect(a.payload_template).toBeUndefined();
    expect(a.headers).toEqual({});
  });

  it("rejects a missing URL", () => {
    const r = draftToInput(webhookDraft({ webhookUrl: "   " }));
    expect(r.ok).toBe(false);
    if (!r.ok) expect(r.error).toMatch(/target URL/);
  });

  it("rejects non-absolute or non-http(s) URLs", () => {
    for (const url of [
      "ftp://hooks.example.com/x",
      "/relative/path",
      "not a url",
      "javascript:alert(1)",
    ]) {
      const r = draftToInput(webhookDraft({ webhookUrl: url }));
      expect(r.ok).toBe(false);
      if (!r.ok) expect(r.error).toMatch(/absolute http\(s\) URL/);
    }
  });

  it("rejects transport-owned header names (case-insensitive)", () => {
    for (const name of ["Host", "host", "Content-Length", "CONTENT-LENGTH"]) {
      const r = draftToInput(
        webhookDraft({ webhookHeaders: [{ name, value: "x" }] }),
      );
      expect(r.ok).toBe(false);
      if (!r.ok) expect(r.error).toMatch(/managed by glance/);
    }
  });

  it("rejects a header row with a value but no name", () => {
    const r = draftToInput(
      webhookDraft({ webhookHeaders: [{ name: "  ", value: "x" }] }),
    );
    expect(r.ok).toBe(false);
    if (!r.ok) expect(r.error).toMatch(/needs a name/);
  });

  it("rejects the masked placeholder as a header value", () => {
    const r = draftToInput(
      webhookDraft({
        webhookHeaders: [{ name: "Authorization", value: "********" }],
      }),
    );
    expect(r.ok).toBe(false);
    if (!r.ok) expect(r.error).toMatch(/placeholder/);
  });

  it("drops name-only header rows instead of round-tripping the mask", () => {
    // The edit flow: names echoed back from the API, values not
    // re-entered. The headers map must come back empty, never with
    // "********" values.
    const r = draftToInput(
      webhookDraft({
        webhookHeaders: [
          { name: "Authorization", value: "" },
          { name: "", value: "" },
        ],
      }),
    );
    expect(r.ok).toBe(true);
    if (!r.ok) return;
    expect(r.input.actions[0].headers).toEqual({});
  });

  it("draftFromRule loads the send_webhook config with header names only", () => {
    const d = draftFromRule(webhookRule());
    const a = d.actions[0];
    expect(a.type).toBe("send_webhook");
    expect(a.webhookUrl).toBe("https://hooks.example.com/deploy");
    expect(a.webhookMethod).toBe("PUT");
    expect(a.webhookPayloadTemplate).toBe('{"title": {{issue.title}}}');
    // Names survive; the masked values are never loaded into the draft.
    expect(a.webhookHeaders).toEqual([
      { name: "Authorization", value: "" },
      { name: "X-Sig", value: "" },
    ]);
  });

  it("draftFromRule normalizes the method defensively", () => {
    const lower = draftFromRule(webhookRule({ method: "put" }));
    expect(lower.actions[0].webhookMethod).toBe("PUT");
    const missing = draftFromRule(webhookRule({ method: undefined }));
    expect(missing.actions[0].webhookMethod).toBe("POST");
  });

  it("edit round-trip never sends the mask back to the API", () => {
    const d = draftFromRule(webhookRule());
    d.name = "r";
    const r = draftToInput(d);
    expect(r.ok).toBe(true);
    if (!r.ok) return;
    const a = r.input.actions[0];
    expect(a.headers).toEqual({});
    expect(JSON.stringify(a)).not.toContain("********");
  });
});
