// @vitest-environment jsdom
import { act, renderHook } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { ReactNode } from "react";
import { useScheduleMutation } from "./useScheduleMutation";
import { api, ApiError } from "../../lib/api";
import { toast } from "../ui/toast";
import type { Issue } from "../../lib/types";

vi.mock("../../lib/api", () => {
  class MockApiError extends Error {
    status: number;
    body: string;
    constructor(status: number, body: string) {
      super(body);
      this.status = status;
      this.body = body;
    }
  }
  return {
    ApiError: MockApiError,
    api: {
      get: vi.fn(),
      post: vi.fn(),
      patch: vi.fn(),
      put: vi.fn(),
      delete: vi.fn(),
    },
  };
});
vi.mock("../ui/toast", () => ({ toast: { add: vi.fn() } }));

const mockPatch = api.patch as unknown as ReturnType<typeof vi.fn>;
const mockToastAdd = toast.add as unknown as ReturnType<typeof vi.fn>;

function mkIssue(over: Partial<Issue> & { id: string }): Issue {
  return {
    project_id: "p",
    sequence_id: 1,
    display_id: "ENG-1",
    name: "test",
    priority: 0,
    state_id: "s",
    sort_order: 0,
    is_draft: false,
    created_by: "u",
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    assignees: [],
    labels: [],
    start_date: null,
    target_date: null,
    ...over,
  };
}

const RANGE = { start: "2026-10-01", end: "2026-11-01" };
const dueKey = ["calendar", "acme", "ENG", "due", RANGE.start];
const startKey = ["calendar", "acme", "ENG", "start", RANGE.start];
const undatedKey = ["calendar", "acme", "ENG", "undated"];

function wrapper(client: QueryClient) {
  return function Wrapper({ children }: { children: ReactNode }) {
    return (
      <QueryClientProvider client={client}>{children}</QueryClientProvider>
    );
  };
}

function freshClient() {
  return new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
}

beforeEach(() => {
  vi.clearAllMocks();
});

