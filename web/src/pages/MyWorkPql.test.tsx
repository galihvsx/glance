// @vitest-environment jsdom
// PQL mode on MyWork (C112T1): toggle on/off, PQL-mode fetch params
// (filter + pql + limit=100), the filter scope retained under a PQL
// predicate (a watched row matching the predicate never shows under
// the assigned tab), invalid-PQL error display with the list intact,
// exit restoring the full set, and switching tabs in PQL mode
// re-running the same query under the new filter. Copies the
// ArchivedPql/CyclesPql detail-page test idioms (stub the workspace
// list for PageHeader, spy on api.get).

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import MyWork from "./MyWork";
import { api, ApiError } from "../lib/api";
import type { MyWorkItem } from "../lib/mywork";
import {
  makeQueryClient,
  stubMatchMedia,
} from "../components/shell/test-utils";

function makeItem(
  id: string,
  displayId: string,
  name: string,
  projectId: string,
  projectIdentifier: string,
  projectName: string,
  priority: number,
): MyWorkItem {
  return {
    id,
    display_id: displayId,
    name,
    priority,
    state_id: "s-1",
    state_name: "Todo",
    state_group: "unstarted",
    project_id: projectId,
    project_identifier: projectIdentifier,
    project_name: projectName,
    updated_at: "2026-10-05T00:00:00Z",
  };
}

// Seeded rows across two projects, one per filter bucket, with a
// High/Low split inside the assigned bucket so the predicate test
// can distinguish "predicate applied" from "bucket returned".
const assignedHigh = makeItem(
  "i-ah",
  "ENG-1",
  "Assigned login bug",
  "p-eng",
  "ENG",
  "Engine",
  4,
);
const assignedLow = makeItem(
  "i-al",
  "ENG-2",
  "Assigned docs task",
  "p-eng",
  "ENG",
  "Engine",
  1,
);
const createdHigh = makeItem(
  "i-ch",
  "WEB-1",
  "Created checkout bug",
  "p-web",
  "WEB",
  "Website",
  4,
);
const watchedHigh = makeItem(
  "i-wh",
  "WEB-2",
  "Watched deploy bug",
  "p-web",
  "WEB",
  "Website",
  4,
);

const BUCKETS: Record<string, MyWorkItem[]> = {
  assigned: [assignedHigh, assignedLow],
  created: [createdHigh],
  watched: [watchedHigh],
};
const HIGH = new Set(["i-ah", "i-ch", "i-wh"]);
const LOW = new Set(["i-al"]);

function renderPage() {
  const get = vi.spyOn(api, "get").mockImplementation((path: string) => {
    if (path === "/api/v1/workspaces")
      return Promise.resolve({ workspaces: [{ slug: "acme", name: "Acme" }] });
    if (path.includes("/my-issues")) {
      const qs = new URLSearchParams(path.split("?")[1] ?? "");
      const pql = qs.get("pql");
      if (pql !== null && pql.includes("bogus")) {
        return Promise.reject(
          new ApiError(400, "unknown field: bogus", "validation_error", {
            position: 1,
          }),
        );
      }
      let rows = BUCKETS[qs.get("filter") ?? ""] ?? [];
      if (pql === "priority = High") rows = rows.filter((r) => HIGH.has(r.id));
      if (pql === "priority = Low") rows = rows.filter((r) => LOW.has(r.id));
      return Promise.resolve({ issues: rows });
    }
    return Promise.reject(new Error(`unexpected GET ${path}`));
  });
  const qc = makeQueryClient();
  render(
    <MemoryRouter initialEntries={["/w/acme/my-work"]}>
      <QueryClientProvider client={qc}>
        <Routes>
          <Route path="/w/:slug/my-work" element={<MyWork />} />
        </Routes>
      </QueryClientProvider>
    </MemoryRouter>,
  );
  return { get };
}

async function waitForInitial() {
  await screen.findByText("Assigned login bug");
  await screen.findByText("Assigned docs task");
}

async function applyPql(query: string) {
  fireEvent.click(screen.getByRole("button", { name: "PQL" }));
  await screen.findByLabelText("PQL query");
  fireEvent.change(screen.getByLabelText("PQL query"), {
    target: { value: query },
  });
  fireEvent.click(screen.getByRole("button", { name: "Apply" }));
}

