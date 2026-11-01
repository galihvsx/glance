// @vitest-environment jsdom
// Workspace-scoped saved-views client (C113T1): the same
// list/create/update/delete shapes as the project group, under
// /api/v1/workspaces/{slug}/views (the C113T0 contract), with
// ["saved-views", "ws", slug] query keys. Follows the
// useSavedViews.test.ts fetch-mock idiom.
import { beforeEach, describe, expect, it, vi } from "vitest";
import { EMPTY_FILTERS } from "./filters";
import { ApiError } from "./api";
import {
  createIssueView,
  deleteIssueView,
  savedViewKeys,
  scopeViewsBasePath,
  updateIssueView,
  type IssueViewRow,
} from "./savedViews";
import { DEFAULT_DISPLAY_SETTINGS } from "../components/issue/useDisplaySettings";

function makeRow(overrides: Partial<IssueViewRow> = {}): IssueViewRow {
  return {
    id: "view-1",
    name: "High priority",
    filters: { ...EMPTY_FILTERS, pql: "priority = High" },
    display: null,
    is_default: false,
    shared: false,
    owner_id: "user-1",
    owner_name: "Alice",
    created_at: "2026-10-10T00:00:00Z",
    ...overrides,
  };
}

function mockFetch(handler: (url: string, init: RequestInit) => Response) {
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: string, init: RequestInit) => handler(url, init)),
  );
}

const ok = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });

beforeEach(() => {
  vi.unstubAllGlobals();
});

describe("workspace-scoped saved views client (C113T1)", () => {
  it("builds the workspace collection path and ws query key", () => {
    expect(scopeViewsBasePath({ workspaceSlug: "acme" })).toBe(
      "/api/v1/workspaces/acme/views",
    );
    expect(savedViewKeys.ws("acme")).toEqual(["saved-views", "ws", "acme"]);
    expect(savedViewKeys.forScope({ workspaceSlug: "acme" })).toEqual([
      "saved-views",
      "ws",
      "acme",
    ]);
    // Project scope keeps its bare-UUID key (no "ws" segment).
    expect(savedViewKeys.forScope({ projectId: "p1" })).toEqual([
      "saved-views",
      "p1",
    ]);
  });

  it("POSTs name/filters/display to /api/v1/workspaces/{slug}/views", async () => {
    let seenUrl = "";
    let seenBody: Record<string, unknown> = {};
    mockFetch((url, init) => {
      seenUrl = url;
      seenBody = JSON.parse(init.body as string) as Record<string, unknown>;
      return ok(201, makeRow());
    });
    const row = await createIssueView(
      { workspaceSlug: "acme" },
      {
        name: "High priority",
        filters: { ...EMPTY_FILTERS, pql: "priority = High" },
        display: null,
      },
    );
    expect(seenUrl).toBe("/api/v1/workspaces/acme/views");
    expect(seenBody.name).toBe("High priority");
    expect(seenBody.filters).toEqual({
      ...EMPTY_FILTERS,
      pql: "priority = High",
    });
    expect(seenBody.display).toBeNull();
    expect(row.id).toBe("view-1");
  });

  it("surfaces a workspace 409 duplicate as an ApiError", async () => {
    mockFetch(() =>
      ok(409, {
        error: {
          code: "conflict",
          message: "a view with that name already exists",
        },
      }),
    );
    const err = await createIssueView(
      { workspaceSlug: "acme" },
      { name: "High priority", filters: EMPTY_FILTERS, display: null },
    ).catch((e) => e);
    expect(err).toBeInstanceOf(ApiError);
    expect((err as ApiError).status).toBe(409);
  });

  it("PATCHes name / is_default / shared to /workspaces/{slug}/views/{viewId}", async () => {
    const seen: Array<{ url: string; body: unknown }> = [];
    mockFetch((url, init) => {
      seen.push({ url, body: JSON.parse(init.body as string) });
      return ok(200, makeRow({ name: "Renamed", shared: true }));
    });
    await updateIssueView(
      { workspaceSlug: "acme" },
      "view-1",
      { name: "Renamed" },
    );
    expect(seen[0].url).toBe("/api/v1/workspaces/acme/views/view-1");
    expect(seen[0].body).toEqual({ name: "Renamed" });
    await updateIssueView(
      { workspaceSlug: "acme" },
      "view-1",
      { shared: true },
    );
    expect(seen[1].body).toEqual({ shared: true });
  });

  it("DELETEs /workspaces/{slug}/views/{viewId}", async () => {
    let seenUrl = "";
    let seenMethod = "";
    mockFetch((url, init) => {
      seenUrl = url;
      seenMethod = init.method ?? "";
      return new Response(null, { status: 204 });
    });
    await deleteIssueView({ workspaceSlug: "acme" }, "view-1");
    expect(seenMethod).toBe("DELETE");
    expect(seenUrl).toBe("/api/v1/workspaces/acme/views/view-1");
  });

  it("the scope-object project form still hits the project endpoints", async () => {
    let seenUrl = "";
    mockFetch((url) => {
      seenUrl = url;
      return ok(201, makeRow());
    });
    await createIssueView(
      { projectId: "p1" },
      { name: "X", filters: EMPTY_FILTERS, display: DEFAULT_DISPLAY_SETTINGS },
    );
    expect(seenUrl).toBe("/api/v1/projects/p1/views");
  });
});
