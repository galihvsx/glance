// Tests for the admin client + guard logic (C5T1). The pure helpers
// (adminRouteDecision, totalPages) are covered directly; the fetchers are
// covered by stubbing fetch and asserting the exact contract the backend
// (internal/api/admin_handler.go, C5T0) documents:
//   GET    /api/v1/admin/stats
//   GET    /api/v1/admin/users?page=&per_page=
//   PATCH  /api/v1/admin/users/:id        {is_admin}
//   POST   /api/v1/admin/users/:id/deactivate
//   POST   /api/v1/admin/users/:id/reactivate
//   GET    /api/v1/admin/workspaces?page=&per_page=
//   DELETE /api/v1/admin/workspaces/:id?confirm=<name>  (204, no body)

import { afterEach, describe, expect, it, vi } from "vitest";
import {
  adminRouteDecision,
  deactivateAdminUser,
  deleteAdminWorkspace,
  fetchAdminStats,
  fetchAdminUsers,
  fetchAdminWorkspaces,
  reactivateAdminUser,
  setAdminUser,
  totalPages,
} from "./admin";

afterEach(() => {
  vi.unstubAllGlobals();
});

function stubFetch(status: number, body: unknown): ReturnType<typeof vi.fn> {
  // 204 responses must not carry a body. Construct a fresh Response per
  // call — bodies are one-shot and api.request reads them.
  const makeResponse = () =>
    new Response(status === 204 ? null : JSON.stringify(body), {
      status,
      headers: { "Content-Type": "application/json" },
    });
  const fetchMock = vi.fn().mockImplementation(() => Promise.resolve(makeResponse()));
  vi.stubGlobal("fetch", fetchMock);
  return fetchMock;
}

describe("adminRouteDecision", () => {
  it("reports loading while the session is resolving", () => {
    expect(adminRouteDecision(true, null)).toBe("loading");
    expect(adminRouteDecision(true, { is_admin: true })).toBe("loading");
  });

  it("sends unauthenticated visitors to /login", () => {
    expect(adminRouteDecision(false, null)).toBe("login");
  });

  it("sends non-admins back to the home page, not to the admin UI", () => {
    expect(adminRouteDecision(false, { is_admin: false })).toBe("home");
  });

  it("allows instance admins into the admin UI", () => {
    expect(adminRouteDecision(false, { is_admin: true })).toBe("allow");
  });
});

describe("totalPages", () => {
  it("always yields at least one page", () => {
    expect(totalPages(0, 25)).toBe(1);
  });

  it("does not add a phantom page on an exact fit", () => {
    expect(totalPages(25, 25)).toBe(1);
    expect(totalPages(50, 25)).toBe(2);
  });

  it("rounds a partial last page up", () => {
    expect(totalPages(26, 25)).toBe(2);
    expect(totalPages(1, 25)).toBe(1);
  });
});

describe("fetchAdminStats", () => {
  it("GETs /api/v1/admin/stats and returns the counters", async () => {
    const body = {
      users: 3,
      workspaces: 2,
      projects: 5,
      issues: 41,
      attachment_bytes: 1024,
    };
    const fetchMock = stubFetch(200, body);
    await expect(fetchAdminStats()).resolves.toEqual(body);
    expect(fetchMock).toHaveBeenCalledTimes(1);
    expect(fetchMock.mock.calls[0][0]).toBe("/api/v1/admin/stats");
  });
});

describe("fetchAdminUsers", () => {
  it("GETs the paginated envelope with page/per_page params", async () => {
    const body = {
      items: [
        {
          id: "u1",
          email: "a@example.com",
          name: "A",
          is_admin: true,
          is_active: true,
          workspace_count: 1,
          created_at: "2026-10-09T00:00:00Z",
        },
      ],
      total: 1,
      page: 2,
      per_page: 25,
    };
    const fetchMock = stubFetch(200, body);
    await expect(fetchAdminUsers(2)).resolves.toEqual(body);
    expect(fetchMock.mock.calls[0][0]).toBe(
      "/api/v1/admin/users?page=2&per_page=25",
    );
  });
});

describe("setAdminUser", () => {
  it("PATCHes {is_admin} to the user endpoint", async () => {
    const fetchMock = stubFetch(200, { ok: true });
    await setAdminUser("user-uuid-1", false);
    const [url, init] = fetchMock.mock.calls[0];
    expect(url).toBe("/api/v1/admin/users/user-uuid-1");
    expect(init.method).toBe("PATCH");
    expect(JSON.parse(init.body)).toEqual({ is_admin: false });
  });

  it("surfaces server errors (e.g. 409 self-demotion) to the caller", async () => {
    stubFetch(
      409,
      { error: { code: "conflict", message: "cannot demote your own admin status" } },
    );
    await expect(setAdminUser("me", false)).rejects.toThrow(
      "cannot demote your own admin status",
    );
  });
});

describe("deactivateAdminUser / reactivateAdminUser", () => {
  it("POSTs to the deactivate/reactivate endpoints with no body", async () => {
    const fetchMock = stubFetch(200, { ok: true });
    await deactivateAdminUser("user-uuid-1");
    expect(fetchMock.mock.calls[0][0]).toBe(
      "/api/v1/admin/users/user-uuid-1/deactivate",
    );
    expect(fetchMock.mock.calls[0][1].method).toBe("POST");

    await reactivateAdminUser("user-uuid-1");
    expect(fetchMock.mock.calls[1][0]).toBe(
      "/api/v1/admin/users/user-uuid-1/reactivate",
    );
    expect(fetchMock.mock.calls[1][1].method).toBe("POST");
  });
});

describe("fetchAdminWorkspaces", () => {
  it("GETs the paginated envelope with page/per_page params", async () => {
    const body = {
      items: [
        {
          id: "w1",
          slug: "acme",
          name: "Acme",
          member_count: 4,
          project_count: 2,
          issue_count: 18,
          created_at: "2026-10-09T00:00:00Z",
        },
      ],
      total: 1,
      page: 1,
      per_page: 25,
    };
    const fetchMock = stubFetch(200, body);
    await expect(fetchAdminWorkspaces(1)).resolves.toEqual(body);
    expect(fetchMock.mock.calls[0][0]).toBe(
      "/api/v1/admin/workspaces?page=1&per_page=25",
    );
  });
});

describe("deleteAdminWorkspace", () => {
  it("DELETEs with the typed name as the confirm query param (204, no body)", async () => {
    const fetchMock = stubFetch(204, undefined);
    await deleteAdminWorkspace("ws-uuid-1", "Acme Inc");
    const [url, init] = fetchMock.mock.calls[0];
    expect(url).toBe("/api/v1/admin/workspaces/ws-uuid-1?confirm=Acme%20Inc");
    expect(init.method).toBe("DELETE");
  });

  it("surfaces a 409 confirmation mismatch to the caller", async () => {
    stubFetch(
      409,
      { error: { code: "conflict", message: "workspace name confirmation does not match" } },
    );
    await expect(deleteAdminWorkspace("ws-uuid-1", "wrong")).rejects.toThrow(
      "workspace name confirmation does not match",
    );
  });
});
