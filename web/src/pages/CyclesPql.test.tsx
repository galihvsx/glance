// @vitest-environment jsdom
// PQL mode on the cycle detail list (C95T1): toggle on/off, PQL-mode
// fetch params (cycle + pql both present), invalid-PQL error display
// with the list intact, progress counts unchanged in PQL mode, and
// exit restoring the full list. Progress counts come from the cycle's
// progress_snapshot, never from the displayed (possibly filtered)
// list — plan decision 5.

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
import Cycles from "./Cycles";
import { api, ApiError } from "../lib/api";
import type { Cycle, Issue } from "../lib/types";
import {
  makeQueryClient,
  stubMatchMedia,
} from "../components/shell/test-utils";

const BASE = "/api/v1/workspaces/acme/projects/ENG";

const cycle: Cycle = {
  id: "cyc-1",
  project_id: "p-eng",
  name: "Sprint 13",
  start_date: "2026-11-02T00:00:00Z",
  end_date: "2026-11-15T00:00:00Z",
  status: "current",
  // Open 2 (unstarted), In progress 1 (started), Done 1 (completed).
  progress_snapshot: { unstarted: 2, started: 1, completed: 1 },
  created_at: "",
  updated_at: "",
};

function makeIssue(id: string, displayId: string, name: string): Issue {
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
  };
}

const issueA = makeIssue("i-a", "ENG-1", "Fix login redirect");
const issueB = makeIssue("i-b", "ENG-2", "Urgent checkout bug");

function renderPage() {
  const get = vi.spyOn(api, "get").mockImplementation((path: string) => {
    if (path === "/api/v1/workspaces/acme")
      return Promise.resolve({ role: 15 });
    if (path === `${BASE}/states`) return Promise.resolve({ states: [] });
    if (path === `${BASE}/cycles`) return Promise.resolve({ cycles: [cycle] });
    if (path.includes("/issues")) {
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
        return Promise.resolve({ results: [issueB], next_cursor: null });
      }
      return Promise.resolve({
        results: [issueA, issueB],
        next_cursor: null,
      });
    }
    if (path.includes("/burndown"))
      return Promise.resolve({ total_scope: 0, days: [] });
    return Promise.reject(new Error(`unexpected GET ${path}`));
  });
  const qc = makeQueryClient();
  render(
    <MemoryRouter initialEntries={["/w/acme/p/ENG/cycles"]}>
      <QueryClientProvider client={qc}>
        <Routes>
          <Route path="/w/:slug/p/:identifier/cycles" element={<Cycles />} />
        </Routes>
      </QueryClientProvider>
    </MemoryRouter>,
  );
  return { get };
}

