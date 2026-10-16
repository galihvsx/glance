// Import client helpers (C8T2 GitHub + C9T1 Jira + C10T0 Trello + C31T1
// Linear): form validation + CSV mapping builder.
import { afterEach, describe, expect, it, vi } from "vitest";
import { ApiError } from "./api";
import {
  buildCsvMapping,
  buildPlaneResolutions,
  fetchLinearTeams,
  linearErrMsg,
  linearRetryAfterSeconds,
  previewLinearImport,
  runLinearImport,
  validateGitHubForm,
  validateJiraForm,
  validateLinearForm,
  validatePlaneFile,
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

describe("validatePlaneFile", () => {
  it("requires a file", () => {
    expect(validatePlaneFile(null)).toBe(
      "Choose a Plane export file (.json or .zip).",
    );
  });

  it("accepts .json and .zip", () => {
    expect(validatePlaneFile({ name: "export.json" } as File)).toBeNull();
    expect(validatePlaneFile({ name: "export.ZIP" } as File)).toBeNull();
  });

  it("rejects other extensions", () => {
    expect(validatePlaneFile({ name: "export.csv" } as File)).toBe(
      "Plane exports are .json or .zip files.",
    );
    expect(validatePlaneFile({ name: "export" } as File)).toBe(
      "Plane exports are .json or .zip files.",
    );
  });
});

describe("buildPlaneResolutions", () => {
  it("builds minimal resolutions from mapping choices", () => {
    expect(
      buildPlaneResolutions({
        people: { "Ada Lovelace": "m1", "Grace Hopper": null },
        states: {
          "In Progress": { kind: "existing", id: "s1" },
          Backlog: { kind: "create" },
          Todo: { kind: "default" },
        },
        labels: {
          bug: { kind: "existing", id: "l1" },
          shiny: { kind: "create" },
          stale: { kind: "default" },
        },
        cycles: {
          "Sprint 1": { kind: "default" },
          "Sprint 2": { kind: "create" },
        },
        modules: {
          Auth: { kind: "existing", id: "mod1" },
        },
      }),
    ).toEqual({
      people: { "Ada Lovelace": { member_id: "m1" } },
      states: {
        "In Progress": { state_id: "s1" },
        Backlog: { group: "backlog" },
      },
      labels: { bug: { label_id: "l1" }, shiny: {} },
      cycles: { "Sprint 2": {} },
      modules: { Auth: { module_id: "mod1" } },
    });
  });

  it("returns empty maps when everything is default or unmapped", () => {
    expect(
      buildPlaneResolutions({
        people: { "Ada Lovelace": null },
        states: { Todo: { kind: "default" } },
        labels: {},
        cycles: {},
        modules: {},
      }),
    ).toEqual({
      people: {},
      states: {},
      labels: {},
      cycles: {},
      modules: {},
    });
  });
});

describe("validateLinearForm", () => {
  it("requires the API key and a team", () => {
    expect(validateLinearForm("", "team-1")).toBe("API key is required.");
    expect(validateLinearForm("  ", "team-1")).toBe("API key is required.");
    expect(validateLinearForm("lin_key", "")).toBe("Choose a team.");
    expect(validateLinearForm("lin_key", "  ")).toBe("Choose a team.");
  });

  it("accepts a key and a team id", () => {
    expect(validateLinearForm("lin_key", "team-1")).toBeNull();
  });
});

describe("linearRetryAfterSeconds", () => {
  it("extracts retry_after from a 429 ApiError's details", () => {
    const err = new ApiError(429, "linear rate limit exceeded: slow down", "rate_limited", {
      retry_after: 60,
    });
    expect(linearRetryAfterSeconds(err)).toBe(60);
  });

  it("returns null when there is no usable retry_after", () => {
    expect(linearRetryAfterSeconds(new ApiError(502, "linear authentication failed"))).toBeNull();
    expect(linearRetryAfterSeconds(new ApiError(429, "slow down", undefined, {}))).toBeNull();
    expect(
      linearRetryAfterSeconds(new ApiError(429, "slow down", undefined, { retry_after: "soon" })),
    ).toBeNull();
    expect(linearRetryAfterSeconds(new Error("boom"))).toBeNull();
    expect(linearRetryAfterSeconds(null)).toBeNull();
  });
});

describe("linearErrMsg", () => {
  it("maps the generic 502 auth failure to the friendly message", () => {
    expect(
      linearErrMsg(new ApiError(502, "linear authentication failed", "bad_gateway"), "Preview failed"),
    ).toBe("Linear authentication failed — check your API key");
  });

  it("surfaces retry-after on 429", () => {
    const err = new ApiError(429, "linear rate limit exceeded: slow down", "rate_limited", {
      retry_after: 60,
    });
    expect(linearErrMsg(err, "Preview failed")).toBe(
      "Linear rate limit exceeded — try again in 60 seconds.",
    );
    expect(
      linearErrMsg(new ApiError(429, "linear rate limit exceeded"), "Preview failed"),
    ).toBe("Linear rate limit exceeded — please wait a bit and try again.");
  });

  it("passes other ApiError messages through and falls back otherwise", () => {
    expect(linearErrMsg(new ApiError(400, "team not found"), "Preview failed")).toBe("team not found");
    expect(linearErrMsg(new Error("boom"), "Preview failed")).toBe("Preview failed");
  });
});

describe("Linear helpers (C31T1)", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  function stubFetch(payload: unknown) {
    const fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      text: async () => JSON.stringify(payload),
    });
    vi.stubGlobal("fetch", fetchMock);
    return fetchMock;
  }

  it("fetchLinearTeams posts the key to the teams endpoint", async () => {
    const fetchMock = stubFetch({ teams: [{ id: "t1", key: "ENG", name: "Engineering" }] });
    const res = await fetchLinearTeams("acme", "ENG", "lin_key");
    expect(res).toEqual({ teams: [{ id: "t1", key: "ENG", name: "Engineering" }] });
    expect(fetchMock).toHaveBeenCalledTimes(1);
    const [path, init] = fetchMock.mock.calls[0] as [string, { body: string }];
    expect(path).toBe("/api/v1/workspaces/acme/projects/ENG/imports/linear/teams");
    expect(JSON.parse(init.body)).toEqual({ api_key: "lin_key" });
  });

  it("previewLinearImport posts key + team + max to the preview endpoint", async () => {
    const fetchMock = stubFetch({ issues: [], total: 0, assignee_misses: 0 });
    const res = await previewLinearImport("acme", "ENG", {
      api_key: "lin_key",
      team_id: "t1",
      max: 50,
    });
    expect(res).toEqual({ issues: [], total: 0, assignee_misses: 0 });
    const [path, init] = fetchMock.mock.calls[0] as [string, { body: string }];
    expect(path).toBe("/api/v1/workspaces/acme/projects/ENG/imports/linear/preview");
    expect(JSON.parse(init.body)).toEqual({ api_key: "lin_key", team_id: "t1", max: 50 });
  });

  it("runLinearImport posts key + team + max to the import endpoint", async () => {
    const fetchMock = stubFetch({
      created: 3,
      skipped: 1,
      failed: 0,
      errors: [],
      assignee_misses: 1,
    });
    const res = await runLinearImport("acme", "ENG", {
      api_key: "lin_key",
      team_id: "t1",
      max: 50,
    });
    expect(res.created).toBe(3);
    const [path, init] = fetchMock.mock.calls[0] as [string, { body: string }];
    expect(path).toBe("/api/v1/workspaces/acme/projects/ENG/imports/linear");
    expect(JSON.parse(init.body)).toEqual({ api_key: "lin_key", team_id: "t1", max: 50 });
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
