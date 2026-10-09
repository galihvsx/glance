import { beforeEach, describe, expect, it, vi } from "vitest";
import {
  EMPTY_FILTERS,
  type IssueFilters,
} from "../../lib/filters";
import { ApiError } from "../../lib/api";
import {
  createIssueView,
  deleteIssueView,
  updateIssueView,
  type IssueViewRow,
} from "../../lib/savedViews";
import { sanitizeView, type SavedView } from "./useSavedViews";
import { DEFAULT_DISPLAY_SETTINGS } from "./useDisplaySettings";

const FULL_FILTERS: IssueFilters = {
  ...EMPTY_FILTERS,
  q: "login bug",
  priorities: [3, 1],
  assignees: ["user-uuid-1", "none"],
  createdAfter: "2026-10-01",
  subscribed: true,
};

/** A backend row as GET /api/v1/projects/{id}/views returns it. */
function makeRow(overrides: Partial<IssueViewRow> = {}): IssueViewRow {
  return {
    id: "view-1",
    name: "My bugs",
    filters: FULL_FILTERS,
    display: {
      ...DEFAULT_DISPLAY_SETTINGS,
      groupBy: "priority",
      showEmptyGroups: false,
    },
    is_default: false,
    shared: false,
    owner_id: "user-1",
    owner_name: "Alice",
    created_at: "2026-10-10T00:00:00Z",
    ...overrides,
  };
}

describe("sanitizeView", () => {
  it("keeps a well-formed backend row intact", () => {
    const row = makeRow();
    const v = sanitizeView(row);
    expect(v).not.toBeNull();
    expect(v?.id).toBe("view-1");
    expect(v?.name).toBe("My bugs");
    expect(v?.filters).toEqual(FULL_FILTERS);
    expect(v?.display?.groupBy).toBe("priority");
    expect(v?.isDefault).toBe(false);
  });

  it("maps is_default to isDefault", () => {
    expect(sanitizeView(makeRow({ is_default: true }))?.isDefault).toBe(true);
  });

  it("accepts a null display (spreadsheet-saved view)", () => {
    const v = sanitizeView(makeRow({ display: null }));
    expect(v?.display).toBeNull();
  });

  it("drops garbage (non-objects, missing id/name, non-object filters)", () => {
    expect(sanitizeView(null)).toBeNull();
    expect(sanitizeView("nope")).toBeNull();
    expect(sanitizeView({ ...makeRow(), id: "" })).toBeNull();
    expect(sanitizeView({ ...makeRow(), name: "   " })).toBeNull();
    expect(sanitizeView({ ...makeRow(), filters: null })).toBeNull();
    expect(
      sanitizeView({ ...makeRow(), filters: "q=bug" as never }),
    ).toBeNull();
  });

  it("trims and caps the name", () => {
    const v = sanitizeView(makeRow({ name: "  padded  " }));
    expect(v?.name).toBe("padded");
    const long = sanitizeView(makeRow({ name: "x".repeat(200) }));
    expect(long?.name).toHaveLength(64);
  });

  it("merges partial filters over EMPTY_FILTERS so the menu never breaks", () => {
    const v = sanitizeView(makeRow({ filters: { q: "x" } as never }));
    expect(v?.filters.q).toBe("x");
    expect(v?.filters.priorities).toEqual([]);
    expect(v?.filters.archived).toBe(false);
  });

  it("sanitizes a garbage display to defaults, keeps null as null", () => {
    const v = sanitizeView(makeRow({ display: { groupBy: "bogus" } as never }));
    expect(v?.display?.groupBy).toBe(DEFAULT_DISPLAY_SETTINGS.groupBy);
  });
});

