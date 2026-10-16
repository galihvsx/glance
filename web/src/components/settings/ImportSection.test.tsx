// @vitest-environment jsdom
// Component tests for the Linear importer card (C31T1): render, the
// team-picker flow with mocked lib helpers, preview render, import
// result + key clearing, and honest 502/429 error copy.

import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi, type Mock } from "vitest";
import ImportSection from "./ImportSection";
import { ApiError } from "../../lib/api";
import {
  fetchLinearTeams,
  previewLinearImport,
  runGitHubImport,
  runLinearImport,
} from "../../lib/import";

vi.mock("../../lib/import", async (importOriginal) => {
  const mod = await importOriginal<typeof import("../../lib/import")>();
  return {
    ...mod,
    fetchLinearTeams: vi.fn(),
    previewLinearImport: vi.fn(),
    runLinearImport: vi.fn(),
    previewGitHubImport: vi.fn(),
    runGitHubImport: vi.fn(),
  };
});

const teamsMock = fetchLinearTeams as unknown as Mock;
const previewMock = previewLinearImport as unknown as Mock;
const runMock = runLinearImport as unknown as Mock;
const ghRunMock = runGitHubImport as unknown as Mock;

const TEAMS = [
  { id: "team-1", key: "ENG", name: "Engineering" },
  { id: "team-2", key: "DES", name: "Design" },
];

const PREVIEW = {
  issues: [
    {
      identifier: "ENG-1",
      title: "Fix login",
      state: "In Progress",
      priority: 2,
      labels: ["bug"],
      assignee_email: "ada@acme.test",
      assignee_matched: true,
      due_date: "2026-10-20",
      url: "https://linear.app/acme/issue/ENG-1/fix-login",
    },
    {
      identifier: "ENG-2",
      title: "Polish empty states",
      state: "Backlog",
      priority: 4,
      labels: [],
      assignee_email: "ghost@acme.test",
      assignee_matched: false,
      due_date: null,
      url: "https://linear.app/acme/issue/ENG-2/polish-empty-states",
    },
  ],
  total: 25,
  assignee_misses: 1,
};

function renderLinear() {
  render(<ImportSection slug="acme" identifier="ENG" canEdit />);
  fireEvent.click(screen.getByRole("button", { name: /Linear/ }));
}

async function pickTeam() {
  teamsMock.mockResolvedValue({ teams: TEAMS });
  fireEvent.change(screen.getByLabelText(/API key/), { target: { value: "lin_key" } });
  fireEvent.click(screen.getByRole("button", { name: /Load teams/ }));
  await waitFor(() => expect(teamsMock).toHaveBeenCalledWith("acme", "ENG", "lin_key"));
  const select = screen.getByLabelText(/Team/) as HTMLSelectElement;
  fireEvent.change(select, { target: { value: "team-1" } });
  return select;
}

beforeEach(() => {
  vi.spyOn(window, "confirm").mockReturnValue(true);
});

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
  vi.restoreAllMocks();
});

