// @vitest-environment jsdom
// Tests for VersionHistory (C53T1): newest-first list with chips,
// expand → diff table (state names, priority labels, parent display id
// vs truncated UUID, description "changed" with no preview), restore
// button gating (writers only, never on the latest version), confirm →
// restore with the right n, 403 permission message, 404 list refresh,
// success invalidation (versions refetch), empty state.
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, describe, expect, it, vi } from "vitest";
import VersionHistory from "./VersionHistory";
import { ApiError, api } from "../../lib/api";
import type { Issue, IssueState } from "../../lib/types";
import type { IssueVersion } from "../../lib/versions";

vi.mock("../../lib/api", async (importOriginal) => {
  const mod = await importOriginal<typeof import("../../lib/api")>();
  return {
    ...mod,
    api: { get: vi.fn(), post: vi.fn(), patch: vi.fn(), del: vi.fn() },
  };
});

const mockApiGet = vi.mocked(api.get);
const mockApiPost = vi.mocked(api.post);

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

const doc = (text: string) => ({
  type: "doc",
  content: [{ type: "paragraph", content: [{ type: "text", text }] }],
});

function issue(partial: Partial<Issue> & { id: string }): Issue {
  return {
    project_id: "proj-1",
    sequence_id: 1,
    display_id: "ENG-1",
    name: "Alpha",
    priority: 1,
    state_id: "s1",
    sort_order: 0,
    is_draft: false,
    created_by: "u1",
    created_at: "2026-10-09T00:00:00Z",
    updated_at: "2026-10-09T00:00:00Z",
    assignees: [],
    labels: [],
    ...partial,
  } as Issue;
}

const KNOWN_PARENT = "aaaaaaaa-1111-2222-3333-444444444444";
const UNKNOWN_PARENT = "bbbbbbbb-5555-6666-7777-888888888888";

function version(version_no: number, snapshot: Issue): IssueVersion {
  return {
    version_no,
    snapshot,
    created_by: { id: "u1", name: "Ada", email: "ada@example.com" },
    created_at: "2026-10-10T00:00:00Z",
  };
}

// Oldest-first (the API order): v1 → v2 → v3 (latest).
const versions: IssueVersion[] = [
  version(
    1,
    issue({ id: "iss-1", description: doc("first"), parent_id: null }),
  ),
  version(
    2,
    issue({
      id: "iss-1",
      name: "Beta",
      description: doc("first"),
      parent_id: KNOWN_PARENT,
    }),
  ),
  version(
    3,
    issue({
      id: "iss-1",
      name: "Beta",
      state_id: "s2",
      priority: 3,
      description: doc("second"),
      parent_id: UNKNOWN_PARENT,
    }),
  ),
];

const states: IssueState[] = [
  {
    id: "s1",
    project_id: "proj-1",
    name: "Backlog",
    group: "backlog",
    color: "#888",
    sequence: 0,
    created_at: "2026-10-09T00:00:00Z",
    updated_at: "2026-10-09T00:00:00Z",
  },
  {
    id: "s2",
    project_id: "proj-1",
    name: "In Progress",
    group: "started",
    color: "#00f",
    sequence: 1,
    created_at: "2026-10-09T00:00:00Z",
    updated_at: "2026-10-09T00:00:00Z",
  },
];

const parentDisplayIds = new Map([[KNOWN_PARENT, "ENG-9"]]);

let role = 20;

function installGet(versionsOverride?: IssueVersion[]) {
  mockApiGet.mockImplementation((path: string) => {
    if (path.includes("/versions")) {
      return Promise.resolve({ versions: versionsOverride ?? versions });
    }
    if (path === "/api/v1/workspaces/acme") {
      return Promise.resolve({ role });
    }
    return Promise.reject(new Error("unexpected path: " + path));
  });
}

function renderHistory() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  render(
    <QueryClientProvider client={client}>
      <VersionHistory
        slug="acme"
        identifier="ENG"
        uuid="iss-1"
        states={states}
        parentDisplayIds={parentDisplayIds}
      />
    </QueryClientProvider>,
  );
  return client;
}

function versionsGetCount(): number {
  return mockApiGet.mock.calls.filter((c) =>
    String(c[0]).includes("/versions"),
  ).length;
}