describe("saved views backend wiring", () => {
  const PROJECT_ID = "11111111-2222-3333-4444-555555555555";

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

  it("POSTs name/filters/display to /api/v1/projects/{id}/views", async () => {
    let seenUrl = "";
    let seenBody: Record<string, unknown> = {};
    mockFetch((url, init) => {
      seenUrl = url;
      seenBody = JSON.parse(init.body as string) as Record<string, unknown>;
      return ok(201, makeRow());
    });
    const row = await createIssueView(PROJECT_ID, {
      name: "My bugs",
      filters: FULL_FILTERS,
      display: DEFAULT_DISPLAY_SETTINGS,
    });
    expect(seenUrl).toBe(`/api/v1/projects/${PROJECT_ID}/views`);
    expect(seenBody.name).toBe("My bugs");
    expect(seenBody.filters).toEqual(FULL_FILTERS);
    expect(seenBody.display).toEqual(DEFAULT_DISPLAY_SETTINGS);
    expect(row.id).toBe("view-1");
  });

  it("surfaces a 409 duplicate as an ApiError with the envelope message", async () => {
    mockFetch(() =>
      ok(409, {
        error: { code: "conflict", message: "a view with that name already exists" },
      }),
    );
    const err = await createIssueView(PROJECT_ID, {
      name: "My bugs",
      filters: FULL_FILTERS,
      display: null,
    }).catch((e) => e);
    expect(err).toBeInstanceOf(ApiError);
    expect((err as ApiError).status).toBe(409);
    expect((err as ApiError).message).toBe(
      "a view with that name already exists",
    );
  });

  it("PATCHes name / is_default to /views/{viewId}", async () => {
    const seen: Array<{ url: string; body: unknown }> = [];
    mockFetch((url, init) => {
      seen.push({ url, body: JSON.parse(init.body as string) });
      return ok(200, makeRow({ name: "Renamed", is_default: true }));
    });
    const renamed = await updateIssueView(PROJECT_ID, "view-1", {
      name: "Renamed",
    });
    expect(seen[0].url).toBe(
      `/api/v1/projects/${PROJECT_ID}/views/view-1`,
    );
    expect(seen[0].body).toEqual({ name: "Renamed" });
    expect(renamed.name).toBe("Renamed");
    await updateIssueView(PROJECT_ID, "view-1", { is_default: true });
    expect(seen[1].body).toEqual({ is_default: true });
  });

  it("DELETEs /views/{viewId}", async () => {
    let seenUrl = "";
    let seenMethod = "";
    mockFetch((url, init) => {
      seenUrl = url;
      seenMethod = init.method ?? "";
      return new Response(null, { status: 204 });
    });
    await deleteIssueView(PROJECT_ID, "view-1");
    expect(seenMethod).toBe("DELETE");
    expect(seenUrl).toBe(`/api/v1/projects/${PROJECT_ID}/views/view-1`);
  });

  it("sanitizeView round-trips a created row into the SavedView shape", async () => {
    mockFetch(() => ok(201, makeRow({ display: null })));
    const row = await createIssueView(PROJECT_ID, {
      name: "My bugs",
      filters: FULL_FILTERS,
      display: null,
    });
    const view: SavedView | null = sanitizeView(row);
    expect(view?.filters.q).toBe("login bug");
    expect(view?.display).toBeNull();
  });
});

describe("shared views (C10T1)", () => {
  const PROJECT_ID = "11111111-2222-3333-4444-555555555555";

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

  it("sanitizeView maps the shared flag and owner annotation", () => {
    const v = sanitizeView(
      makeRow({ shared: true, owner_id: "user-2", owner_name: "Bob" }),
    );
    expect(v?.shared).toBe(true);
    expect(v?.ownerId).toBe("user-2");
    expect(v?.ownerName).toBe("Bob");
  });

  it("sanitizeView defaults a private row to shared=false", () => {
    const v = sanitizeView(makeRow());
    expect(v?.shared).toBe(false);
    expect(v?.ownerId).toBe("user-1");
    expect(v?.ownerName).toBe("Alice");
  });

  it("PATCHes {shared:true} to /views/{viewId} to share a view", async () => {
    let seenUrl = "";
    let seenBody: Record<string, unknown> = {};
    mockFetch((url, init) => {
      seenUrl = url;
      seenBody = JSON.parse(init.body as string) as Record<string, unknown>;
      return ok(200, makeRow({ shared: true }));
    });
    const row = await updateIssueView(PROJECT_ID, "view-1", { shared: true });
    expect(seenUrl).toBe(`/api/v1/projects/${PROJECT_ID}/views/view-1`);
    expect(seenBody).toEqual({ shared: true });
    expect(row.shared).toBe(true);
  });

  it("surfaces a 403 on a non-owner share toggle as an ApiError", async () => {
    mockFetch(() =>
      ok(403, {
        error: {
          code: "forbidden",
          message: "only the view owner can change it",
        },
      }),
    );
    const err = await updateIssueView(PROJECT_ID, "view-1", {
      shared: true,
    }).catch((e) => e);
    expect(err).toBeInstanceOf(ApiError);
    expect((err as ApiError).status).toBe(403);
  });
});
