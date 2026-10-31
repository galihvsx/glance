// @vitest-environment jsdom
// C95T2: PQL mode on the module detail list. Default mode is the
// dedicated /modules/{id}/issues membership list; PQL mode fetches
// /issues?module=<uuid>&pql=<query> for DISPLAY ONLY — progress
// counts keep the unfiltered membership list (plan decision 5).
// Invalid PQL leaves the list untouched (probe-first editor).

import { afterEach, describe, expect, it, vi } from "vitest";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import Modules from "./Modules";
import { api, ApiError } from "../lib/api";
import type { Issue, IssueState, Module } from "../lib/types";
import {
  makeQueryClient,
  stubMatchMedia,
} from "../components/shell/test-utils";

const BASE = "/api/v1/workspaces/acme/projects/ENG";
const MODULE_ID = "mod-1";

const states: IssueState[] = [
  { id: "st-open", project_id: "p-eng", name: "Todo", group: "unstarted", color: "#888", sequence: 1, created_at: "", updated_at: "" },
  { id: "st-started", project_id: "p-eng", name: "In Progress", group: "started", color: "#888", sequence: 2, created_at: "", updated_at: "" },
  { id: "st-done", project_id: "p-eng", name: "Done", group: "completed", color: "#888", sequence: 3, created_at: "", updated_at: "" },
];

function issue(id: string, display: string, name: string, stateId: string): Issue {
  return {
    id,
    project_id: "p-eng",
    sequence_id: 1,
    display_id: display,
    name,
    priority: 2,
    state_id: stateId,
    sort_order: 0,
    is_draft: false,
    created_by: "u-1",
    created_at: "",
    updated_at: "",
    assignees: [],
    labels: [],
  };
}

const membership = [
  issue("i-open", "ENG-1", "Open work", "st-open"),
  issue("i-open2", "ENG-4", "Second open work", "st-open"),
  issue("i-started", "ENG-2", "Started work", "st-started"),
  issue("i-done", "ENG-3", "Done work", "st-done"),
];

const moduleRow: Module = {
  id: MODULE_ID,
  project_id: "p-eng",
  name: "Auth module",
  status: "active",
  issue_count: 4,
  created_at: "",
  updated_at: "",
};

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

function renderPage(opts: { pqlError?: boolean } = {}) {
  stubMatchMedia(false);
  const pqlCalls: string[] = [];
  const get = vi.spyOn(api, "get").mockImplementation((path: string) => {
    if (path === "/api/v1/workspaces") return Promise.resolve({ workspaces: [] });
    if (path === "/api/v1/workspaces/acme") return Promise.resolve({ role: 5 });
    if (path === "/api/v1/workspaces/acme/members") return Promise.resolve({ members: [] });
    if (path === `${BASE}/states`) return Promise.resolve({ states });
    if (path === `${BASE}/modules`) return Promise.resolve({ modules: [moduleRow] });
    if (path === `${BASE}/modules/${MODULE_ID}/issues`) {
      return Promise.resolve({ issues: membership });
    }
    if (path.startsWith(`${BASE}/issues?`)) {
      pqlCalls.push(path);
      if (opts.pqlError) {
        return Promise.reject(
          new ApiError(400, "unknown field", "validation_error", { position: 1 }),
        );
      }
      // PQL fetch returns only the started issue (as if it matched).
      return Promise.resolve({ results: [membership.find((i) => i.id === "i-started")!], next_cursor: undefined });
    }
    return Promise.reject(new Error(`unexpected GET ${path}`));
  });
  const qc = makeQueryClient();
  render(
    <MemoryRouter initialEntries={["/w/acme/p/ENG/modules"]}>
      <QueryClientProvider client={qc}>
        <Routes>
          <Route path="/w/:slug/p/:identifier/modules" element={<Modules />} />
        </Routes>
      </QueryClientProvider>
    </MemoryRouter>,
  );
  return { get, pqlCalls };
}