function myWorkCalls(get: ReturnType<typeof renderPage>["get"]) {
  return get.mock.calls
    .map((c) => c[0] as string)
    .filter((p) => p.includes("/my-issues"))
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

describe("mywork PQL mode (C112T1)", () => {
  it("default mode shows the assigned set only, with no PQL editor", async () => {
    const { get } = renderPage();
    await waitForInitial();
    expect(screen.queryByText("Created checkout bug")).toBeNull();
    expect(screen.queryByText("Watched deploy bug")).toBeNull();
    expect(screen.queryByLabelText("PQL query")).toBeNull();
    expect(screen.getByRole("button", { name: "All work" })).toHaveAttribute(
      "aria-pressed",
      "true",
    );
    // Default fetches carry filter + limit but no pql.
    const calls = myWorkCalls(get);
    expect(calls.length).toBeGreaterThan(0);
    for (const qs of calls) {
      expect(qs.get("filter")).toBe("assigned");
      expect(qs.get("limit")).toBe("100");
      expect(qs.get("pql")).toBeNull();
    }
  });

  it("applying a query enters PQL mode with the predicate applied AND the filter scope retained", async () => {
    const { get } = renderPage();
    await waitForInitial();
    await applyPql("priority = High");
    // Await the filtered list itself: unlike the Archived shape
    // (which falls back to the unfiltered set while loading), this
    // single-query shape shows a loading state under the new key,
    // so the old row's absence alone would pass before the fetch
    // lands.
    await screen.findByText("Assigned login bug");
    expect(screen.queryByText("Assigned docs task")).toBeNull();
    // A watched row matching the same predicate never shows under
    // the assigned tab: the PQL predicate ANDs with the filter.
    expect(screen.queryByText("Watched deploy bug")).toBeNull();
    expect(screen.queryByText("Created checkout bug")).toBeNull();
    expect(editorValue()).toBe("priority = High");
    // Every PQL fetch carries filter + pql + limit=100.
    const pqlCalls = myWorkCalls(get).filter((qs) => qs.get("pql") !== null);
    expect(pqlCalls.length).toBeGreaterThan(0);
    for (const qs of pqlCalls) {
      expect(qs.get("filter")).toBe("assigned");
      expect(qs.get("pql")).toBe("priority = High");
      expect(qs.get("limit")).toBe("100");
    }
  });

  it("invalid PQL shows the editor error with column and leaves the displayed set untouched", async () => {
    renderPage();
    await waitForInitial();
    await applyPql("bogus = 1");
    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain("unknown field: bogus");
    expect(alert.textContent).toContain("at column 1");
    // Displayed set untouched: both assigned rows still on screen.
    // No unhandled rejection: the editor catches the probe failure
    // internally, and the list query key never changed.
    expect(screen.getByText("Assigned login bug")).toBeTruthy();
    expect(screen.getByText("Assigned docs task")).toBeTruthy();
  });

  it("leaving PQL mode restores the full set and closes the editor", async () => {
    renderPage();
    await waitForInitial();
    await applyPql("priority = High");
    await waitFor(() => {
      expect(screen.queryByText("Assigned docs task")).toBeNull();
    });
    fireEvent.click(screen.getByRole("button", { name: "All work" }));
    await screen.findByText("Assigned docs task");
    expect(screen.getByText("Assigned login bug")).toBeTruthy();
    expect(screen.queryByLabelText("PQL query")).toBeNull();
  });

  it("switching tabs in PQL mode keeps PQL mode and re-runs the query under the new filter", async () => {
    const { get } = renderPage();
    await waitForInitial();
    await applyPql("priority = High");
    await waitFor(() => {
      expect(screen.queryByText("Assigned docs task")).toBeNull();
    });
    const user = userEvent.setup();
    await user.click(screen.getByRole("tab", { name: "Created by me" }));
    // Same query, new scope: the created High row appears, the
    // assigned High row leaves, and the editor stays mounted.
    await screen.findByText("Created checkout bug");
    await waitFor(() => {
      expect(screen.queryByText("Assigned login bug")).toBeNull();
    });
    expect(screen.getByLabelText("PQL query")).toBeTruthy();
    expect(editorValue()).toBe("priority = High");
    const createdPqlCalls = myWorkCalls(get).filter(
      (qs) => qs.get("filter") === "created" && qs.get("pql") === "priority = High",
    );
    expect(createdPqlCalls.length).toBeGreaterThan(0);
  });

  it("a query matching nothing under the current tab shows the PQL empty state", async () => {
    const user = userEvent.setup();
    renderPage();
    await waitForInitial();
    await user.click(screen.getByRole("tab", { name: "Created by me" }));
    await screen.findByText("Created checkout bug");
    await applyPql("priority = Low");
    await screen.findByText("No issues match this PQL query");
    expect(screen.queryByText("Created checkout bug")).toBeNull();
  });

  it("editor stages the no-save-path-yet hint text for the views cycle (showSaveHint stays false, so no hint renders)", async () => {
    renderPage();
    await waitForInitial();
    fireEvent.click(screen.getByRole("button", { name: "PQL" }));
    await screen.findByLabelText("PQL query");
    // The staged text is not rendered while showSaveHint is false —
    // the views cycle flips the boolean; assert the absence so a
    // premature hint (promising a save path that does not exist)
    // cannot slip in unnoticed.
    expect(
      screen.queryByText(
        "There is no Views save path for this query on this page yet.",
      ),
    ).toBeNull();
  });
});