describe("useScheduleMutation", () => {
  it("PATCHes the resolved dates and optimistically moves the chip", async () => {
    const client = freshClient();
    const issue = mkIssue({ id: "i1", target_date: "2026-10-10T00:00:00Z" });
    client.setQueryData(dueKey, [issue]);
    const { result } = renderHook(
      () => useScheduleMutation("acme", "ENG", RANGE),
      { wrapper: wrapper(client) },
    );

    mockPatch.mockResolvedValueOnce({ ...issue, target_date: "2026-10-14T00:00:00Z" });
    await act(async () => {
      await result.current.mutateAsync({
        uuid: "i1",
        dates: { target_date: "2026-10-14" },
      });
    });

    expect(mockPatch).toHaveBeenCalledWith(
      "/api/v1/workspaces/acme/projects/ENG/issues/i1",
      { target_date: "2026-10-14" },
    );
    const cached = client.getQueryData<Issue[]>(dueKey);
    expect(cached?.[0].target_date).toBe("2026-10-14T00:00:00Z");
    expect(mockToastAdd).not.toHaveBeenCalled();
  });

  it("rolls back the cache and toasts the server message on PATCH failure", async () => {
    const client = freshClient();
    const issue = mkIssue({ id: "i1", target_date: "2026-10-10T00:00:00Z" });
    client.setQueryData(dueKey, [issue]);
    const { result } = renderHook(
      () => useScheduleMutation("acme", "ENG", RANGE),
      { wrapper: wrapper(client) },
    );

    mockPatch.mockRejectedValueOnce(
      new ApiError(400, "start_date cannot be after target_date"),
    );
    await act(async () => {
      await expect(
        result.current.mutateAsync({
          uuid: "i1",
          dates: { target_date: "2026-10-14" },
        }),
      ).rejects.toThrow();
    });

    // Cache rolled back to the pre-drop dates.
    const cached = client.getQueryData<Issue[]>(dueKey);
    expect(cached).toHaveLength(1);
    expect(cached?.[0].target_date).toBe("2026-10-10T00:00:00Z");
    // Error toast carries the server message.
    expect(mockToastAdd).toHaveBeenCalledWith(
      expect.objectContaining({
        type: "error",
        description: "start_date cannot be after target_date",
      }),
    );
  });

  it("drops the chip from the due cache when moved outside the visible range", async () => {
    const client = freshClient();
    const issue = mkIssue({ id: "i1", target_date: "2026-10-31T00:00:00Z" });
    client.setQueryData(dueKey, [issue]);
    const { result } = renderHook(
      () => useScheduleMutation("acme", "ENG", RANGE),
      { wrapper: wrapper(client) },
    );

    mockPatch.mockResolvedValueOnce({ ...issue, target_date: "2026-11-05T00:00:00Z" });
    await act(async () => {
      await result.current.mutateAsync({
        uuid: "i1",
        dates: { target_date: "2026-11-05" },
      });
    });

    expect(client.getQueryData<Issue[]>(dueKey)).toHaveLength(0);
  });

  it("leaves the start cache untouched for issues without a target date", async () => {
    const client = freshClient();
    const issue = mkIssue({ id: "i1", start_date: "2026-10-10T00:00:00Z" });
    client.setQueryData(startKey, [issue]);
    const { result } = renderHook(
      () => useScheduleMutation("acme", "ENG", RANGE),
      { wrapper: wrapper(client) },
    );

    mockPatch.mockResolvedValueOnce({ ...issue, start_date: "2026-10-14T00:00:00Z" });
    await act(async () => {
      await result.current.mutateAsync({
        uuid: "i1",
        dates: { start_date: "2026-10-14" },
      });
    });

    const cached = client.getQueryData<Issue[]>(startKey);
    expect(cached?.[0].start_date).toBe("2026-10-14T00:00:00Z");
    expect(cached?.[0].target_date).toBeNull();
    expect(client.getQueryData(dueKey)).toBeUndefined();
  });

  it("PATCHes a null target_date to unschedule and optimistically lands the chip in the undated cache", async () => {
    const client = freshClient();
    const issue = mkIssue({ id: "i1", target_date: "2026-10-10T00:00:00Z" });
    client.setQueryData(dueKey, [issue]);
    client.setQueryData(undatedKey, []);
    const { result } = renderHook(
      () => useScheduleMutation("acme", "ENG", RANGE),
      { wrapper: wrapper(client) },
    );

    mockPatch.mockResolvedValueOnce({ ...issue, target_date: null });
    await act(async () => {
      await result.current.mutateAsync({
        uuid: "i1",
        dates: { target_date: null },
      });
    });

    // Explicit null goes out on the wire so the server clears the column.
    expect(mockPatch).toHaveBeenCalledWith(
      "/api/v1/workspaces/acme/projects/ENG/issues/i1",
      { target_date: null },
    );
    // Chip leaves the dated caches…
    expect(client.getQueryData<Issue[]>(dueKey)).toHaveLength(0);
    // …and appears in the undated cache with the date cleared.
    const undated = client.getQueryData<Issue[]>(undatedKey);
    expect(undated).toHaveLength(1);
    expect(undated?.[0].target_date).toBeNull();
    expect(mockToastAdd).not.toHaveBeenCalled();
  });

  it("PATCHes a null start_date to unschedule a start-placed chip", async () => {
    const client = freshClient();
    const issue = mkIssue({ id: "i1", start_date: "2026-10-10T00:00:00Z" });
    client.setQueryData(startKey, [issue]);
    client.setQueryData(undatedKey, []);
    const { result } = renderHook(
      () => useScheduleMutation("acme", "ENG", RANGE),
      { wrapper: wrapper(client) },
    );

    mockPatch.mockResolvedValueOnce({ ...issue, start_date: null });
    await act(async () => {
      await result.current.mutateAsync({
        uuid: "i1",
        dates: { start_date: null },
      });
    });

    expect(mockPatch).toHaveBeenCalledWith(
      "/api/v1/workspaces/acme/projects/ENG/issues/i1",
      { start_date: null },
    );
    expect(client.getQueryData<Issue[]>(startKey)).toHaveLength(0);
    expect(client.getQueryData<Issue[]>(undatedKey)).toHaveLength(1);
  });

  it("rolls back the undated optimistic add when the unschedule PATCH fails", async () => {
    const client = freshClient();
    const issue = mkIssue({ id: "i1", target_date: "2026-10-10T00:00:00Z" });
    client.setQueryData(dueKey, [issue]);
    client.setQueryData(undatedKey, []);
    const { result } = renderHook(
      () => useScheduleMutation("acme", "ENG", RANGE),
      { wrapper: wrapper(client) },
    );

    mockPatch.mockRejectedValueOnce(new ApiError(403, "forbidden"));
    await act(async () => {
      await expect(
        result.current.mutateAsync({
          uuid: "i1",
          dates: { target_date: null },
        }),
      ).rejects.toThrow();
    });

    // Dated cache restored, optimistic undated add reverted.
    const due = client.getQueryData<Issue[]>(dueKey);
    expect(due).toHaveLength(1);
    expect(due?.[0].target_date).toBe("2026-10-10T00:00:00Z");
    expect(client.getQueryData<Issue[]>(undatedKey)).toHaveLength(0);
    expect(mockToastAdd).toHaveBeenCalledWith(
      expect.objectContaining({ type: "error", description: "forbidden" }),
    );
  });

  it("PATCHes a target_date when a strip chip is scheduled onto a day", async () => {
    const client = freshClient();
    const issue = mkIssue({ id: "i1" });
    client.setQueryData(undatedKey, [issue]);
    const { result } = renderHook(
      () => useScheduleMutation("acme", "ENG", RANGE),
      { wrapper: wrapper(client) },
    );

    mockPatch.mockResolvedValueOnce({
      ...issue,
      target_date: "2026-10-14T00:00:00Z",
    });
    await act(async () => {
      await result.current.mutateAsync({
        uuid: "i1",
        dates: { target_date: "2026-10-14" },
      });
    });

    expect(mockPatch).toHaveBeenCalledWith(
      "/api/v1/workspaces/acme/projects/ENG/issues/i1",
      { target_date: "2026-10-14" },
    );
    // Chip leaves the undated cache (it reappears in the day cell on refetch).
    expect(client.getQueryData<Issue[]>(undatedKey)).toHaveLength(0);
  });

  it("invalidates the PQL window sets on settle so a drag in PQL mode refetches", async () => {
    const client = freshClient();
    const issue = mkIssue({ id: "i1", target_date: "2026-10-10T00:00:00Z" });
    // PQL keys (see Calendar.tsx): element 4 is "due-pql" / "start-pql" /
    // "undated-pql", so they are NOT a prefix match for the base keys.
    const pqlDueKey = ["calendar", "acme", "ENG", "due-pql", RANGE.start, "state:open"];
    const pqlStartKey = ["calendar", "acme", "ENG", "start-pql", RANGE.start, "state:open"];
    const pqlUndatedKey = ["calendar", "acme", "ENG", "undated-pql", "state:open"];
    client.setQueryData(pqlDueKey, [issue]);
    client.setQueryData(pqlStartKey, []);
    client.setQueryData(pqlUndatedKey, []);
    const invalidateSpy = vi.spyOn(client, "invalidateQueries");
    const { result } = renderHook(
      () => useScheduleMutation("acme", "ENG", RANGE),
      { wrapper: wrapper(client) },
    );

    mockPatch.mockResolvedValueOnce({ ...issue, target_date: "2026-10-14T00:00:00Z" });
    await act(async () => {
      await result.current.mutateAsync({
        uuid: "i1",
        dates: { target_date: "2026-10-14" },
      });
    });

    // The settle invalidation must cover the shared prefix, which is the
    // only key shape that prefix-matches both base and *-pql keys.
    expect(invalidateSpy).toHaveBeenCalledWith({
      queryKey: ["calendar", "acme", "ENG"],
    });
    // And that prefix must actually match all three PQL keys in the cache.
    const matched = client
      .getQueryCache()
      .findAll({ queryKey: ["calendar", "acme", "ENG"] })
      .map((q) => q.queryKey);
    expect(matched).toEqual(
      expect.arrayContaining([pqlDueKey, pqlStartKey, pqlUndatedKey]),
    );
    invalidateSpy.mockRestore();
  });
});
