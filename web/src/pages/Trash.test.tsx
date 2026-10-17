// @vitest-environment jsdom
// Rendering tests for the Trash page (C52T2): empty state, titles as
// plain text (NOT links — a still-deleted row's detail route 404s),
// per-row Restore calling the client, bulk selection + Restore
// selected, and the client-side search filter.
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import Trash from "./Trash";
import type { Issue } from "../lib/types";

const mockTrash = vi.hoisted(() => ({
  fetchDeleted: vi.fn(),
  restoreIssue: vi.fn(),
  bulkRestoreIssues: vi.fn(),
}));

vi.mock("../lib/trash", () => ({
  trashKey: (slug: string, identifier: string) => ["trash", slug, identifier],
  fetchDeleted: mockTrash.fetchDeleted,
  restoreIssue: mockTrash.restoreIssue,
  bulkRestoreIssues: mockTrash.bulkRestoreIssues,
}));

// PageHeader reads workspaces via useWorkspaces; stub it so the test
// never touches the network.
vi.mock("../lib/useWorkspaces", () => ({
  workspacesKey: ["workspaces"],
  useWorkspaces: () => ({ data: [] }),
  useSeedWorkspaces: () => () => {},
}));

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

function deletedIssue(partial: Partial<Issue> & { id: string }): Issue {
  return {
    project_id: "proj-1",
    sequence_id: 1,
    display_id: "ENG-1",
    name: "Deleted issue",
    priority: 0,
    state_id: "s1",
    sort_order: 0,
    is_draft: false,
    created_by: "u1",
    created_at: "2026-10-16T00:00:00Z",
    updated_at: "2026-10-16T00:00:00Z",
    assignees: [],
    labels: [],
    deleted_at: "2026-10-17T00:00:00Z",
    ...partial,
  } as Issue;
}

function renderTrash() {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={["/w/acme/p/ENG/trash"]}>
        <Routes>
          <Route
            path="/w/:slug/p/:identifier/trash"
            element={<Trash />}
          />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  );
  return qc;
}

describe("Trash page", () => {
  beforeEach(() => {
    mockTrash.restoreIssue.mockResolvedValue({} as Issue);
    mockTrash.bulkRestoreIssues.mockResolvedValue([]);
  });

  it("renders the empty state when trash is empty", async () => {
    mockTrash.fetchDeleted.mockResolvedValue([]);
    renderTrash();
    expect(await screen.findByText("Trash is empty")).toBeInTheDocument();
    expect(mockTrash.fetchDeleted).toHaveBeenCalledWith("acme", "ENG");
  });

  it("renders deleted titles as plain text, not links", async () => {
    mockTrash.fetchDeleted.mockResolvedValue([
      deletedIssue({ id: "d1", display_id: "ENG-1", name: "First gone" }),
    ]);
    renderTrash();
    expect(await screen.findByText("First gone")).toBeInTheDocument();
    expect(
      screen.queryByRole("link", { name: "First gone" }),
    ).not.toBeInTheDocument();
  });

  it("per-row Restore calls restoreIssue with slug, identifier, id", async () => {
    mockTrash.fetchDeleted.mockResolvedValue([
      deletedIssue({ id: "d1", display_id: "ENG-1", name: "First gone" }),
    ]);
    renderTrash();
    fireEvent.click(await screen.findByRole("button", { name: "Restore" }));
    await waitFor(() => {
      expect(mockTrash.restoreIssue).toHaveBeenCalledWith("acme", "ENG", "d1");
    });
  });

  it("bulk selection + Restore selected calls bulkRestoreIssues", async () => {
    mockTrash.fetchDeleted.mockResolvedValue([
      deletedIssue({ id: "d1", display_id: "ENG-1", name: "First gone" }),
      deletedIssue({ id: "d2", display_id: "ENG-2", name: "Second gone" }),
    ]);
    mockTrash.bulkRestoreIssues.mockResolvedValue([{ id: "d1", ok: true }]);
    renderTrash();
    await screen.findByText("First gone");
    fireEvent.click(
      screen.getByRole("checkbox", {
        name: "Select ENG-1 for bulk restore",
      }),
    );
    fireEvent.click(
      screen.getByRole("button", { name: /Restore selected/ }),
    );
    await waitFor(() => {
      expect(mockTrash.bulkRestoreIssues).toHaveBeenCalledWith("acme", "ENG", [
        "d1",
      ]);
    });
  });

  it("search filters rows client-side by name", async () => {
    mockTrash.fetchDeleted.mockResolvedValue([
      deletedIssue({ id: "d1", display_id: "ENG-1", name: "Alpha gone" }),
      deletedIssue({ id: "d2", display_id: "ENG-2", name: "Beta gone" }),
    ]);
    renderTrash();
    await screen.findByText("Alpha gone");
    fireEvent.change(screen.getByLabelText("Search deleted issues"), {
      target: { value: "beta" },
    });
    expect(screen.queryByText("Alpha gone")).not.toBeInTheDocument();
    expect(screen.getByText("Beta gone")).toBeInTheDocument();
  });
});
