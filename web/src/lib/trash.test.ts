// Tests for the trash view client (C52T2): the ?deleted=1 list shape,
// the single-restore POST shape, and the bulk-restore POST shape +
// {results} unwrap.
import { beforeEach, describe, expect, it, vi } from "vitest";
import {
  bulkRestoreIssues,
  fetchDeleted,
  restoreIssue,
  trashKey,
} from "./trash";
import { api } from "./api";
import type { Issue } from "./types";

vi.mock("./api", () => ({
  api: {
    get: vi.fn(),
    post: vi.fn(),
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

describe("fetchDeleted", () => {
  beforeEach(() => {
    vi.mocked(api.get).mockReset();
  });

  it("requests ?deleted=1 and returns the rows", async () => {
    vi.mocked(api.get).mockResolvedValue({
      results: [
        issue({ id: "d1", deleted_at: "2026-10-17T00:00:00Z" }),
      ],
    });
    const rows = await fetchDeleted("ws", "gl");
    expect(api.get).toHaveBeenCalledWith(
      "/api/v1/workspaces/ws/projects/gl/issues?deleted=1&per_page=100",
    );
    expect(rows.map((r) => r.id)).toEqual(["d1"]);
    expect(rows[0].deleted_at).toBe("2026-10-17T00:00:00Z");
  });

  it("returns an empty list when the backend has none", async () => {
    vi.mocked(api.get).mockResolvedValue({ results: [] });
    await expect(fetchDeleted("ws", "gl")).resolves.toEqual([]);
  });
});

describe("restoreIssue", () => {
  it("POSTs to the issue restore endpoint with no body", async () => {
    const spy = vi.spyOn(api, "post").mockResolvedValue({ id: "x" } as never);
    await restoreIssue("ws", "gl", "x");
    expect(spy).toHaveBeenCalledWith(
      "/api/v1/workspaces/ws/projects/gl/issues/x/restore",
    );
    spy.mockRestore();
  });
});

describe("bulkRestoreIssues", () => {
  it("POSTs {ids} to bulk-restore and unwraps {results}", async () => {
    const spy = vi.spyOn(api, "post").mockResolvedValue({
      results: [
        { id: "a", ok: true },
        { id: "b", ok: false, error: "issue not found" },
      ],
    } as never);
    const results = await bulkRestoreIssues("ws", "gl", ["a", "b"]);
    expect(spy).toHaveBeenCalledWith(
      "/api/v1/workspaces/ws/projects/gl/issues/bulk-restore",
      { ids: ["a", "b"] },
    );
    expect(results).toEqual([
      { id: "a", ok: true },
      { id: "b", ok: false, error: "issue not found" },
    ]);
    spy.mockRestore();
  });
});

describe("trashKey", () => {
  it("builds a scoped query key", () => {
    expect(trashKey("ws", "gl")).toEqual(["trash", "ws", "gl"]);
  });
});