describe("Linear importer card", () => {
  it("renders the API key field and the Load teams button", () => {
    renderLinear();
    expect(screen.getByLabelText(/API key/)).toBeTruthy();
    expect(screen.getByRole("button", { name: /Load teams/ })).toBeTruthy();
    expect(screen.getByText(/Sent to api\.linear\.app only, never stored/)).toBeTruthy();
  });

  it("loads teams on demand and offers them in the picker", async () => {
    renderLinear();
    await pickTeam();
    const select = screen.getByLabelText(/Team/) as HTMLSelectElement;
    const options = Array.from(select.options).map((o) => o.text);
    expect(options).toContain("ENG — Engineering");
    expect(options).toContain("DES — Design");
  });

  it("shows the team-load error without crashing", async () => {
    renderLinear();
    teamsMock.mockRejectedValue(new ApiError(502, "linear authentication failed"));
    fireEvent.change(screen.getByLabelText(/API key/), { target: { value: "bad_key" } });
    fireEvent.click(screen.getByRole("button", { name: /Load teams/ }));
    await waitFor(() =>
      expect(
        screen.getByText("Linear authentication failed — check your API key"),
      ).toBeTruthy(),
    );
  });

  it("renders the preview table with a first-10 note when total > shown", async () => {
    renderLinear();
    await pickTeam();
    previewMock.mockResolvedValue(PREVIEW);
    fireEvent.click(screen.getByRole("button", { name: /^Preview$/ }));
    await waitFor(() => expect(previewMock).toHaveBeenCalled());
    expect(screen.getByText("ENG-1")).toBeTruthy();
    expect(screen.getByText("Fix login")).toBeTruthy();
    expect(screen.getByText("ENG-2")).toBeTruthy();
    expect(screen.getByText("matched a member")).toBeTruthy();
    expect(screen.getByText("no member match")).toBeTruthy();
    expect(screen.getByText("2026-10-20")).toBeTruthy();
    expect(screen.getByText("Showing the first 2 of 25 issues.")).toBeTruthy();
  });

  it("shows the empty-team state instead of a table", async () => {
    renderLinear();
    await pickTeam();
    previewMock.mockResolvedValue({ issues: [], total: 0, assignee_misses: 0 });
    fireEvent.click(screen.getByRole("button", { name: /^Preview$/ }));
    await waitFor(() => expect(previewMock).toHaveBeenCalled());
    expect(screen.getByText("No issues in this team")).toBeTruthy();
  });

  it("maps a 502 bad-key preview failure to the friendly message", async () => {
    renderLinear();
    await pickTeam();
    previewMock.mockRejectedValue(new ApiError(502, "linear authentication failed"));
    fireEvent.click(screen.getByRole("button", { name: /^Preview$/ }));
    await waitFor(() =>
      expect(
        screen.getByText("Linear authentication failed — check your API key"),
      ).toBeTruthy(),
    );
  });

  it("surfaces retry-after on a 429 preview failure", async () => {
    renderLinear();
    await pickTeam();
    previewMock.mockRejectedValue(
      new ApiError(429, "linear rate limit exceeded: slow down", "rate_limited", {
        retry_after: 60,
      }),
    );
    fireEvent.click(screen.getByRole("button", { name: /^Preview$/ }));
    await waitFor(() =>
      expect(
        screen.getByText("Linear rate limit exceeded — try again in 60 seconds."),
      ).toBeTruthy(),
    );
  });

  it("shows the import result and clears the API key after import", async () => {
    renderLinear();
    await pickTeam();
    runMock.mockResolvedValue({
      created: 2,
      skipped: 1,
      failed: 1,
      errors: [{ identifier: "ENG-3", message: "title is required" }],
      assignee_misses: 1,
    });
    fireEvent.click(screen.getByRole("button", { name: /^Import$/ }));
    await waitFor(() => expect(runMock).toHaveBeenCalled());
    expect(screen.getByText(/Import result/)).toBeTruthy();
    // Stats: Created 2, Skipped 1, Failed 1 — the result card renders them.
    expect(screen.getByText("ENG-3: title is required")).toBeTruthy();
    // The key never lingers in the form after a run.
    expect((screen.getByLabelText(/API key/) as HTMLInputElement).value).toBe("");
  });
});

describe("GitHub importer result (C38T1 follow-up)", () => {
  it("shows the Releases created stat next to Milestones skipped", async () => {
    // GitHub is the default tab — no tab click needed.
    render(<ImportSection slug="acme" identifier="ENG" canEdit />);
    fireEvent.change(screen.getByLabelText(/Owner/), {
      target: { value: "octocat" },
    });
    fireEvent.change(screen.getByLabelText(/Repo/), {
      target: { value: "hello-world" },
    });
    ghRunMock.mockResolvedValue({
      source: "octocat/hello-world",
      created: 5,
      skipped: 0,
      prs_skipped: 1,
      failed: 0,
      labels_created: [],
      assignee_misses: 0,
      milestones_skipped: 0,
      releases_created: 2,
      errors: [],
    });
    fireEvent.click(screen.getByRole("button", { name: /^Import$/ }));
    await waitFor(() => expect(ghRunMock).toHaveBeenCalled());
    expect(screen.getByText(/Import result/)).toBeTruthy();
    const stat = screen.getByText("Releases created");
    // The Stat renders value above label — the value 2 sits in the same cell.
    expect(stat.parentElement?.textContent).toContain("2");
    expect(
      screen.getByText("Milestones skipped (unmappable)"),
    ).toBeTruthy();
  });
});
