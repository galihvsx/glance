// Tests for the versions client + snapshot diff helper (C53T0): the
// three envelope shapes (list/get/restore) and the diff-field rules —
// exactly-the-changed-fields, updated_at-only / subtask_progress-only /
// deleted_at-only -> zero changes, v1 created marker, description
// key-reorder -> no change, description content change -> one change.
import { describe, expect, it, vi } from "vitest";
import {
  diffSnapshots,
  fetchVersion,
  fetchVersions,
  restoreVersion,
  versionsKey,
} from "./versions";
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
    sequence_id: 1,
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

function version(partial: { version_no: number; snapshot: Issue }) {
  return {
    version_no: partial.version_no,
    snapshot: partial.snapshot,
    created_by: { id: "u1", name: "U One", email: "u@example.com" },
    created_at: "2026-10-09T00:00:00Z",
  };
}

const BASE = "/api/v1/workspaces/ws/projects/gl/issues/iss-1";

describe("fetchVersions", () => {
  it("GETs the list endpoint and unwraps {versions}", async () => {
    const v1 = version({ version_no: 1, snapshot: issue({ id: "iss-1" }) });
    vi.mocked(api.get).mockResolvedValue({ versions: [v1] });
    const rows = await fetchVersions("ws", "gl", "iss-1");
    expect(api.get).toHaveBeenCalledWith(`${BASE}/versions`);
    expect(rows).toEqual([v1]);
  });

  it("returns an empty list when the backend has none", async () => {
    vi.mocked(api.get).mockResolvedValue({ versions: [] });
    await expect(fetchVersions("ws", "gl", "iss-1")).resolves.toEqual([]);
  });
});

describe("fetchVersion", () => {
  it("GETs one version by number and returns it bare (no envelope)", async () => {
    const v2 = version({
      version_no: 2,
      snapshot: issue({ id: "iss-1", name: "renamed" }),
    });
    vi.mocked(api.get).mockResolvedValue(v2);
    await expect(fetchVersion("ws", "gl", "iss-1", 2)).resolves.toEqual(v2);
    expect(api.get).toHaveBeenCalledWith(`${BASE}/versions/2`);
  });
});

describe("restoreVersion", () => {
  it("POSTs to the restore endpoint with no body and returns the live Issue", async () => {
    const restored = issue({ id: "iss-1", name: "old name" });
    const spy = vi.spyOn(api, "post").mockResolvedValue(restored as never);
    await expect(restoreVersion("ws", "gl", "iss-1", 1)).resolves.toEqual(
      restored,
    );
    expect(spy).toHaveBeenCalledWith(`${BASE}/versions/1/restore`);
    spy.mockRestore();
  });
});

describe("versionsKey", () => {
  it("builds a scoped query key", () => {
    expect(versionsKey("ws", "gl", "iss-1")).toEqual([
      "versions",
      "ws",
      "gl",
      "iss-1",
    ]);
  });
});

describe("diffSnapshots", () => {
  it("reports exactly the changed fields for a two-snapshot pair", () => {
    const prev = issue({ id: "iss-1", name: "old", priority: 1 });
    const curr = issue({
      id: "iss-1",
      name: "new",
      priority: 2,
      // updated_at differs too — must NOT be reported (see below).
      updated_at: "2026-10-10T00:00:00Z",
    });
    expect(diffSnapshots(prev, curr)).toEqual([
      { field: "name", oldValue: "old", newValue: "new" },
      { field: "priority", oldValue: 1, newValue: 2 },
    ]);
  });

  it("updated_at-only difference -> zero changes", () => {
    const prev = issue({ id: "iss-1" });
    const curr = issue({
      id: "iss-1",
      updated_at: "2026-10-10T00:00:00Z",
    });
    expect(diffSnapshots(prev, curr)).toEqual([]);
  });

  it("subtask_progress-only difference -> zero changes", () => {
    const prev = issue({ id: "iss-1", subtask_progress: { total: 0, done: 0 } });
    const curr = issue({ id: "iss-1", subtask_progress: { total: 2, done: 1 } });
    expect(diffSnapshots(prev, curr)).toEqual([]);
  });

  it("deleted_at-only difference -> zero changes", () => {
    const prev = issue({ id: "iss-1" });
    const curr = issue({ id: "iss-1", deleted_at: "2026-10-10T00:00:00Z" });
    expect(diffSnapshots(prev, curr)).toEqual([]);
  });

  it("identity-only difference -> zero changes", () => {
    const prev = issue({ id: "iss-1", created_at: "2026-10-09T00:00:00Z" });
    const curr = issue({
      id: "iss-1",
      display_id: "G-2",
      created_by: "u2",
      created_at: "2026-10-09T00:00:01Z",
    });
    expect(diffSnapshots(prev, curr)).toEqual([]);
  });

  it("v1 (prev = null) -> created marker", () => {
    expect(diffSnapshots(null, issue({ id: "iss-1" }))).toEqual([
      { field: "created", oldValue: null, newValue: null },
    ]);
  });

  it("description key-reorder -> no change", () => {
    const prev = issue({
      id: "iss-1",
      description: { type: "doc", content: [{ type: "paragraph", text: "hi" }] },
    });
    const curr = issue({
      id: "iss-1",
      description: { content: [{ text: "hi", type: "paragraph" }], type: "doc" },
    });
    expect(diffSnapshots(prev, curr)).toEqual([]);
  });

  it("description content change -> exactly one change", () => {
    const prev = issue({
      id: "iss-1",
      description: { type: "doc", content: [{ type: "paragraph", text: "hi" }] },
    });
    const curr = issue({
      id: "iss-1",
      description: { type: "doc", content: [{ type: "paragraph", text: "bye" }] },
    });
    const changes = diffSnapshots(prev, curr);
    expect(changes).toHaveLength(1);
    expect(changes[0].field).toBe("description");
  });

  it("null vs undefined on a nullable field -> no change", () => {
    const prev = issue({ id: "iss-1", parent_id: undefined });
    const curr = issue({ id: "iss-1", parent_id: null });
    expect(diffSnapshots(prev, curr)).toEqual([]);
  });
});
