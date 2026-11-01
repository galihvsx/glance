// @vitest-environment jsdom
// useSavedViews in workspace scope (C113T1), through the real hook:
// the workspace list query hits /api/v1/workspaces/{slug}/views, the
// own/shared split keys on the authed user, and create/share go to
// the workspace endpoints. Project scope through the same hook is
// the regression guard (bare-UUID argument form, unchanged).
import { act, renderHook, waitFor } from "@testing-library/react";
import { QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ReactNode } from "react";
import { useSavedViews } from "./useSavedViews";
import { api } from "../../lib/api";
import { EMPTY_FILTERS } from "../../lib/filters";
import type { IssueViewRow } from "../../lib/savedViews";
import { makeQueryClient } from "../shell/test-utils";

// Stub auth: the own/shared split keys on this user id.
vi.mock("../../lib/auth", () => ({
  useAuth: () => ({
    user: { id: "u-1", email: "u@x.com", name: "Me" },
    loading: false,
  }),
}));
vi.mock("../ui/toast", () => ({ toast: { add: vi.fn() } }));

function makeRow(overrides: Partial<IssueViewRow> = {}): IssueViewRow {
  return {
    id: "view-1",
    name: "High priority",
    filters: { ...EMPTY_FILTERS, pql: "priority = High" },
    display: null,
    is_default: false,
    shared: false,
    owner_id: "u-1",
    owner_name: "Me",
    created_at: "2026-10-10T00:00:00Z",
    ...overrides,
  };
}

const sharedRow = makeRow({
  id: "view-2",
  name: "Team highs",
  shared: true,
  owner_id: "u-2",
  owner_name: "Bob",
});

function wrapper(qc: ReturnType<typeof makeQueryClient>) {
  return function Wrapper({ children }: { children: ReactNode }) {
    return <QueryClientProvider client={qc}>{children}</QueryClientProvider>;
  };
}

beforeEach(() => {
  vi.restoreAllMocks();
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe("useSavedViews workspace scope (C113T1)", () => {
  it("lists from /api/v1/workspaces/{slug}/views with the own/shared split", async () => {
    const get = vi.spyOn(api, "get").mockImplementation((path: string) => {
      if (path === "/api/v1/workspaces/acme/views")
        return Promise.resolve({ views: [makeRow(), sharedRow] });
      return Promise.reject(new Error(`unexpected GET ${path}`));
    });
    const { result } = renderHook(
      () => useSavedViews({ workspaceSlug: "acme" }),
      { wrapper: wrapper(makeQueryClient()) },
    );
    await waitFor(() => expect(result.current.viewsReady).toBe(true));
    expect(get).toHaveBeenCalledWith("/api/v1/workspaces/acme/views");
    expect(result.current.views.map((v) => v.id)).toEqual([
      "view-1",
      "view-2",
    ]);
    expect(result.current.ownViews.map((v) => v.id)).toEqual(["view-1"]);
    expect(result.current.sharedViews.map((v) => v.id)).toEqual(["view-2"]);
  });

  it("creates and shares through the workspace endpoints", async () => {
    vi.spyOn(api, "get").mockImplementation((path: string) => {
      if (path === "/api/v1/workspaces/acme/views")
        return Promise.resolve({ views: [makeRow(), sharedRow] });
      return Promise.reject(new Error(`unexpected GET ${path}`));
    });
    const post = vi
      .spyOn(api, "post")
      .mockResolvedValue(makeRow({ id: "view-9", name: "Saved" }));
    const patch = vi
      .spyOn(api, "patch")
      .mockResolvedValue(makeRow({ shared: true }));
    const { result } = renderHook(
      () => useSavedViews({ workspaceSlug: "acme" }),
      { wrapper: wrapper(makeQueryClient()) },
    );
    await waitFor(() => expect(result.current.viewsReady).toBe(true));
    await act(async () => {
      await result.current.createView("Saved", EMPTY_FILTERS, null);
    });
    expect(post).toHaveBeenCalledWith("/api/v1/workspaces/acme/views", {
      name: "Saved",
      filters: EMPTY_FILTERS,
      display: null,
    });
    await act(async () => {
      await result.current.setViewShared("view-1", true);
    });
    expect(patch).toHaveBeenCalledWith(
      "/api/v1/workspaces/acme/views/view-1",
      { shared: true },
    );
    // A non-owner row cannot be shared from this hook, before any
    // network call: the guard is client-side, not a backend 403.
    await expect(
      act(async () => {
        await result.current.setViewShared("view-2", true);
      }),
    ).rejects.toThrow("only the view owner can change sharing");
  });

  it("the bare-UUID project form still hits the project endpoints", async () => {
    const get = vi.spyOn(api, "get").mockImplementation((path: string) => {
      if (path === "/api/v1/projects/p1/views")
        return Promise.resolve({ views: [makeRow()] });
      return Promise.reject(new Error(`unexpected GET ${path}`));
    });
    const { result } = renderHook(() => useSavedViews("p1"), {
      wrapper: wrapper(makeQueryClient()),
    });
    await waitFor(() => expect(result.current.viewsReady).toBe(true));
    expect(get).toHaveBeenCalledWith("/api/v1/projects/p1/views");
    expect(result.current.ownViews.map((v) => v.id)).toEqual(["view-1"]);
  });
});
