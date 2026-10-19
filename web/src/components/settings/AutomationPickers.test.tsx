// @vitest-environment jsdom
// Component tests for the C16T4 automation pickers: trigger filter
// fields and action parameter fields render per trigger/action type.

import { fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import {
  ActionFields,
  ActionTypeSelect,
  TriggerFields,
} from "./AutomationPickers";
import {
  emptyDraftAction,
  emptyTriggerDraft,
  type DraftAction,
  type TriggerDraft,
} from "../../lib/automationDraft";

const lookups = {
  states: [{ id: "s1", name: "Backlog" }],
  labels: [
    { id: "l1", name: "bug" },
    { id: "l2", name: "hotfix" },
  ],
};

const actionLookups = {
  members: [
    { id: "u1", name: "Ada", email: "ada@example.com", role: 15 },
    { id: "u2", name: "Guest", email: "guest@example.com", role: 5 },
  ],
  labels: lookups.labels,
  states: lookups.states,
  cycles: [{ id: "c1", name: "Cycle 1" }],
  modules: [{ id: "m1", name: "Module A" }],
  estimateScales: [
    {
      id: "sc1",
      name: "T-shirt",
      points: [
        { id: "p1", key: "S" },
        { id: "p2", key: "M" },
      ],
    },
  ],
};

function renderTrigger(trigger: TriggerDraft) {
  const onChange = vi.fn();
  render(
    <TriggerFields trigger={trigger} onChange={onChange} lookups={lookups} />,
  );
  return onChange;
}

function renderAction(action: DraftAction, index = 0) {
  const onChange = vi.fn();
  render(
    <ActionFields
      index={index}
      action={action}
      onChange={onChange}
      lookups={actionLookups}
    />,
  );
  return onChange;
}

describe("TriggerFields", () => {
  afterEach(() => document.body.innerHTML = "");

  it("lists all event and scheduled triggers, grouped", () => {
    renderTrigger(emptyTriggerDraft());
    const select = screen.getByLabelText("When") as HTMLSelectElement;
    const options = Array.from(select.options).map((o) => o.value);
    for (const t of [
      "issue.state_changed",
      "issue.created",
      "issue.assigned",
      "issue.unassigned",
      "issue.labels_changed",
      "issue.priority_changed",
      "issue.due_date_changed",
      "issue.estimate_changed",
      "issue.comment_added",
      "issue.due_soon",
      "issue.overdue",
      "issue.stale",
      "cycle.ending_soon",
    ]) {
      expect(options).toContain(t);
    }
  });

  it("shows the label multi-select for labels_changed", () => {
    renderTrigger({ ...emptyTriggerDraft(), type: "issue.labels_changed" });
    expect(screen.getByLabelText(/Only for these labels/)).toBeTruthy();
    expect(screen.queryByLabelText("Due within (hours)")).toBeNull();
  });

  it("shows priority from/to selects for priority_changed", () => {
    renderTrigger({ ...emptyTriggerDraft(), type: "issue.priority_changed" });
    expect(screen.getByLabelText("From priority")).toBeTruthy();
    expect(screen.getByLabelText("To priority")).toBeTruthy();
  });

  it("shows the window input and schedule copy for due_soon", () => {
    renderTrigger({ ...emptyTriggerDraft(), type: "issue.due_soon" });
    expect(screen.getByLabelText("Due within (hours)")).toBeTruthy();
    expect(screen.getByText(/checked by a ticker/)).toBeTruthy();
  });

  it("shows the days input for stale", () => {
    renderTrigger({ ...emptyTriggerDraft(), type: "issue.stale" });
    expect(screen.getByLabelText("Untouched for (days)")).toBeTruthy();
  });

  it("shows no filter inputs for filterless triggers", () => {
    renderTrigger({ ...emptyTriggerDraft(), type: "issue.comment_added" });
    expect(screen.queryByLabelText(/Only for these labels/)).toBeNull();
    expect(screen.queryByLabelText("From priority")).toBeNull();
  });

  it("propagates the chosen trigger type", () => {
    const onChange = renderTrigger(emptyTriggerDraft());
    fireEvent.change(screen.getByLabelText("When"), {
      target: { value: "issue.overdue" },
    });
    expect(onChange).toHaveBeenCalledWith({ type: "issue.overdue" });
  });
});

describe("ActionFields", () => {
  afterEach(() => (document.body.innerHTML = ""));

  it("lists all actions in the type select", () => {
    render(
      <ActionTypeSelect
        index={0}
        value="add_comment"
        onChange={() => {}}
      />,
    );
    const options = Array.from(
      (screen.getByLabelText("Action 1 type") as HTMLSelectElement).options,
    ).map((o) => o.value);
    for (const t of [
      "assign",
      "add_label",
      "add_comment",
      "set_priority",
      "set_state",
      "remove_label",
      "unassign",
      "set_estimate",
      "set_due_date",
      "move_to_cycle",
      "move_to_module",
      "add_watcher",
    ]) {
      expect(options).toContain(t);
    }
    document.body.innerHTML = "";
  });

  it("renders a cycle select for move_to_cycle", () => {
    renderAction({ ...emptyDraftAction(), type: "move_to_cycle" });
    expect(screen.getByLabelText("Action 1 cycle")).toBeTruthy();
    expect(
      screen.getByRole("option", { name: "Cycle 1" }),
    ).toBeTruthy();
  });

  it("renders a module select for move_to_module", () => {
    renderAction({ ...emptyDraftAction(), type: "move_to_module" });
    expect(screen.getByLabelText("Action 1 module")).toBeTruthy();
  });

  it("renders estimate points grouped by scale", () => {
    renderAction({ ...emptyDraftAction(), type: "set_estimate" });
    expect(screen.getByRole("option", { name: "S" })).toBeTruthy();
    expect(screen.getByRole("option", { name: "M" })).toBeTruthy();
  });

  it("toggles due-date absolute/relative inputs", () => {
    const baseAction = { ...emptyDraftAction(), type: "set_due_date" as const };
    const { rerender } = render(
      <ActionFields
        index={0}
        action={baseAction}
        onChange={() => {}}
        lookups={actionLookups}
      />,
    );
    expect(screen.getByLabelText("Action 1 due date")).toBeTruthy();
    expect(screen.queryByLabelText("Action 1 due date offset")).toBeNull();
    rerender(
      <ActionFields
        index={0}
        action={{ ...baseAction, dueDateMode: "relative" }}
        onChange={() => {}}
        lookups={actionLookups}
      />,
    );
    expect(screen.getByLabelText("Action 1 due date offset")).toBeTruthy();
    expect(screen.getByText(/days after the rule fires/)).toBeTruthy();
  });

  it("renders a watcher select that excludes guests", () => {
    const onChange = renderAction({ ...emptyDraftAction(), type: "add_watcher" });
    const options = Array.from(
      (screen.getByLabelText("Action 1 watcher") as HTMLSelectElement).options,
    ).map((o) => o.text);
    expect(options).toContain("Ada");
    expect(options).not.toContain("Guest");
    expect(onChange).not.toHaveBeenCalled();
  });

  it("unassign needs no further input", () => {
    renderAction({ ...emptyDraftAction(), type: "unassign" });
    expect(screen.getByText(/Clears every assignee/)).toBeTruthy();
    expect(screen.queryByLabelText("Action 1 user")).toBeNull();
  });
});

describe("ActionFields send_webhook (C77T1)", () => {
  afterEach(() => document.body.innerHTML = "");

  function renderWebhook(over: Partial<DraftAction> = {}) {
    return renderAction({
      ...emptyDraftAction(),
      type: "send_webhook",
      webhookUrl: "https://hooks.example.com/deploy",
      ...over,
    });
  }

  it("offers a Send webhook action type in the picker", () => {
    render(
      <ActionTypeSelect index={0} value="add_comment" onChange={() => {}} />,
    );
    const select = screen.getByLabelText(
      "Action 1 type",
    ) as HTMLSelectElement;
    const byValue = new Map(
      Array.from(select.options).map((o) => [o.value, o.text]),
    );
    expect(byValue.get("send_webhook")).toBe("Send webhook");
  });

  it("renders the URL, method, headers and template editors", () => {
    renderWebhook();
    expect(screen.getByLabelText("URL")).toHaveValue(
      "https://hooks.example.com/deploy",
    );
    expect(screen.getByLabelText("Method")).toHaveValue("POST");
    expect(screen.getByLabelText(/Payload template/)).toBeTruthy();
    expect(
      screen.getByRole("button", { name: /Add header/ }),
    ).toBeTruthy();
    expect(
      screen.getByText(/Absolute URL with an http or https scheme/),
    ).toBeTruthy();
  });

  it("masks header values like passwords", () => {
    renderWebhook({
      webhookHeaders: [{ name: "Authorization", value: "Bearer s3cret" }],
    });
    const value = screen.getByLabelText(
      "Action 1 header 1 value",
    ) as HTMLInputElement;
    expect(value.type).toBe("password");
    expect(
      (screen.getByLabelText("Action 1 header 1 name") as HTMLInputElement)
        .type,
    ).toBe("text");
  });

  it("shows header names echoed back from the API with empty values", () => {
    renderWebhook({
      webhookHeaders: [{ name: "Authorization", value: "" }],
    });
    expect(screen.getByLabelText("Action 1 header 1 name")).toHaveValue(
      "Authorization",
    );
    expect(screen.getByLabelText("Action 1 header 1 value")).toHaveValue("");
    expect(
      screen.getByText(/values are write-only/),
    ).toBeTruthy();
  });

  it("adding a header row reports the new row via onChange", () => {
    const onChange = renderWebhook({ webhookHeaders: [] });
    fireEvent.click(screen.getByRole("button", { name: /Add header/ }));
    expect(onChange).toHaveBeenCalledWith({
      webhookHeaders: [{ name: "", value: "" }],
    });
  });

  it("editing a header row reports the patch", () => {
    const onChange = renderWebhook({
      webhookHeaders: [{ name: "Authorization", value: "" }],
    });
    fireEvent.change(screen.getByLabelText("Action 1 header 1 value"), {
      target: { value: "Bearer s3cret" },
    });
    expect(onChange).toHaveBeenCalledWith({
      webhookHeaders: [{ name: "Authorization", value: "Bearer s3cret" }],
    });
    fireEvent.click(
      screen.getByRole("button", { name: /remove header 1/i }),
    );
    expect(onChange).toHaveBeenCalledWith({ webhookHeaders: [] });
  });

  it("lists the template variables with the unquoted-placeholder note", () => {
    renderWebhook();
    // Collapsed by default.
    expect(screen.queryByText("{{issue.title}}")).toBeNull();
    fireEvent.click(
      screen.getByRole("button", { name: /Show template variables/ }),
    );
    for (const v of [
      "{{issue.id}}",
      "{{issue.identifier}}",
      "{{issue.title}}",
      "{{issue.state}}",
      "{{issue.priority}}",
      "{{issue.labels}}",
      "{{issue.assignees}}",
      "{{rule.id}}",
      "{{trigger}}",
      "{{fired_at}}",
    ]) {
      expect(screen.getByText(v)).toBeTruthy();
    }
    expect(screen.getByText(/don't put quotes around them/)).toBeTruthy();
    // Default-envelope preview for a blank template.
    expect(screen.getByText(/"automation\.webhook"/)).toBeTruthy();
    fireEvent.click(
      screen.getByRole("button", { name: /Hide template variables/ }),
    );
    expect(screen.queryByText("{{issue.title}}")).toBeNull();
  });

  it("reports URL, method and template edits via onChange", () => {
    const onChange = renderWebhook();
    fireEvent.change(screen.getByLabelText("URL"), {
      target: { value: "https://other.example/hook" },
    });
    expect(onChange).toHaveBeenCalledWith({
      webhookUrl: "https://other.example/hook",
    });
    fireEvent.change(screen.getByLabelText("Method"), {
      target: { value: "PUT" },
    });
    expect(onChange).toHaveBeenCalledWith({ webhookMethod: "PUT" });
    fireEvent.change(screen.getByLabelText(/Payload template/), {
      target: { value: '{"title": {{issue.title}}}' },
    });
    expect(onChange).toHaveBeenCalledWith({
      webhookPayloadTemplate: '{"title": {{issue.title}}}',
    });
  });
});
