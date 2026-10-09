// vitest: template -> create-form prefill logic (pure functions in
// lib/templates.ts). No DOM, no network.

import { describe, expect, it } from "vitest";
import {
  applyTemplateToForm,
  defaultsToTemplateData,
  emptyDefaults,
  templateDataToDefaults,
  type CreateFormValues,
  type IssueTemplate,
} from "./templates";

function makeTemplate(templateData: unknown, name = "Bug report"): IssueTemplate {
  return {
    id: "tmpl-1",
    project_id: "proj-1",
    name,
    description: "",
    template_data: (templateData ?? {}) as IssueTemplate["template_data"],
    created_at: "2026-10-09T00:00:00Z",
    updated_at: "2026-10-09T00:00:00Z",
  };
}

const blankForm: CreateFormValues = {
  name: "",
  description: "",
  priority: 0,
  stateId: "",
  estimatePointId: "",
  labelIds: [],
};

const tiptapDoc = {
  type: "doc",
  content: [
    {
      type: "paragraph",
      content: [{ type: "text", text: "Steps to reproduce:" }],
    },
    {
      type: "paragraph",
      content: [{ type: "text", text: "1. Click the button" }],
    },
  ],
};

describe("applyTemplateToForm", () => {
  it("prefills every defined default, overwriting current form values", () => {
    const t = makeTemplate({
      name: "Login is broken",
      description: tiptapDoc,
      priority: 3,
      state_id: "state-triage",
      estimate_point_id: "point-5",
      label_ids: ["label-a", "label-b"],
    });
    const out = applyTemplateToForm(t, {
      ...blankForm,
      name: "user typed",
      priority: 1,
    });
    expect(out).toEqual({
      name: "Login is broken",
      description: "Steps to reproduce:\n1. Click the button",
      priority: 3,
      stateId: "state-triage",
      estimatePointId: "point-5",
      labelIds: ["label-a", "label-b"],
    });
  });

  it("leaves fields the template does not define untouched", () => {
    const t = makeTemplate({ priority: 4 });
    const out = applyTemplateToForm(t, {
      ...blankForm,
      name: "keep me",
      description: "keep desc",
      stateId: "state-keep",
    });
    expect(out.name).toBe("keep me");
    expect(out.description).toBe("keep desc");
    expect(out.priority).toBe(4);
    expect(out.stateId).toBe("state-keep");
  });

  it("an empty template_data leaves the form unchanged", () => {
    const t = makeTemplate({});
    const form = { ...blankForm, name: "n", priority: 2 };
    expect(applyTemplateToForm(t, form)).toEqual(form);
  });

  it("tolerates a null template_data document", () => {
    const t = makeTemplate(null);
    const form = { ...blankForm, name: "n" };
    expect(applyTemplateToForm(t, form)).toEqual(form);
  });

  it("a blank-string template name does not clobber the typed title", () => {
    const t = makeTemplate({ name: "   " });
    const out = applyTemplateToForm(t, { ...blankForm, name: "typed" });
    expect(out.name).toBe("typed");
  });

  it("explicit priority 0 overwrites a higher form priority", () => {
    const t = makeTemplate({ priority: 0 });
    const out = applyTemplateToForm(t, { ...blankForm, priority: 3 });
    expect(out.priority).toBe(0);
  });

  it("does not mutate the input form", () => {
    const t = makeTemplate({ name: "X", label_ids: ["l1"] });
    const form = { ...blankForm, labelIds: ["orig"] };
    applyTemplateToForm(t, form);
    expect(form).toEqual({ ...blankForm, labelIds: ["orig"] });
  });
});

describe("defaultsToTemplateData", () => {
  it("omits every unset default", () => {
    expect(defaultsToTemplateData(emptyDefaults())).toEqual({});
  });

  it("serializes set fields, trimming and dropping empties", () => {
    const data = defaultsToTemplateData({
      issueName: "  Crash on launch  ",
      issueDescription: "Repro steps",
      priority: 2,
      stateId: "s1",
      estimatePointId: "",
      labelIds: ["a", "b"],
    });
    expect(data.name).toBe("Crash on launch");
    expect(data.priority).toBe(2);
    expect(data.state_id).toBe("s1");
    expect(data.label_ids).toEqual(["a", "b"]);
    expect(data).not.toHaveProperty("estimate_point_id");
    expect(data.description).toMatchObject({ type: "doc" });
  });

  it("keeps explicit priority 0 as a real default", () => {
    const data = defaultsToTemplateData({ ...emptyDefaults(), priority: 0 });
    expect(data.priority).toBe(0);
  });
});

describe("templateDataToDefaults", () => {
  it("round-trips through defaultsToTemplateData", () => {
    const original = {
      ...emptyDefaults(),
      issueName: "Bug",
      issueDescription: "line one\nline two",
      priority: 3,
      stateId: "s9",
      estimatePointId: "p2",
      labelIds: ["l1"],
    };
    const back = templateDataToDefaults(defaultsToTemplateData(original));
    expect(back).toEqual(original);
  });

  it("drops out-of-range priorities and non-string ids", () => {
    const d = templateDataToDefaults({
      priority: 9,
      state_id: 42 as unknown as string,
      label_ids: ["ok", 7 as unknown as string, ""],
    });
    expect(d.priority).toBeNull();
    expect(d.stateId).toBeNull();
    expect(d.labelIds).toEqual(["ok"]);
  });

  it("handles null and non-object documents", () => {
    expect(templateDataToDefaults(null)).toEqual(emptyDefaults());
    expect(templateDataToDefaults(undefined)).toEqual(emptyDefaults());
  });
});
