// Import client helpers (C8T2 GitHub + C9T1 Jira): form validation +
// CSV mapping builder.
import { describe, expect, it } from "vitest";
import { buildCsvMapping, validateGitHubForm, validateJiraForm } from "./import";

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

describe("validateJiraForm", () => {
  it("requires site, email and project key", () => {
    expect(validateJiraForm("", "a@b.c", "PROJ")).toBe(
      "Site is required (your Atlassian subdomain, e.g. “acme”).",
    );
    expect(validateJiraForm("acme", "", "PROJ")).toBe("Email is required.");
    expect(validateJiraForm("acme", "a@b.c", "")).toBe(
      "Project key must start with a letter and contain only letters and digits (e.g. “PROJ”).",
    );
  });

  it("accepts valid inputs and normalizes case", () => {
    expect(validateJiraForm("acme", "a@b.c", "PROJ")).toBeNull();
    expect(validateJiraForm("Acme", "a@b.c", "proj")).toBeNull();
    expect(validateJiraForm("my-site2", "a@b.c", "ABC123")).toBeNull();
  });

  it("rejects full hosts, TLDs and JQL injection attempts", () => {
    expect(validateJiraForm("acme.atlassian.net", "a@b.c", "PROJ")).toBe(
      "Site must be the subdomain only — letters, digits and hyphens (e.g. “acme”, not “acme.atlassian.net”).",
    );
    expect(validateJiraForm("evil.com", "a@b.c", "PROJ")).toBe(
      "Site must be the subdomain only — letters, digits and hyphens (e.g. “acme”, not “acme.atlassian.net”).",
    );
    expect(validateJiraForm("acme", "a@b.c", 'PROJ" OR 1=1 --')).toBe(
      "Project key must start with a letter and contain only letters and digits (e.g. “PROJ”).",
    );
    expect(validateJiraForm("acme", "a@b.c", "1PROJ")).toBe(
      "Project key must start with a letter and contain only letters and digits (e.g. “PROJ”).",
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
