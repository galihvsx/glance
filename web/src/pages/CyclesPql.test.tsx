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
import { EMPTY_FILTERS } from "../lib/filters";
import type { IssueViewRow } from "../lib/savedViews";
import type { Cycle, Issue } from "../lib/types";

// CycleDetail renders SavedViewsMenu → useAuth; the real provider needs
// a session — stub the auth context (BoardPql.test.tsx pattern).
vi.mock("../lib/auth", () => ({
  useAuth: () => ({ user: { id: "u-1", email: "u@x.com" }, loading: false }),
}));
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

function renderPage(opts: { views?: IssueViewRow[] } = {}) {
  const get = vi.spyOn(api, "get").mockImplementation((path: string) => {
    if (path === "/api/v1/workspaces/acme")
      return Promise.resolve({ role: 15 });
    if (path === BASE)
      return Promise.resolve({
        id: "p-eng",
        workspace_id: "w-acme",
        identifier: "ENG",
        name: "ENG",
        description: "",
        parallel_cycles: false,
        labels: [],
        created_at: "",
        updated_at: "",
      });
    if (path === `${BASE}/states`) return Promise.resolve({ states: [] });
    if (path === `${BASE}/cycles`) return Promise.resolve({ cycles: [cycle] });
    if (path === "/api/v1/projects/p-eng/views")
      return Promise.resolve({ views: opts.views ?? [] });
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
        // Distinct scoped result for a second saved view, so a
        // re-apply while already in PQL mode is observable in the
        // list as well as in the editor draft.
        if (pql.includes("Low")) {
          return Promise.resolve({ results: [issueA], next_cursor: null });
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

  it("cycle detail PQL editor shows the corrected save hint (C108T1)", async () => {
    renderPage();
    await openDetail();
    fireEvent.click(screen.getByRole("button", { name: "PQL" }));
    await screen.findByLabelText("PQL query");
    // Corrected wording: the detail-mode Views menu sits next to
    // the All issues / PQL toggle (C107), not in the page header.
    expect(
      screen.getByText(
        "To save this query, use Views → Save current view next to the All issues / PQL toggle; it re-applies when the view is opened.",
      ),
    ).toBeInTheDocument();
    expect(screen.queryByText(/in the page header/)).toBeNull();
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


describe("cycle detail saved views (C107T1)", () => {
  function makeViewRow(
    name: string,
    filters: IssueViewRow["filters"],
    opts: { isDefault?: boolean; shared?: boolean; ownerId?: string } = {},
  ): IssueViewRow {
    return {
      id: `v-${name}`,
      name,
      filters,
      display: null,
      is_default: opts.isDefault ?? false,
      shared: opts.shared ?? false,
      owner_id: opts.ownerId ?? "u-1",
      owner_name: "",
      created_at: "",
    };
  }

  async function openViewsMenu() {
    fireEvent.click(screen.getByRole("button", { name: "Saved views" }));
  }

  async function applyViewNamed(name: string) {
    await openViewsMenu();
    await screen.findByText(name);
    fireEvent.click(screen.getByTitle(`Apply view “${name}”`));
  }

  it("applying a saved PQL view narrows the list and seeds the PQL editor", async () => {
    renderPage({
      views: [
        makeViewRow("High priority", {
          ...EMPTY_FILTERS,
          pql: "priority = High",
        }),
      ],
    });
    await openDetail();
    await applyViewNamed("High priority");
    await waitFor(() => {
      expect(screen.queryByText("Fix login redirect")).toBeNull();
    });
    expect(screen.getByText("Urgent checkout bug")).toBeTruthy();
    expect(
      screen.getByRole("button", { name: "PQL" }),
    ).toHaveAttribute("aria-pressed", "true");
    // Probe-first then mode entry: the editor mounts seeded with the
    // applied query, not an empty draft.
    expect(
      (screen.getByLabelText("PQL query") as HTMLTextAreaElement).value,
    ).toBe("priority = High");
  });

  it("a default view present does NOT auto-apply on detail mount", async () => {
    renderPage({
      views: [
        makeViewRow(
          "High priority",
          { ...EMPTY_FILTERS, pql: "priority = High" },
          { isDefault: true },
        ),
      ],
    });
    await openDetail();
    // Opening the menu proves the views have loaded — the detail-mode
    // effect must still not have applied anything by then.
    await openViewsMenu();
    await screen.findByText("High priority");
    expect(screen.getByText("Fix login redirect")).toBeTruthy();
    expect(screen.getByText("Urgent checkout bug")).toBeTruthy();
    expect(screen.queryByLabelText("PQL query")).toBeNull();
    expect(
      screen.getByRole("button", { name: "All issues" }),
    ).toHaveAttribute("aria-pressed", "true");
  });

  it("a saved view with an invalid query fails honestly: list untouched, error surfaced", async () => {
    renderPage({
      views: [
        makeViewRow("Broken", { ...EMPTY_FILTERS, pql: "bogus = 1" }),
      ],
    });
    await openDetail();
    await applyViewNamed("Broken");
    // The detail-level alert carries the probe's 400 (the editor
    // mounts fresh, so its own inline error cannot show it).
    await screen.findByText(/unknown field: bogus/);
    expect(screen.getByText("Fix login redirect")).toBeTruthy();
    expect(screen.getByText("Urgent checkout bug")).toBeTruthy();
    // No unhandled rejection: the handler catches the probe failure
    // internally (a rejection here would fail the run), and the
    // editor is present for the user to correct the query.
    expect(screen.getByLabelText("PQL query")).toBeTruthy();
  });

  it("the detail menu renders PQL views only, own and shared", async () => {
    renderPage({
      views: [
        makeViewRow("High priority", {
          ...EMPTY_FILTERS,
          pql: "priority = High",
        }),
        makeViewRow("My bugs", EMPTY_FILTERS),
        makeViewRow(
          "Shared high",
          { ...EMPTY_FILTERS, pql: "priority = High" },
          { shared: true, ownerId: "u-2" },
        ),
        makeViewRow("Shared bugs", EMPTY_FILTERS, {
          shared: true,
          ownerId: "u-2",
        }),
      ],
    });
    await openDetail();
    await openViewsMenu();
    await screen.findByText("High priority");
    expect(screen.getByText("Shared high")).toBeTruthy();
    expect(screen.queryByText("My bugs")).toBeNull();
    expect(screen.queryByText("Shared bugs")).toBeNull();
  });

  it("applying a second saved view while already in PQL mode re-seeds the editor", async () => {
    renderPage({
      views: [
        makeViewRow("High priority", {
          ...EMPTY_FILTERS,
          pql: "priority = High",
        }),
        makeViewRow("Low priority", {
          ...EMPTY_FILTERS,
          pql: "priority = Low",
        }),
      ],
    });
    await openDetail();
    await applyViewNamed("High priority");
    await waitFor(() => {
      expect(screen.queryByText("Fix login redirect")).toBeNull();
    });
    expect(
      (screen.getByLabelText("PQL query") as HTMLTextAreaElement).value,
    ).toBe("priority = High");
    // Already in PQL mode: the editor is mounted, so without the
    // applied-query key its draft would stay on view A's query.
    await applyViewNamed("Low priority");
    // While the re-scoped query loads, the list falls back to the
    // full membership list (both issues) — wait for the refetch to
    // land, not just for the new result to appear.
    await waitFor(() => {
      expect(screen.queryByText("Urgent checkout bug")).toBeNull();
    });
    expect(screen.getByText("Fix login redirect")).toBeTruthy();
    expect(
      (screen.getByLabelText("PQL query") as HTMLTextAreaElement).value,
    ).toBe("priority = Low");
  });
});