async function openDetail() {
  fireEvent.click(await screen.findByRole("button", { name: "Open module Auth module" }));
  await screen.findByText("Issues in this module");
  await screen.findByText("Open work");
}

async function enterPqlMode() {
  fireEvent.click(screen.getByRole("button", { name: "PQL" }));
  await screen.findByLabelText("PQL query");
}

async function applyQuery(query: string) {
  fireEvent.change(screen.getByLabelText("PQL query"), { target: { value: query } });
  fireEvent.click(screen.getByRole("button", { name: "Apply" }));
}

describe("module detail PQL mode (C95T2)", () => {
  it("default mode shows the full membership list and progress counts", async () => {
    renderPage();
    await openDetail();
    expect(screen.getByText("Open work")).toBeTruthy();
    expect(screen.getByText("Started work")).toBeTruthy();
    expect(screen.getByText("Done work")).toBeTruthy();
    // Progress band (stat cells, via their unique notes): Open 2 /
    // In progress 1 / Done 1. ("Done" alone is ambiguous — the issue
    // row also renders its state name.)
    expect(screen.getByText("triaged, backlog, unstarted").parentElement?.textContent).toContain("2");
    expect(screen.getByText("started").parentElement?.textContent).toContain("1");
    expect(screen.getByText("completed").parentElement?.textContent).toContain("1");
  });

  it("PQL mode fetches /issues with module + pql and shows only the filtered list", async () => {
    const { pqlCalls } = renderPage();
    await openDetail();
    await enterPqlMode();
    await applyQuery("priority = High");
    await waitFor(() => {
      expect(screen.queryByText("Open work")).toBeNull();
      expect(screen.getByText("Started work")).toBeTruthy();
      expect(screen.queryByText("Done work")).toBeNull();
    });
    expect(pqlCalls).toHaveLength(1);
    const params = new URLSearchParams(pqlCalls[0].split("?")[1]);
    expect(params.get("module")).toBe(MODULE_ID);
    expect(params.get("pql")).toBe("priority = High");
  });

  it("progress counts stay on the unfiltered membership list in PQL mode", async () => {
    renderPage();
    await openDetail();
    await enterPqlMode();
    await applyQuery("priority = High");
    await waitFor(() => {
      expect(screen.queryByText("Open work")).toBeNull();
    });
    // The stat band still reports the full membership (Open 2,
    // In progress 1, Done 1) even though the filtered list holds
    // only the started issue — rewiring it to the filter would
    // show Open 0. The section count drops to the filtered list;
    // the progress band must not.
    expect(screen.getByText("PQL filter active — progress above counts the full module.")).toBeTruthy();
    const openCell = screen.getByText("Open").parentElement;
    expect(openCell?.textContent).toContain("2");
  });

  it("invalid PQL shows the editor error and leaves the list untouched", async () => {
    renderPage({ pqlError: true });
    await openDetail();
    await enterPqlMode();
    await applyQuery("bogus = 1");
    await screen.findByRole("alert");
    expect(screen.getByRole("alert").textContent).toContain("unknown field");
    // Full list intact, never blanked on error.
    expect(screen.getByText("Open work")).toBeTruthy();
    expect(screen.getByText("Started work")).toBeTruthy();
    expect(screen.getByText("Done work")).toBeTruthy();
  });

  it("leaving PQL mode restores the full list", async () => {
    renderPage();
    await openDetail();
    await enterPqlMode();
    await applyQuery("priority = High");
    await waitFor(() => {
      expect(screen.queryByText("Open work")).toBeNull();
    });
    fireEvent.click(screen.getByRole("button", { name: "All issues" }));
    await waitFor(() => {
      expect(screen.getByText("Open work")).toBeTruthy();
      expect(screen.getByText("Started work")).toBeTruthy();
      expect(screen.getByText("Done work")).toBeTruthy();
    });
    expect(screen.queryByLabelText("PQL query")).toBeNull();
  });
});
