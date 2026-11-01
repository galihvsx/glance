// @vitest-environment jsdom
// PQL mode on the roadmap (C109T3): the Cycles detail pattern brought
// to Roadmap — "All issues" / "PQL" toggle + PqlEditor above the
// roadmap, probe-first apply through the plain list, display-only
// filtering. PQL mode changes ONLY which issues render: month
// buckets and the Unscheduled lane are computed from the filtered
// set. Default mode is byte-for-byte today's behavior (plain list,
// cursor loop, bucket by target_date month). No Views save path on
// this page yet (Board precedent), so the editor passes
// showSaveHint={false}.

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import Roadmap from "./Roadmap";
import { api, ApiError } from "../lib/api";
import type { Issue, IssueState } from "../lib/types";
import {
  makeQueryClient,
  stubMatchMedia,
} from "../components/shell/test-utils";

const BASE = "/api/v1/workspaces/acme/projects/ENG";

const states: IssueState[] = [
  {
    id: "s-1",
    project_id: "p-eng",
    name: "Todo",
    group: "unstarted",
    color: "#888",
    sequence: 1,
    created_at: "",
    updated_at: "",
  },
];

function makeIssue(
  id: string,
  displayId: string,
  name: string,
  targetDate: string | null,
): Issue {
  return {
    id,
    project_id: "p-eng",
    sequence_id: 1,
    display_id: displayId,
    name,
    priority: 3,
    state_id: "s-1",
    sort_order: 0,
    is_draft: false,
    created_by: "u-1",
    created_at: "",
    updated_at: "",
    assignees: [],
    labels: [],
    target_date: targetDate,
  };
}

// Anchor is the current month at render time, so target dates are
// built relative to it: A in the anchor month, B in the next month,
// C undated (Unscheduled lane). Built from the same UTC-month math
// as lib/roadmap so the test never depends on today's date.
function monthKey(offset: number): string {
  const d = new Date();
  const m = new Date(Date.UTC(d.getUTCFullYear(), d.getUTCMonth() + offset, 1));
  return `${m.getUTCFullYear()}-${String(m.getUTCMonth() + 1).padStart(2, "0")}-15T00:00:00Z`;
}

const issueA = makeIssue("i-a", "ENG-1", "November launch task", monthKey(0));
const issueB = makeIssue("i-b", "ENG-2", "December launch task", monthKey(1));
const issueC = makeIssue("i-c", "ENG-3", "Unscheduled backlog task", null);

function renderPage() {
  const get = vi.spyOn(api, "get").mockImplementation((path: string) => {
    if (path === "/api/v1/workspaces")
      return Promise.resolve({ workspaces: [] });
    if (path === "/api/v1/workspaces/acme")
      return Promise.resolve({ role: 15 });
    if (path === `${BASE}/states`) return Promise.resolve({ states });
    if (path.startsWith(`${BASE}/issues`)) {
      const qs = new URLSearchParams(path.split("?")[1] ?? "");
      const pql = qs.get("pql");
      if (pql !== null) {
        if (pql.includes("bogus")) {
          return Promise.reject(
            new ApiError(400, "unknown field: bogus", "validation_error", {
              position: 1,
            }),
          );
        }
        // Distinct result for a second query, so a re-apply while
        // already in PQL mode is observable in the lanes as well
        // as in the editor draft.
        if (pql.includes("Low")) {
          return Promise.resolve({ results: [issueC], next_cursor: null });
        }
        return Promise.resolve({ results: [issueB], next_cursor: null });
      }
      return Promise.resolve({
        results: [issueA, issueB, issueC],
        next_cursor: null,
      });
    }
    return Promise.reject(new Error(`unexpected GET ${path}`));
  });
  const qc = makeQueryClient();
  render(
    <MemoryRouter initialEntries={["/w/acme/p/ENG/roadmap"]}>
      <QueryClientProvider client={qc}>
        <Routes>
          <Route path="/w/:slug/p/:identifier/roadmap" element={<Roadmap />} />
        </Routes>
      </QueryClientProvider>
    </MemoryRouter>,
  );
  return { get };
}

async function waitForRoadmap() {
  await screen.findByText("November launch task");
  await screen.findByText("December launch task");
  await screen.findByText("Unscheduled backlog task");
}

async function applyPql(query: string) {
  fireEvent.click(screen.getByRole("button", { name: "PQL" }));
  await screen.findByLabelText("PQL query");
  fireEvent.change(screen.getByLabelText("PQL query"), {
    target: { value: query },
  });
  fireEvent.click(screen.getByRole("button", { name: "Apply" }));
}

function issueCalls(get: ReturnType<typeof renderPage>["get"]) {
  return get.mock.calls
    .map((c) => c[0] as string)
    .filter((p) => p.includes("/issues"))
    .map((p) => new URLSearchParams(p.split("?")[1] ?? ""));
}

beforeEach(() => {
  stubMatchMedia(false);
});

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

