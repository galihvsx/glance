// @vitest-environment jsdom
// PQL mode on Drafts (C111T2): the Cycles-detail shape brought to
// the drafts list — "All drafts" / "PQL" toggle + PqlEditor above
// the list, probe-first apply through ?draft=true&pql=...&
// per_page=100, display-only filtering. Default mode is byte-for-byte
// today's behavior (the unfiltered draft list). No
// client-side filter is needed or applied: draft mode selects
// is_draft = TRUE server-side, so a PQL query on Drafts never
// leaks published issues into the view — the mock models that
// server-side selection (the draft endpoint never returns the
// published issue, whatever the predicate). C111T4 wired the
// detail-mode Views menu beside the toggle, so the editor passes
// showSaveHint={true} with the corrected hint text.

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
// C111T4: Drafts now mounts the detail-mode SavedViewsMenu, whose
// useSavedViews hook reads useAuth; the real provider needs a
// session — stub the auth context (GanttPql.test.tsx pattern).
// The menu itself is untested here (projectId stays "" — this
// file's mock has no project endpoint — so its views query stays
// disabled); DraftsSavedViews.test.tsx covers the menu path.
vi.mock("../lib/auth", () => ({
  useAuth: () => ({ user: { id: "u-1", email: "u@x.com" }, loading: false }),
}));
import Drafts from "./Drafts";
import { api, ApiError } from "../lib/api";
import type { Issue } from "../lib/types";
import {
  makeQueryClient,
  stubMatchMedia,
} from "../components/shell/test-utils";

const BASE = "/api/v1/workspaces/acme/projects/ENG";

function makeIssue(
  id: string,
  displayId: string,
  name: string,
  isDraft: boolean,
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
    is_draft: isDraft,
    created_by: "u-1",
    created_at: "2026-11-01T00:00:00Z",
    updated_at: "2026-11-01T00:00:00Z",
    assignees: [],
    labels: [],
    target_date: null,
  };
}

const draftA = makeIssue("i-a", "ENG-1", "Draft alpha", true);
const draftB = makeIssue("i-b", "ENG-2", "Draft beta", true);
const published = makeIssue("i-c", "ENG-3", "Published gamma", false);

function renderPage() {
  // Published drafts leave the draft set on publish; the mock
  // models the server-side is_draft selection from that point on.
  const publishedIds = new Set<string>();
  const get = vi.spyOn(api, "get").mockImplementation((path: string) => {
    if (path === "/api/v1/workspaces")
      return Promise.resolve({ workspaces: [] });
    if (path.startsWith(`${BASE}/issues`)) {
      const qs = new URLSearchParams(path.split("?")[1] ?? "");
      if (qs.get("draft") !== "true") {
        // The page never issues a non-draft call; if it ever did,
        // the published issue would be reachable — fail loudly.
        return Promise.resolve({ results: [published], next_cursor: null });
      }
      const liveDrafts = [draftA, draftB].filter(
        (d) => !publishedIds.has(d.id),
      );
      const pql = qs.get("pql");
      if (pql !== null) {
        if (pql.includes("bogus")) {
          return Promise.reject(
            new ApiError(400, "unknown field: bogus", "validation_error", {
              position: 1,
            }),
          );
        }
        if (pql.includes("beta")) {
          return Promise.resolve({
            results: liveDrafts.filter((d) => d.id === "i-b"),
            next_cursor: null,
          });
        }
        if (pql.includes("alpha")) {
          return Promise.resolve({
            results: liveDrafts.filter((d) => d.id === "i-a"),
            next_cursor: null,
          });
        }
        return Promise.resolve({ results: liveDrafts, next_cursor: null });
      }
      return Promise.resolve({ results: liveDrafts, next_cursor: null });
    }
    return Promise.reject(new Error(`unexpected GET ${path}`));
  });
  const patch = vi.spyOn(api, "patch").mockImplementation((path: string) => {
    const id = path.split("/").pop() ?? "";
    publishedIds.add(id);
    const issue = [draftA, draftB].find((d) => d.id === id);
    return Promise.resolve({ ...issue, is_draft: false });
  });
  const qc = makeQueryClient();
  render(
    <MemoryRouter initialEntries={["/w/acme/p/ENG/drafts"]}>
      <QueryClientProvider client={qc}>
        <Routes>
          <Route path="/w/:slug/p/:identifier/drafts" element={<Drafts />} />
        </Routes>
      </QueryClientProvider>
    </MemoryRouter>,
  );
  return { get, patch };
}

