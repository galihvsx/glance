// Import client helpers (C8T2): form validation + CSV mapping builder.
import { describe, expect, it } from "vitest";
import { buildCsvMapping, validateGitHubForm } from "./import";

describe("validateGitHubForm", () => {
  it("requires owner and repo", () => {
    expect(validateGitHubForm("", "widgets")).toBe("Owner and repo are required.");
    expect(validateGitHubForm("acme", "  ")).toBe("Owner and repo are required.");
  });

  it("accepts valid owner/repo pairs", () => {
    expect(validateGitHubForm("acme", "widgets")).toBeNull();
    expect(validateGitHubForm("my-org_2", "repo.js")).toBeNull();
  });

  it("rejects path-unsafe characters", () => {
    expect(validateGitHubForm("ac/me", "widgets")).toBe(
      "Owner/repo may only contain letters, digits, dot, dash and underscore.",
    );
    expect(validateGitHubForm("acme", "wid gets")).toBe(
      "Owner/repo may only contain letters, digits, dot, dash and underscore.",
    );
  });
});

describe("buildCsvMapping", () => {
  it("drops empty optional fields", () => {
    expect(
      buildCsvMapping({ title: "Subject", description: "", priority: "high" }),
    ).toEqual({ title: "Subject", priority: "high" });
  });

  it("trims values", () => {
    expect(buildCsvMapping({ title: "  Subject  ", labels: " bug " })).toEqual({
      title: "Subject",
      labels: "bug",
    });
  });
});
