// Import client helpers (C8T2 GitHub + C9T1 Jira + C10T0 Trello): form
// validation + CSV mapping builder.
import { describe, expect, it } from "vitest";
import {
  buildCsvMapping,
  validateGitHubForm,
  validateJiraForm,
  validateTrelloForm,
} from "./import";

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

describe("validateTrelloForm", () => {
  it("requires the API key and the board", () => {
    expect(validateTrelloForm("", "abc12345")).toBe("API key is required.");
    expect(validateTrelloForm("key", "")).toBe("Board is required.");
    expect(validateTrelloForm("key", "  ")).toBe("Board is required.");
  });

  it("accepts the 24-char board id, the 8-char short link, and https board URLs", () => {
    expect(validateTrelloForm("key", "5abbe4b7ddc1b351ef961414")).toBeNull();
    expect(validateTrelloForm("key", "AbC123xY")).toBeNull();
    expect(validateTrelloForm("key", "https://trello.com/b/AbC123xY/roadmap")).toBeNull();
    expect(validateTrelloForm("key", "https://www.trello.com/b/AbC123xY")).toBeNull();
  });

  it("rejects malformed board references", () => {
    const msg =
      "Board must be the 24-char board id, the 8-char short link, or an https trello.com board URL.";
    expect(validateTrelloForm("key", "abc123")).toBe(msg); // 6 chars
    expect(validateTrelloForm("key", "abc 1234")).toBe(msg); // space
    expect(validateTrelloForm("key", "5abbe4b7ddc1b351ef96141g")).toBe(msg); // non-hex 24-char
    expect(validateTrelloForm("key", "http://trello.com/b/AbC123xY/x")).toBe(msg); // plain http
    expect(validateTrelloForm("key", "https://evil.com/b/AbC123xY/x")).toBe(msg); // wrong host
    expect(validateTrelloForm("key", "https://trello.com/c/AbC123xY/x")).toBe(msg); // card URL
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
