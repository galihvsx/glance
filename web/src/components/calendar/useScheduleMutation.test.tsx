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
});
