import { describe, expect, it } from "vitest";
import {
  emptyProjectTemplatePayload,
  summarizeProjectTemplatePayload,
  type ProjectTemplatePayload,
} from "./projectTemplates";

describe("emptyProjectTemplatePayload", () => {
  it("returns empty sections and no estimate scale", () => {
    expect(emptyProjectTemplatePayload()).toEqual({
      states: [],
      labels: [],
      modules: [],
    });
  });
});

describe("summarizeProjectTemplatePayload", () => {
  it("summarizes each seeded section", () => {
    const p: ProjectTemplatePayload = {
      states: [
        { name: "Icebox", group: "backlog", color: "#6b7280" },
        { name: "Doing", group: "started", color: "#f59e0b" },
      ],
      labels: [{ name: "bug", color: "#ef4444" }],
      modules: [{ name: "API", description: "" }],
      estimate_scale: {
        name: "Fibonacci",
        points: [{ key: "1", value: 1, description: "" }],
      },
    };
    expect(summarizeProjectTemplatePayload(p)).toBe(
      "2 states · 1 labels · 1 modules · estimate: Fibonacci",
    );
  });

  it("reports defaults-only when nothing is seeded", () => {
    expect(summarizeProjectTemplatePayload(emptyProjectTemplatePayload())).toBe(
      "Defaults only",
    );
  });

  it("handles null/undefined gracefully", () => {
    expect(summarizeProjectTemplatePayload(null)).toBe("No seeds");
    expect(summarizeProjectTemplatePayload(undefined)).toBe("No seeds");
  });

  it("omits the estimate scale when unnamed", () => {
    const p: ProjectTemplatePayload = {
      states: [],
      labels: [],
      modules: [],
      estimate_scale: { name: "", points: [] },
    };
    expect(summarizeProjectTemplatePayload(p)).toBe("Defaults only");
  });
});
