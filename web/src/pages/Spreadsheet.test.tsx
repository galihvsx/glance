// @vitest-environment jsdom
// C20T2: spreadsheet Cycle/Module/Release columns — chip rendering,
// membership wiring via ?include_project_structure=1, and sort behavior
// (empty cells sort last, matching the existing spreadsheet convention).
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";
import Spreadsheet from "./Spreadsheet";
import type { Issue, IssueProjectStructure } from "../lib/types";

function mkIssue(over: Partial<Issue> & { id: string }): Issue {
  return {
    project_id: "proj-1",
    sequence_id: 1,
    display_id: "ENG-1",
    name: "test",
    priority: 0,
    state_id: "s",
    sort_order: 0,
    is_draft: false,
    created_by: "u",
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    assignees: [],
    labels: [],
    ...over,
  };
}

const issues = [
  mkIssue({ id: "i1", sequence_id: 1, display_id: "ENG-1", name: "First" }),
  mkIssue({ id: "i2", sequence_id: 2, display_id: "ENG-2", name: "Second" }),
  mkIssue({ id: "i3", sequence_id: 3, display_id: "ENG-3", name: "Third" }),
];

const structures: Record<string, IssueProjectStructure> = {
  i1: {
    cycle: { id: "c1", name: "Sprint 1" },
    modules: [
      { id: "m1", name: "Auth" },
      { id: "m2", name: "API" },
    ],
    release: { id: "r1", name: "v0.2" },
  },
  i2: {
    cycle: { id: "c2", name: "Sprint 2" },
    modules: [],
    release: null,
  },
  // i3: no membership at all — cycle/release omitted, modules empty.
  i3: { modules: [] },
};

const BASE = "/api/v1/workspaces/acme/projects/ENG";

vi.mock("../lib/api", () => ({
  api: {
    get: vi.fn((path: string) => {
      // C20T2 per-row membership fetch — must precede the generic /issues
      // branch below.
      if (path.includes("include_project_structure=1")) {
        const id = path.split("/issues/")[1].split("?")[0];
        return Promise.resolve({ project_structure: structures[id] });
      }
      if (path.includes("/states")) {
        return Promise.resolve({ states: [] });
      }
      if (path.includes("/members")) {
        return Promise.resolve({ members: [] });
      }
      if (path.includes("/labels")) {
        return Promise.resolve({ labels: [] });
      }
      if (path.includes("/estimates")) {
        return Promise.resolve({ estimates: [] });
      }
      if (path.includes("/custom-fields")) {
        return Promise.resolve({ custom_fields: [] });
      }
      if (path.includes("/views")) {
        return Promise.resolve({ views: [] });
      }
      if (path.includes("/issues")) {
        return Promise.resolve({ results: issues, next_cursor: undefined });
      }
      if (path === BASE) {
        return Promise.resolve({
          id: "proj-1",
          identifier: "ENG",
          name: "Eng",
        });
      }
      return Promise.reject(new Error("unexpected path: " + path));
    }),
    post: vi.fn(),
    patch: vi.fn(),
    del: vi.fn(),
  },
  ApiError: class ApiError extends Error {},
}));

vi.mock("../lib/auth", () => ({
  useAuth: () => ({
    user: { id: "u1", name: "U", email: "u@example.com" },
    loading: false,
  }),
}));

function renderSpreadsheet() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return render(
    <MemoryRouter initialEntries={["/w/acme/p/ENG/spreadsheet"]}>
      <QueryClientProvider client={client}>
        <Routes>
          <Route
            path="/w/:slug/p/:identifier/spreadsheet"
            element={<Spreadsheet />}
          />
        </Routes>
      </QueryClientProvider>
    </MemoryRouter>,
  );
}

function rowLabels(): string[] {
  const table = screen.getByRole("table");
  return within(table)
    .getAllByRole("link")
    .map((row) => row.getAttribute("aria-label") ?? "");
}