async function waitForDrafts() {
  await screen.findByText("Draft alpha");
  await screen.findByText("Draft beta");
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

describe("drafts PQL mode (C111T2)", () => {
  it("default mode shows the full draft set with no PQL editor, and no pql param", async () => {
    const { get } = renderPage();
    await waitForDrafts();
    expect(screen.queryByLabelText("PQL query")).toBeNull();
    expect(
      screen.getByRole("button", { name: "All drafts" }),
    ).toHaveAttribute("aria-pressed", "true");
    const calls = issueCalls(get);
    expect(calls.length).toBeGreaterThan(0);
    for (const qs of calls) {
      expect(qs.get("pql")).toBeNull();
      expect(qs.get("draft")).toBe("true");
    }
  });

  it("applying a PQL query enters PQL mode with the predicate applied and published issues never shown", async () => {
    const { get } = renderPage();
    await waitForDrafts();
    await applyPql("name ~ beta");
    await waitFor(() => {
      expect(screen.queryByText("Draft alpha")).toBeNull();
    });
    expect(screen.getByText("Draft beta")).toBeTruthy();
    expect(screen.queryByText("Published gamma")).toBeNull();
    // The editor shows the applied query.
    expect(
      (screen.getByLabelText("PQL query") as HTMLTextAreaElement).value,
    ).toBe("name ~ beta");
    // Every PQL fetch carries draft=true + pql + per_page=100, so
    // the server-side draft selection is what keeps published
    // issues out — no client-side filter is involved.
    const pqlCalls = issueCalls(get).filter((qs) => qs.get("pql") !== null);
    expect(pqlCalls.length).toBeGreaterThan(0);
    for (const qs of pqlCalls) {
      expect(qs.get("draft")).toBe("true");
      expect(qs.get("pql")).toBe("name ~ beta");
      expect(qs.get("per_page")).toBe("100");
    }
  });

  it("a failing probe surfaces inline with the displayed set untouched", async () => {
    renderPage();
    await waitForDrafts();
    await applyPql("bogus = 1");
    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain("unknown field: bogus");
    expect(alert.textContent).toContain("at column 1");
    // Set untouched: both drafts still on screen.
    await waitForDrafts();
  });

  it("leaving PQL mode restores the full draft set and closes the editor", async () => {
    renderPage();
    await waitForDrafts();
    await applyPql("name ~ beta");
    await waitFor(() => {
      expect(screen.queryByText("Draft alpha")).toBeNull();
    });
    fireEvent.click(screen.getByRole("button", { name: "All drafts" }));
    await waitForDrafts();
    expect(screen.queryByLabelText("PQL query")).toBeNull();
    expect(
      screen.getByRole("button", { name: "All drafts" }),
    ).toHaveAttribute("aria-pressed", "true");
  });

  it("publishing from a PQL-filtered list re-runs the list in place", async () => {
    const { get, patch } = renderPage();
    await waitForDrafts();
    await applyPql("name ~ beta");
    await waitFor(() => {
      expect(screen.queryByText("Draft alpha")).toBeNull();
    });
    const pqlCallsBefore = issueCalls(get).filter(
      (qs) => qs.get("pql") !== null,
    ).length;
    fireEvent.click(screen.getByRole("button", { name: "Publish" }));
    await waitFor(() => {
      expect(patch).toHaveBeenCalled();
    });
    // Still in PQL mode, and the PQL-mode list re-ran: the
    // published draft is gone from the filtered set, which is now
    // an empty PQL result (not the full draft set).
    await screen.findByText("No drafts match this PQL query");
    expect(screen.getByLabelText("PQL query")).toBeTruthy();
    expect(screen.queryByText("Draft alpha")).toBeNull();
    const pqlCallsAfter = issueCalls(get).filter(
      (qs) => qs.get("pql") !== null,
    ).length;
    expect(pqlCallsAfter).toBeGreaterThan(pqlCallsBefore);
  });

  it("the PQL editor shows the corrected save hint — the detail-mode menu is now the save path", async () => {
    renderPage();
    await waitForDrafts();
    fireEvent.click(screen.getByRole("button", { name: "PQL" }));
    await screen.findByLabelText("PQL query");
    // C111T4: the detail-mode Views menu beside the toggle is now
    // the save path, so the corrected save hint is shown.
    expect(
      screen.getByText(
        "To save this query, use Views → Save current view next to the All drafts / PQL toggle; it re-applies when the view is opened.",
      ),
    ).toBeTruthy();
  });
});