describe("VersionHistory", () => {
  it("renders newest-first with changed-field chips; v1 shows a Created chip", async () => {
    role = 20;
    installGet();
    renderHistory();
    await screen.findByText("v3");
    const rows = screen.getAllByText(/v[123]/);
    expect(rows.map((r) => r.textContent)).toEqual(["v3", "v2", "v1"]);
    // v3 changed State, Priority, Parent, Description vs v2.
    expect(screen.getAllByText("State")).toHaveLength(1);
    expect(screen.getAllByText("Priority")).toHaveLength(1);
    // v2 changed Name + Parent vs v1.
    expect(screen.getAllByText("Name")).toHaveLength(1);
    expect(screen.getAllByText("Parent")).toHaveLength(2);
    expect(screen.getByText("Created")).toBeTruthy();
  });

  it("expanding a row shows the diff table with resolved names and no description preview", async () => {
    role = 20;
    installGet();
    renderHistory();
    await screen.findByText("v3");
    fireEvent.click(screen.getAllByRole("button", { name: "Show diff" })[0]);
    // State resolves via the already-loaded states; priority via the label.
    expect(screen.getByText("Backlog")).toBeTruthy();
    expect(screen.getByText("In Progress")).toBeTruthy();
    expect(screen.getByText("Low")).toBeTruthy();
    expect(screen.getByText("High")).toBeTruthy();
    // Unknown parent renders as a truncated UUID, never a fetch.
    expect(screen.getByText("bbbbbbbb…")).toBeTruthy();
    // Description reports "changed" with no content preview.
    expect(screen.getAllByText("changed")).toHaveLength(1);
    expect(screen.queryByText("first")).toBeNull();
    expect(screen.queryByText("second")).toBeNull();
    expect(
      screen.getByText(
        "Labels, assignees and custom values are not part of version snapshots.",
      ),
    ).toBeTruthy();
  });

  it("expanding v2 shows the known parent display id", async () => {
    role = 20;
    installGet();
    renderHistory();
    await screen.findByText("v3");
    fireEvent.click(screen.getAllByRole("button", { name: "Show diff" })[1]);
    expect(screen.getByText("ENG-9")).toBeTruthy();
  });

  it("shows Restore on older versions only, and only for writers", async () => {
    role = 20;
    installGet();
    renderHistory();
    await screen.findByText("v3");
    // v2 + v1 get a Restore button; the latest (v3) never does.
    expect(screen.getAllByRole("button", { name: "Restore" })).toHaveLength(2);
  });

  it("hides every Restore button for role < 15", async () => {
    role = 0;
    installGet();
    renderHistory();
    await screen.findByText("v3");
    expect(screen.queryByRole("button", { name: "Restore" })).toBeNull();
  });

  it("confirm → calls restore with the right version number", async () => {
    role = 20;
    installGet();
    mockApiPost.mockResolvedValue({} as Issue);
    renderHistory();
    await screen.findByText("v3");
    // Restore buttons are newest-first: [v2, v1] → click the v1 button.
    fireEvent.click(screen.getAllByRole("button", { name: "Restore" })[1]);
    const dialog = await screen.findByRole("alertdialog");
    expect(
      within(dialog).getByText("Restore version 1?"),
    ).toBeTruthy();
    expect(
      within(dialog).getByText(/history is never rewritten/),
    ).toBeTruthy();
    fireEvent.click(within(dialog).getByRole("button", { name: "Restore" }));
    await waitFor(() =>
      expect(mockApiPost).toHaveBeenCalledWith(
        "/api/v1/workspaces/acme/projects/ENG/issues/iss-1/versions/1/restore",
      ),
    );
  });

  it("restore success shows a message and refetches the versions list", async () => {
    role = 20;
    installGet();
    mockApiPost.mockResolvedValue({} as Issue);
    renderHistory();
    await screen.findByText("v3");
    const before = versionsGetCount();
    fireEvent.click(screen.getAllByRole("button", { name: "Restore" })[1]);
    const dialog = await screen.findByRole("alertdialog");
    fireEvent.click(within(dialog).getByRole("button", { name: "Restore" }));
    await screen.findByText(/Version restored/);
    await waitFor(() => expect(versionsGetCount()).toBeGreaterThan(before));
  });

  it("403 shows the permission message and nothing else", async () => {
    role = 20;
    installGet();
    mockApiPost.mockRejectedValue(new ApiError(403, "Forbidden"));
    renderHistory();
    await screen.findByText("v3");
    fireEvent.click(screen.getAllByRole("button", { name: "Restore" })[1]);
    const dialog = await screen.findByRole("alertdialog");
    fireEvent.click(within(dialog).getByRole("button", { name: "Restore" }));
    await screen.findByText("You don't have permission to restore versions");
  });

  it("404 refetches the list", async () => {
    role = 20;
    installGet();
    mockApiPost.mockRejectedValue(new ApiError(404, "Not found"));
    renderHistory();
    await screen.findByText("v3");
    const before = versionsGetCount();
    fireEvent.click(screen.getAllByRole("button", { name: "Restore" })[1]);
    const dialog = await screen.findByRole("alertdialog");
    fireEvent.click(within(dialog).getByRole("button", { name: "Restore" }));
    await screen.findByText(/no longer exists/);
    await waitFor(() => expect(versionsGetCount()).toBeGreaterThan(before));
  });

  it("renders an empty state when there are no versions", async () => {
    role = 20;
    installGet([]);
    renderHistory();
    await screen.findByText("No versions yet.");
  });
});
