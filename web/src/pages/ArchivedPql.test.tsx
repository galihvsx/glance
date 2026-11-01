// @vitest-environment jsdom
// PQL mode on the archived list (C111T1): toggle on/off, PQL-mode
// fetch params (archived=1 + pql), the client-side archived-only
// filter retained on PQL-mode results (working-set matches never
// shown), invalid-PQL error display with the list intact, exit
// restoring the full archived set, and unarchive from a
// PQL-filtered list re-running the list in place. Copies the
// CyclesPql/CalendarPql detail-page test idioms (stub the workspace
// list for PageHeader, spy on api.get/api.patch).

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
import Archived from "./Archived";
import { api, ApiError } from "../lib/api";
import type { Issue } from "../lib/types";
import {
  makeQueryClient,
  stubMatchMedia,
} from "../components/shell/test-utils";

function makeIssue(
  id: string,
  displayId: string,
  name: string,
  archivedAt: string | null,
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
    archived_at: archivedAt,
    assignees: [],
    labels: [],
  };
}

// Two archived issues (the unfiltered set) plus one working-set
// issue the server also returns under archived=1 (include, not
// restrict) — the client filter must drop it everywhere.
const archivedA = makeIssue(
  "i-a",
  "ENG-1",
  "Old login bug",
  "2026-10-01T00:00:00Z",
);
const archivedB = makeIssue(
  "i-b",
  "ENG-2",
  "Retired report",
  "2026-10-05T00:00:00Z",
);
const workingW = makeIssue("i-w", "ENG-3", "Live checkout", null);

function renderPage() {
  // Stateful server: unarchiving an issue clears its archived_at,
  // so later fetches still return it under archived=1 but the
  // client filter drops it — exactly the production behavior.
  const unarchived = new Set<string>();
  const withState = (i: Issue): Issue =>
    unarchived.has(i.id) ? { ...i, archived_at: null } : i;
  const get = vi.spyOn(api, "get").mockImplementation((path: string) => {
    if (path === "/api/v1/workspaces")
      return Promise.resolve({ workspaces: [{ slug: "acme", name: "Acme" }] });
    if (path.includes("/issues")) {
      const qs = new URLSearchParams(path.split("?")[1] ?? "");
      const pql = qs.get("pql");
      if (pql !== null && pql.includes("bogus")) {
        return Promise.reject(
          new ApiError(400, "unknown field: bogus", "validation_error", {
            position: 1,
          }),
        );
      }
      if (pql === "priority = High") {
        // Predicate match: one archived issue AND one working-set
        // issue — the view must show only the archived one.
        return Promise.resolve({
          results: [withState(archivedA), withState(workingW)],
          next_cursor: null,
        });
      }
      if (pql === "priority = Low") {
        return Promise.resolve({
          results: [withState(archivedB)],
          next_cursor: null,
        });
      }
      return Promise.resolve({
        results: [withState(archivedA), withState(archivedB), withState(workingW)],
        next_cursor: null,
      });
    }
    return Promise.reject(new Error(`unexpected GET ${path}`));
  });
  const patch = vi.spyOn(api, "patch").mockImplementation((path: string) => {
    const id = path.split("/").pop() ?? "";
    unarchived.add(id);
    return Promise.resolve({ ...archivedA, id, archived_at: null });
  });
  const qc = makeQueryClient();
  render(
    <MemoryRouter initialEntries={["/w/acme/p/ENG/archived"]}>
      <QueryClientProvider client={qc}>
        <Routes>
          <Route
            path="/w/:slug/p/:identifier/archived"
            element={<Archived />}
          />
        </Routes>
      </QueryClientProvider>
    </MemoryRouter>,
  );
  return { get, patch };
}