describe("roadmap PQL mode (C109T3)", () => {
  it("default mode shows the full list with no PQL editor, and no pql param", async () => {
    const { get } = renderPage();
    await waitForRoadmap();
    expect(screen.queryByLabelText("PQL query")).toBeNull();
    expect(
      screen.getByRole("button", { name: "All issues" }),
    ).toHaveAttribute("aria-pressed", "true");
    const calls = issueCalls(get);
    expect(calls.length).toBeGreaterThan(0);
    for (const qs of calls) {
      expect(qs.get("pql")).toBeNull();
    }
  });

  it("applying a PQL query narrows buckets and the Unscheduled lane, and the editor shows the applied query", async () => {
    const { get } = renderPage();
    await waitForRoadmap();
    await applyPql("priority = High");
    await waitFor(() => {
      expect(screen.queryByText("November launch task")).toBeNull();
    });
    expect(screen.getByText("December launch task")).toBeTruthy();
    // The Unscheduled lane narrowed too: its only issue is gone and
    // the lane reports empty.
    expect(screen.queryByText("Unscheduled backlog task")).toBeNull();
    expect(screen.getByText("Every issue has a target date.")).toBeTruthy();
    // The editor shows the applied query.
    expect(
      (screen.getByLabelText("PQL query") as HTMLTextAreaElement).value,
    ).toBe("priority = High");
    // The PQL fetch goes through the plain list with pql, no
    // window/scope params.
    const pqlCalls = issueCalls(get).filter((qs) => qs.get("pql") !== null);
    expect(pqlCalls.length).toBeGreaterThan(0);
    for (const qs of pqlCalls) {
      expect(qs.get("pql")).toBe("priority = High");
    }
  });

  it("leaving PQL mode restores the unfiltered list and closes the editor", async () => {
    renderPage();
    await waitForRoadmap();
    await applyPql("priority = High");
    await waitFor(() => {
      expect(screen.queryByText("November launch task")).toBeNull();
    });
    fireEvent.click(screen.getByRole("button", { name: "All issues" }));
    await waitForRoadmap();
    expect(screen.queryByLabelText("PQL query")).toBeNull();
    expect(
      screen.getByRole("button", { name: "All issues" }),
    ).toHaveAttribute("aria-pressed", "true");
  });

  it("invalid PQL shows the editor error with column and leaves the displayed set untouched", async () => {
    renderPage();
    await waitForRoadmap();
    await applyPql("bogus = 1");
    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain("unknown field: bogus");
    expect(alert.textContent).toContain("at column 1");
    // Set untouched: all three issues still on screen.
    await waitForRoadmap();
  });

  it("invalid PQL while already in PQL mode leaves the filtered set untouched", async () => {
    renderPage();
    await waitForRoadmap();
    await applyPql("priority = High");
    await waitFor(() => {
      expect(screen.queryByText("November launch task")).toBeNull();
    });
    // Re-applying a bad query from the mounted editor: the probe
    // rejects, appliedQuery never changes, the filtered set stays.
    fireEvent.change(screen.getByLabelText("PQL query"), {
      target: { value: "bogus = 1" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Apply" }));
    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain("unknown field: bogus");
    expect(screen.getByText("December launch task")).toBeTruthy();
    expect(screen.queryByText("November launch task")).toBeNull();
    expect(screen.queryByText("Unscheduled backlog task")).toBeNull();
  });

  it("re-applying a second query while in PQL mode re-seeds the editor", async () => {
    renderPage();
    await waitForRoadmap();
    await applyPql("priority = High");
    await waitFor(() => {
      expect(screen.queryByText("November launch task")).toBeNull();
    });
    expect(
      (screen.getByLabelText("PQL query") as HTMLTextAreaElement).value,
    ).toBe("priority = High");
    // Already in PQL mode: the editor is mounted, so without the
    // applied-query key its draft would stay on the first query.
    fireEvent.change(screen.getByLabelText("PQL query"), {
      target: { value: "priority = Low" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Apply" }));
    await waitFor(() => {
      expect(screen.queryByText("December launch task")).toBeNull();
    });
    expect(screen.getByText("Unscheduled backlog task")).toBeTruthy();
    expect(
      (screen.getByLabelText("PQL query") as HTMLTextAreaElement).value,
    ).toBe("priority = Low");
  });

  it("Clear in PQL mode restores the full list without leaving the mode", async () => {
    renderPage();
    await waitForRoadmap();
    await applyPql("priority = High");
    await waitFor(() => {
      expect(screen.queryByText("November launch task")).toBeNull();
    });
    fireEvent.click(screen.getByRole("button", { name: "Clear" }));
    await waitForRoadmap();
    expect(screen.getByLabelText("PQL query")).toBeTruthy();
  });

  it("the PQL editor shows no Views save hint on this page", async () => {
    renderPage();
    await waitForRoadmap();
    fireEvent.click(screen.getByRole("button", { name: "PQL" }));
    await screen.findByLabelText("PQL query");
    expect(screen.queryByText(/Save current view/)).toBeNull();
  });
});