beforeEach(() => {
  localStorage.clear();
  vi.clearAllMocks();
});

describe("Spreadsheet Cycle/Module/Release columns", () => {
  it("renders Cycle, Module, and Release column headers", async () => {
    renderSpreadsheet();
    expect(
      await screen.findByRole("button", { name: "Sort by Cycle" }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "Sort by Module" }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "Sort by Release" }),
    ).toBeInTheDocument();
  });

  it("fetches per-row membership via ?include_project_structure=1", async () => {
    const { container } = renderSpreadsheet();
    await screen.findByRole("button", { name: "Sort by Cycle" });
    // Wait for the membership chips to render before asserting on calls.
    await screen.findByTitle("Sprint 1");
    const { api } = await import("../lib/api");
    const getMock = api.get as ReturnType<typeof vi.fn>;
    for (const id of ["i1", "i2", "i3"]) {
      expect(getMock).toHaveBeenCalledWith(
        expect.stringContaining(
          `/issues/${id}?include_project_structure=1`,
        ),
      );
    }
    void container;
  });

  it("renders cycle/module/release chips; empty cells show an em dash", async () => {
    renderSpreadsheet();
    await screen.findByTitle("Sprint 1");

    // Cycle chip + release chip on the ENG-1 row.
    const row1 = screen.getByRole("link", { name: /Open issue ENG-1/ });
    expect(within(row1).getByTitle("Sprint 1")).toHaveTextContent("Sprint 1");
    expect(within(row1).getByTitle("v0.2")).toHaveTextContent("v0.2");

    // Modules: one chip per module, comma-separated.
    expect(within(row1).getByTitle("Auth")).toHaveTextContent("Auth");
    expect(within(row1).getByTitle("API")).toHaveTextContent("API");
    const moduleCell = within(row1).getByTitle("Auth").parentElement!
      .parentElement!;
    expect(moduleCell.textContent).toContain(",");

    // ENG-3 has no membership — all three cells are em dashes.
    const row3 = screen.getByRole("link", { name: /Open issue ENG-3/ });
    const dashes = within(row3).getAllByText("—");
    expect(dashes.length).toBeGreaterThanOrEqual(3);
  });

  it("sorts by cycle with empty cells last in both directions", async () => {
    renderSpreadsheet();
    await screen.findByTitle("Sprint 1");
    const user = userEvent.setup();

    await user.click(screen.getByRole("button", { name: "Sort by Cycle" }));
    let labels = rowLabels();
    expect(labels[0]).toMatch(/ENG-1/);
    expect(labels[1]).toMatch(/ENG-2/);
    expect(labels[2]).toMatch(/ENG-3/); // no cycle → last

    await user.click(screen.getByRole("button", { name: "Sort by Cycle" }));
    labels = rowLabels();
    expect(labels[0]).toMatch(/ENG-2/);
    expect(labels[1]).toMatch(/ENG-1/);
    expect(labels[2]).toMatch(/ENG-3/); // no cycle → still last
  });

  it("sorts by module and release with empty cells last", async () => {
    renderSpreadsheet();
    await screen.findByTitle("Sprint 1");
    const user = userEvent.setup();

    // Only ENG-1 has modules: "API,Auth" joined.
    await user.click(screen.getByRole("button", { name: "Sort by Module" }));
    let labels = rowLabels();
    expect(labels[0]).toMatch(/ENG-1/);
    // ENG-2 and ENG-3 are both empty → last, stable relative order.
    expect(labels.slice(1).join(" ")).toMatch(/ENG-2.*ENG-3/);

    // Only ENG-1 has a release.
    await user.click(screen.getByRole("button", { name: "Sort by Release" }));
    labels = rowLabels();
    expect(labels[0]).toMatch(/ENG-1/);
    expect(labels.slice(1).join(" ")).toMatch(/ENG-2.*ENG-3/);
  });
});