async function waitForInitial() {
  await screen.findByText("Old login bug");
  await screen.findByText("Retired report");
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

function editorValue(): string {
  return (screen.getByLabelText("PQL query") as HTMLTextAreaElement).value;
}

beforeEach(() => {
  stubMatchMedia(false);
  localStorage.clear();
});

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

describe("archived PQL mode (C111T1)", () => {
  it("default mode shows the archived set only, with no PQL editor", async () => {
    const { get } = renderPage();
    await waitForInitial();
    // Working-set issue returned by the server is filtered out.
    expect(screen.queryByText("Live checkout")).toBeNull();
    expect(screen.queryByLabelText("PQL query")).toBeNull();
    expect(
      screen.getByRole("button", { name: "All archived" }),
    ).toHaveAttribute("aria-pressed", "true");
    // Default fetches carry archived=1 but no pql.
    const calls = issueCalls(get);
    expect(calls.length).toBeGreaterThan(0);
    for (const qs of calls) {
      expect(qs.get("archived")).toBe("1");
      expect(qs.get("pql")).toBeNull();
    }
  });

  it("applying a query enters PQL mode with the predicate applied AND the archived-only filter retained", async () => {
    const { get } = renderPage();
    await waitForInitial();
    await applyPql("priority = High");
    // Await the filtered list, not just the absence of the old one:
    // while a new PQL query loads, the display falls back to the
    // unfiltered set, so absence alone passes before the fetch lands.
    await waitFor(() => {
      expect(screen.queryByText("Retired report")).toBeNull();
    });
    expect(screen.getByText("Old login bug")).toBeTruthy();
    // Working-set match of the same predicate is never shown.
    expect(screen.queryByText("Live checkout")).toBeNull();
    expect(editorValue()).toBe("priority = High");
    // Every PQL fetch carries archived=1 plus pql.
    const pqlCalls = issueCalls(get).filter((qs) => qs.get("pql") !== null);
    expect(pqlCalls.length).toBeGreaterThan(0);
    for (const qs of pqlCalls) {
      expect(qs.get("archived")).toBe("1");
      expect(qs.get("pql")).toBe("priority = High");
      expect(qs.get("per_page")).toBe("100");
    }
  });

  it("invalid PQL shows the editor error with column and leaves the displayed set untouched", async () => {
    renderPage();
    await waitForInitial();
    await applyPql("bogus = 1");
    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain("unknown field: bogus");
    expect(alert.textContent).toContain("at column 1");
    // Displayed set untouched: both archived issues still on screen,
    // and the working-set issue never appears. No unhandled
    // rejection: the editor catches the probe failure internally.
    expect(screen.getByText("Old login bug")).toBeTruthy();
    expect(screen.getByText("Retired report")).toBeTruthy();
    expect(screen.queryByText("Live checkout")).toBeNull();
  });

  it("leaving PQL mode restores the full archived set and closes the editor", async () => {
    renderPage();
    await waitForInitial();
    await applyPql("priority = High");
    await waitFor(() => {
      expect(screen.queryByText("Retired report")).toBeNull();
    });
    fireEvent.click(screen.getByRole("button", { name: "All archived" }));
    await screen.findByText("Retired report");
    expect(screen.getByText("Old login bug")).toBeTruthy();
    expect(screen.queryByLabelText("PQL query")).toBeNull();
  });

  it("re-applying a second query while in PQL mode re-seeds the editor", async () => {
    renderPage();
    await waitForInitial();
    await applyPql("priority = High");
    await waitFor(() => {
      expect(screen.queryByText("Retired report")).toBeNull();
    });
    expect(editorValue()).toBe("priority = High");
    // Already in PQL mode: the editor is mounted, so without the
    // applied-query key its draft would stay on the first query.
    fireEvent.change(screen.getByLabelText("PQL query"), {
      target: { value: "priority = Low" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Apply" }));
    // Same fallback caveat as the first apply: await the new set
    // (absence of the old row), not just the presence of the new
    // one — the fallback shows the unfiltered set while loading.
    await waitFor(() => {
      expect(screen.queryByText("Old login bug")).toBeNull();
    });
    expect(screen.getByText("Retired report")).toBeTruthy();
    expect(editorValue()).toBe("priority = Low");
  });

  it("unarchive from a PQL-filtered list re-runs the list in place", async () => {
    const { get, patch } = renderPage();
    await waitForInitial();
    await applyPql("priority = High");
    await waitFor(() => {
      expect(screen.queryByText("Retired report")).toBeNull();
    });
    const pqlCallsBefore = issueCalls(get).filter(
      (qs) => qs.get("pql") === "priority = High",
    ).length;
    fireEvent.click(screen.getByRole("button", { name: "Unarchive" }));
    // The PATCH unarchives, then the PQL-mode list re-runs: the
    // unarchived issue now fails the client-side archived-only
    // filter, so the filtered list empties in place.
    await screen.findByText("No issues match this PQL query");
    expect(patch).toHaveBeenCalled();
    const patchCall = patch.mock.calls[0];
    expect(patchCall[0]).toContain("/i-a");
    expect(patchCall[1]).toEqual({ archived: false });
    const pqlCallsAfter = issueCalls(get).filter(
      (qs) => qs.get("pql") === "priority = High",
    ).length;
    expect(pqlCallsAfter).toBeGreaterThan(pqlCallsBefore);
  });

  it("editor hides the save hint: no Views save path on this page yet", async () => {
    renderPage();
    await waitForInitial();
    fireEvent.click(screen.getByRole("button", { name: "PQL" }));
    await screen.findByLabelText("PQL query");
    expect(screen.queryByText(/Save current view/)).toBeNull();
  });
});