async function openDetail() {
  fireEvent.click(
    await screen.findByRole("button", { name: "Open cycle Sprint 13" }),
  );
  await screen.findByText("Issues in this cycle");
  await screen.findByText("Fix login redirect");
  await screen.findByText("Urgent checkout bug");
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

function statValue(label: string): string | null {
  const el = screen.getByText(label).parentElement;
  return el ? el.textContent : null;
}

beforeEach(() => {
  stubMatchMedia(false);
});

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

describe("cycle detail PQL mode (C95T1)", () => {
  it("default mode shows the full membership list with no PQL editor", async () => {
    const { get } = renderPage();
    await openDetail();
    expect(screen.queryByLabelText("PQL query")).toBeNull();
    expect(
      screen.getByRole("button", { name: "All issues" }),
    ).toHaveAttribute("aria-pressed", "true");
    // Default fetch carries cycle but no pql.
    const calls = issueCalls(get);
    expect(calls.length).toBeGreaterThan(0);
    for (const qs of calls) {
      expect(qs.get("cycle")).toBe("cyc-1");
      expect(qs.get("pql")).toBeNull();
    }
  });

  it("PQL-mode fetch carries both cycle and pql, and shows only the filtered list", async () => {
    const { get } = renderPage();
    await openDetail();
    await applyPql("priority = High");
    await waitFor(() => {
      expect(screen.queryByText("Fix login redirect")).toBeNull();
    });
    expect(screen.getByText("Urgent checkout bug")).toBeTruthy();
    const pqlCalls = issueCalls(get).filter((qs) => qs.get("pql") !== null);
    expect(pqlCalls.length).toBeGreaterThan(0);
    for (const qs of pqlCalls) {
      expect(qs.get("cycle")).toBe("cyc-1");
      expect(qs.get("pql")).toBe("priority = High");
    }
  });

  it("cycle detail PQL editor hides the save hint (C96T1: no Views menu on detail pages)", async () => {
    renderPage();
    await openDetail();
    fireEvent.click(screen.getByRole("button", { name: "PQL" }));
    await screen.findByLabelText("PQL query");
    expect(screen.queryByText(/To save this query, use Views/)).toBeNull();
  });

  it("invalid PQL shows the editor error with column and leaves the list intact", async () => {
    renderPage();
    await openDetail();
    await applyPql("bogus = 1");
    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain("unknown field: bogus");
    expect(alert.textContent).toContain("at column 1");
    // List untouched: both membership issues still on screen.
    expect(screen.getByText("Fix login redirect")).toBeTruthy();
    expect(screen.getByText("Urgent checkout bug")).toBeTruthy();
  });

  it("progress counts stay on the unfiltered snapshot in PQL mode", async () => {
    renderPage();
    await openDetail();
    expect(statValue("Open")).toContain("2");
    expect(statValue("In progress")).toContain("1");
    expect(statValue("Done")).toContain("1");
    await applyPql("priority = High");
    await waitFor(() => {
      expect(screen.queryByText("Fix login redirect")).toBeNull();
    });
    // Filtered list has 1 issue, but the stat band is unchanged.
    expect(statValue("Open")).toContain("2");
    expect(statValue("In progress")).toContain("1");
    expect(statValue("Done")).toContain("1");
  });

  it("leaving PQL mode restores the full list and closes the editor", async () => {
    renderPage();
    await openDetail();
    await applyPql("priority = High");
    await waitFor(() => {
      expect(screen.queryByText("Fix login redirect")).toBeNull();
    });
    fireEvent.click(screen.getByRole("button", { name: "All issues" }));
    await screen.findByText("Fix login redirect");
    expect(screen.getByText("Urgent checkout bug")).toBeTruthy();
    expect(screen.queryByLabelText("PQL query")).toBeNull();
  });

  it("Clear in PQL mode restores the full list without leaving the mode", async () => {
    renderPage();
    await openDetail();
    await applyPql("priority = High");
    await waitFor(() => {
      expect(screen.queryByText("Fix login redirect")).toBeNull();
    });
    fireEvent.click(screen.getByRole("button", { name: "Clear" }));
    await screen.findByText("Fix login redirect");
    expect(screen.getByText("Urgent checkout bug")).toBeTruthy();
    expect(screen.getByLabelText("PQL query")).toBeTruthy();
  });
});

describe("cycle detail PQL refetch on membership mutation (C103T0)", () => {
  const issueC = makeIssue("i-c", "ENG-3", "New urgent task");

  function renderPageC103() {
    // Stateful mock: membership and the PQL-filtered result change
    // only after a membership mutation, so a refetch is observable
    // as a new PQL GET carrying the same query text plus a new list.
    let added = false;
    let ended = false;
    const get = vi.spyOn(api, "get").mockImplementation((path: string) => {
      if (path === "/api/v1/workspaces/acme")
        return Promise.resolve({ role: 15 });
      if (path === `${BASE}/states`) return Promise.resolve({ states: [] });
      if (path === `${BASE}/cycles`)
        return Promise.resolve({ cycles: [cycle] });
      if (path.includes("/issues")) {
        const qs = new URLSearchParams(path.split("?")[1] ?? "");
        if (qs.get("pql") !== null) {
          if (ended) return Promise.resolve({ results: [], next_cursor: null });
          return Promise.resolve({
            results: added ? [issueB, issueC] : [issueB],
            next_cursor: null,
          });
        }
        if (qs.get("cycle") !== null) {
          return Promise.resolve({
            results: ended
              ? [issueA]
              : added
                ? [issueA, issueB, issueC]
                : [issueA, issueB],
            next_cursor: null,
          });
        }
        // Board-issues fetch for the add dialog: all project issues.
        return Promise.resolve({
          results: [issueA, issueB, issueC],
          next_cursor: null,
        });
      }
      if (path.includes("/burndown"))
        return Promise.resolve({ total_scope: 0, days: [] });
      return Promise.reject(new Error(`unexpected GET ${path}`));
    });
    const post = vi.spyOn(api, "post").mockImplementation((path: string) => {
      if (path === `${BASE}/cycles/cyc-1/issues`) {
        added = true;
        return Promise.resolve({});
      }
      if (path === `${BASE}/cycles/cyc-1/end`) {
        ended = true;
        return Promise.resolve({ ...cycle, status: "completed" });
      }
      return Promise.reject(new Error(`unexpected POST ${path}`));
    });
    const qc = makeQueryClient();
    render(
      <MemoryRouter initialEntries={["/w/acme/p/ENG/cycles"]}>
        <QueryClientProvider client={qc}>
          <Routes>
            <Route path="/w/:slug/p/:identifier/cycles" element={<Cycles />} />
          </Routes>
        </QueryClientProvider>
      </MemoryRouter>,
    );
    return { get, post };
  }

  function pqlCalls(get: ReturnType<typeof renderPageC103>["get"]) {
    return issueCalls(get).filter((qs) => qs.get("pql") !== null);
  }

  it("adding issues re-runs the same committed PQL query and updates the filtered list", async () => {
    const { get } = renderPageC103();
    await openDetail();
    await applyPql("priority = High");
    await waitFor(() => {
      expect(screen.queryByText("Fix login redirect")).toBeNull();
    });
    expect(screen.getByText("Urgent checkout bug")).toBeTruthy();
    expect(screen.queryByText("New urgent task")).toBeNull();
    const before = pqlCalls(get).length;
    expect(before).toBeGreaterThan(0);

    // Add issueC via the dialog (ENG-1/ENG-2 are already in-cycle).
    fireEvent.click(screen.getByRole("button", { name: "Add issues" }));
    const checkbox = await screen.findByRole("checkbox", { name: /ENG-3/ });
    fireEvent.click(checkbox);
    fireEvent.click(
      await screen.findByRole("button", { name: "Add 1 issue" }),
    );

    // Same query text, re-fetched: the newly added matching issue appears.
    await screen.findByText("New urgent task");
    const after = pqlCalls(get);
    expect(after.length).toBeGreaterThan(before);
    for (const qs of after) {
      expect(qs.get("cycle")).toBe("cyc-1");
      expect(qs.get("pql")).toBe("priority = High");
    }
    // Still in PQL mode: the non-matching membership issue stays hidden.
    expect(screen.queryByText("Fix login redirect")).toBeNull();
    expect(screen.getByLabelText("PQL query")).toBeTruthy();
  });

  it("ending the cycle re-runs the same committed PQL query and updates the filtered list", async () => {
    const { get } = renderPageC103();
    await openDetail();
    await applyPql("priority = High");
    await waitFor(() => {
      expect(screen.queryByText("Fix login redirect")).toBeNull();
    });
    const before = pqlCalls(get).length;
    expect(before).toBeGreaterThan(0);

    fireEvent.click(screen.getByRole("button", { name: "End now" }));
    fireEvent.click(
      await screen.findByRole("button", { name: "End cycle" }),
    );

    // Rollover empties the filtered result; the refetch surfaces it
    // in place without leaving PQL mode.
    await screen.findByText("No issues match this PQL query");
    const after = pqlCalls(get);
    expect(after.length).toBeGreaterThan(before);
    for (const qs of after) {
      expect(qs.get("cycle")).toBe("cyc-1");
      expect(qs.get("pql")).toBe("priority = High");
    }
    expect(screen.getByLabelText("PQL query")).toBeTruthy();
  });
});

