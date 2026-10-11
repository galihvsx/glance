// @vitest-environment jsdom
import { cleanup, render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";
import RecentActivitySection, {
  fetchRecentActivity,
} from "./RecentActivitySection";
import type { ActivityEntry } from "../../lib/activity";
import type { Workspace } from "../../lib/types";

vi.mock("../../lib/api", () => ({
  api: {
    get: vi.fn((path: string) => {
      if (path.includes("/activity")) {
        return Promise.resolve({ activity: mockFeed });
      }
      if (path.includes("/states")) {
        return Promise.resolve({ states: [] });
      }
      return Promise.reject(new Error("unexpected path: " + path));
    }),
  },
}));

const mockFeed: ActivityEntry[] = [
  {
    at: "2026-10-11T10:00:00Z",
    actor: "Ada Member",
    issue_uuid: "uuid-b",
    issue_identifier: "DES-2",
    field: "priority",
    old: "0",
    new: "2",
    project_identifier: "DES",
    project_name: "Design",
  },
  {
    at: "2026-10-11T09:00:00Z",
    actor: "Ada Member",
    issue_uuid: "uuid-a",
    issue_identifier: "ENG-1",
    field: "_created",
    old: null,
    new: '{"name":"First"}',
    project_identifier: "ENG",
    project_name: "Eng",
  },
];

const workspaces = [{ slug: "acme" } as Workspace];

function renderSection(ws: Workspace[] | undefined) {
  return render(
    <MemoryRouter>
      <QueryClientProvider client={new QueryClient()}>
        <RecentActivitySection workspaces={ws} />
      </QueryClientProvider>
    </MemoryRouter>,
  );
}

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

describe("fetchRecentActivity", () => {
  it("merges workspaces newest-first and tags the workspace slug", async () => {
    const rows = await fetchRecentActivity(workspaces);
    expect(rows).toHaveLength(2);
    // Newest first.
    expect(rows[0].issue_identifier).toBe("DES-2");
    expect(rows[1].issue_identifier).toBe("ENG-1");
    // Slug tagged for issue links.
    expect(rows[0].slug).toBe("acme");
  });

  it("caps the merged feed at 8 rows", async () => {
    const many = Array.from({ length: 20 }, (_, i) => ({
      ...mockFeed[0],
      at: `2026-10-11T10:${String(i).padStart(2, "0")}:00Z`,
      issue_uuid: `uuid-${i}`,
    }));
    const { api } = await import("../../lib/api");
    vi.mocked(api.get).mockResolvedValueOnce({ activity: many });
    const rows = await fetchRecentActivity(workspaces);
    expect(rows).toHaveLength(8);
  });
});

describe("RecentActivitySection", () => {
  it("renders rows with project badges and issue links", async () => {
    renderSection(workspaces);
    expect(await screen.findByText("Recent activity")).toBeTruthy();
    // Project badges per row.
    expect(await screen.findByText("Design")).toBeTruthy();
    expect(screen.getByText("Eng")).toBeTruthy();
    // Issue identifier links to the issue page.
    const link = screen.getByText("DES-2").closest("a");
    expect(link?.getAttribute("href")).toBe("/w/acme/p/DES/i/uuid-b");
    // Human-readable change text.
    expect(screen.getByText(/changed/i)).toBeTruthy();
  });

  it("renders the empty state when the feed is empty", async () => {
    const { api } = await import("../../lib/api");
    vi.mocked(api.get).mockImplementation((path: string) => {
      if (path.includes("/activity")) return Promise.resolve({ activity: [] });
      return Promise.resolve({ states: [] });
    });
    renderSection(workspaces);
    expect(await screen.findByText("No recent activity")).toBeTruthy();
  });

  it("renders nothing without workspaces", () => {
    const { container } = renderSection(undefined);
    expect(container.innerHTML).toBe("");
  });
});
