// Tests for the archived view client (C7T5). The only logic here is the
// client-side "archived only" filter: ?archived=1 *includes* archived rows
// rather than selecting them, so fetchArchived must drop active rows — plus
// the archive/unarchive PATCH shapes.
import { beforeEach, describe, expect, it, vi } from "vitest";
import { archivedKey, fetchArchived, isArchived, unarchiveIssue } from "./archived";
import { api } from "./api";
import type { Issue } from "./types";

vi.mock("./api", () => ({
  api: {
    get: vi.fn(),
    patch: vi.fn(),
    del: vi.fn(),
  },
}));

function issue(partial: Partial<Issue> & { id: string }): Issue {
  return {
    project_id: "proj-1",
    display_id: "G-1",
    name: "t",
    priority: 0,
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

describe("isArchived", () => {
  it("treats a set archived_at as archived", () => {
    expect(
      isArchived(issue({ id: "a", archived_at: "2026-10-08T00:00:00Z" })),
    ).toBe(true);
  });

  it("treats null and missing archived_at as active", () => {
    expect(isArchived(issue({ id: "b", archived_at: null }))).toBe(false);
    expect(isArchived(issue({ id: "c" }))).toBe(false);
  });
});

describe("fetchArchived", () => {
  beforeEach(() => {
    vi.mocked(api.get).mockReset();
  });

  it("requests ?archived=1 and keeps only archived rows", async () => {
    vi.mocked(api.get).mockResolvedValue({
      results: [
        issue({ id: "active", archived_at: null }),
        issue({ id: "old", archived_at: "2026-10-01T00:00:00Z" }),
      ],
    });
    const rows = await fetchArchived("ws", "gl");
    expect(api.get).toHaveBeenCalledWith(
      expect.stringContaining("/issues?archived=1"),
    );
    expect(rows.map((r) => r.id)).toEqual(["old"]);
  });

  it("returns an empty list when the backend has none", async () => {
    vi.mocked(api.get).mockResolvedValue({ results: [] });
    await expect(fetchArchived("ws", "gl")).resolves.toEqual([]);
  });
});

describe("unarchiveIssue", () => {
  it("PATCHes {archived: false} to the issue endpoint", async () => {
    const { api } = await import("./api");
    const spy = vi.spyOn(api, "patch").mockResolvedValue({ id: "x" } as never);
    await unarchiveIssue("ws", "gl", "x");
    expect(spy).toHaveBeenCalledWith(
      "/api/v1/workspaces/ws/projects/gl/issues/x",
      { archived: false },
    );
    spy.mockRestore();
  });
});

describe("archiveIssue", () => {
  it("PATCHes {archived: true} to the issue endpoint", async () => {
    const { api } = await import("./api");
    const { archiveIssue } = await import("./archived");
    const spy = vi.spyOn(api, "patch").mockResolvedValue({ id: "x" } as never);
    await archiveIssue("ws", "gl", "x");
    expect(spy).toHaveBeenCalledWith(
      "/api/v1/workspaces/ws/projects/gl/issues/x",
      { archived: true },
    );
    spy.mockRestore();
  });
});

describe("archivedKey", () => {
  it("builds a scoped query key", () => {
    expect(archivedKey("ws", "gl")).toEqual(["archived", "ws", "gl"]);
  });
});
