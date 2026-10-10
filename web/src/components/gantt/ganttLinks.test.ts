// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderHook, act } from "@testing-library/react";
import { createElement, type ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { IssueLink } from "../../lib/types";
import {
  cancelLinkDraw,
  closeLinkDelete,
  dropTargetAt,
  effectiveLinks,
  enterLinkMode,
  exitLinkMode,
  initialGanttLinkUiState,
  isTempLinkId,
  requestLinkDelete,
  resolveLinkDrop,
  startLinkDraw,
  useGanttLinkMutations,
} from "./ganttLinks";

vi.mock("../../lib/api", () => ({
  api: {
    get: vi.fn(),
    post: vi.fn(),
    patch: vi.fn(),
    del: vi.fn(),
  },
  ApiError: class ApiError extends Error {
    status: number;
    constructor(status: number, message: string) {
      super(message);
      this.status = status;
    }
  },
}));

vi.mock("../ui/toast", () => ({
  toast: { add: vi.fn() },
}));

import { api } from "../../lib/api";
import { toast } from "../ui/toast";

const post = api.post as unknown as ReturnType<typeof vi.fn>;
const del = api.del as unknown as ReturnType<typeof vi.fn>;
const toastAdd = toast.add as unknown as ReturnType<typeof vi.fn>;

function link(over: Partial<IssueLink> = {}): IssueLink {
  return {
    id: "link-1",
    issue_id: "a",
    target_issue_id: "b",
    kind: "blocks",
    created_at: "2026-10-11T00:00:00Z",
    ...over,
  };
}

describe("gantt link-mode state machine", () => {
  it("enters and exits link mode, clearing an in-progress draw", () => {
    let s = initialGanttLinkUiState;
    expect(s.linkMode).toBe(false);
    s = enterLinkMode(s);
    expect(s.linkMode).toBe(true);
    s = startLinkDraw(s, "a");
    expect(s.drawingFrom).toBe("a");
    s = exitLinkMode(s);
    expect(s.linkMode).toBe(false);
    expect(s.drawingFrom).toBeNull();
  });

  it("ignores draw start outside link mode", () => {
    const s = startLinkDraw(initialGanttLinkUiState, "a");
    expect(s.drawingFrom).toBeNull();
  });

  it("cancels a draw", () => {
    const s = cancelLinkDraw(startLinkDraw(enterLinkMode(initialGanttLinkUiState), "a"));
    expect(s.drawingFrom).toBeNull();
    expect(s.linkMode).toBe(true);
  });

  it("requests and closes the delete confirm", () => {
    const l = link();
    let s = requestLinkDelete(initialGanttLinkUiState, l);
    expect(s.confirmDelete).toBe(l);
    s = closeLinkDelete(s);
    expect(s.confirmDelete).toBeNull();
  });
});

describe("resolveLinkDrop", () => {
  it("creates when the draw lands on a different issue", () => {
    expect(resolveLinkDrop("a", "b")).toBe("create");
  });
  it("flags a self-drop", () => {
    expect(resolveLinkDrop("a", "a")).toBe("self");
  });
  it("cancels when the draw never started or missed every row", () => {
    expect(resolveLinkDrop(null, "b")).toBe("cancel");
    expect(resolveLinkDrop("a", null)).toBe("cancel");
  });
});

describe("dropTargetAt", () => {
  const rows = [
    { id: "a", y: 56 },
    { id: "b", y: 90 },
    { id: "c", y: 124 },
  ];
  const ROW_H = 34;

  it("hits the row containing the pointer", () => {
    expect(dropTargetAt(rows, 60, ROW_H)).toBe("a");
    expect(dropTargetAt(rows, 89, ROW_H)).toBe("a");
    expect(dropTargetAt(rows, 90, ROW_H)).toBe("b");
    expect(dropTargetAt(rows, 150, ROW_H)).toBe("c");
  });
  it("misses above the first row and below the last", () => {
    expect(dropTargetAt(rows, 55, ROW_H)).toBeNull();
    expect(dropTargetAt(rows, 158, ROW_H)).toBeNull();
  });
});

describe("effectiveLinks", () => {
  it("hides removed ids and appends optimistic adds", () => {
    const server = [link(), link({ id: "link-2", issue_id: "b", target_issue_id: "c" })];
    const added = [link({ id: "temp-x", issue_id: "c", target_issue_id: "d" })];
    const out = effectiveLinks(server, added, ["link-2"]);
    expect(out.map((l) => l.id)).toEqual(["link-1", "temp-x"]);
  });
  it("dedupes an add once the server has echoed it", () => {
    const server = [link(), link({ id: "link-2", issue_id: "b", target_issue_id: "c" })];
    const added = [link({ id: "link-2", issue_id: "b", target_issue_id: "c" })];
    const out = effectiveLinks(server, added, []);
    expect(out.map((l) => l.id)).toEqual(["link-1", "link-2"]);
  });
  it("prunes removed ids that no longer exist server-side", () => {
    const out = effectiveLinks([link()], [], ["link-1", "stale"]);
    expect(out.map((l) => l.id)).toEqual([]);
  });
});

describe("isTempLinkId", () => {
  it("recognizes optimistic ids", () => {
    expect(isTempLinkId("temp-abc")).toBe(true);
    expect(isTempLinkId("9f3c-real-uuid")).toBe(false);
  });
});

function makeWrapper() {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  return function Wrapper({ children }: { children: ReactNode }) {
    return createElement(QueryClientProvider, { client: qc }, children);
  };
}

describe("useGanttLinkMutations", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });
  afterEach(() => {
    vi.clearAllMocks();
  });

  it("creates a blocks link via the issue-link API and shows it optimistically", async () => {
    const serverLink = link({ id: "srv-1", issue_id: "a", target_issue_id: "b" });
    post.mockResolvedValue(serverLink);
    const { result } = renderHook(
      () => useGanttLinkMutations("acme", "ENG", []),
      { wrapper: makeWrapper() },
    );

    await act(async () => {
      result.current.createMutation.mutate({ fromId: "a", toId: "b" });
    });

    expect(post).toHaveBeenCalledWith(
      "/api/v1/workspaces/acme/projects/ENG/issues/a/links",
      { target_issue_id: "b", kind: "blocks" },
    );
    // Optimistic add replaced by the server echo, exactly once.
    expect(result.current.links.map((l) => l.id)).toEqual(["srv-1"]);
    expect(result.current.createMutation.isError).toBe(false);
  });

  it("rolls back the optimistic add and toasts on create failure", async () => {
    post.mockRejectedValue(new Error("issue link already exists"));
    const { result } = renderHook(
      () => useGanttLinkMutations("acme", "ENG", []),
      { wrapper: makeWrapper() },
    );

    await act(async () => {
      try {
        await result.current.createMutation.mutateAsync({ fromId: "a", toId: "b" });
      } catch {
        /* expected */
      }
    });

    expect(result.current.links).toEqual([]);
    expect(toastAdd).toHaveBeenCalledWith(
      expect.objectContaining({ type: "error", title: "Failed to create dependency" }),
    );
  });

  it("deletes via the issue-link API, hiding the edge optimistically", async () => {
    del.mockResolvedValue(undefined);
    const l = link();
    const { result } = renderHook(
      () => useGanttLinkMutations("acme", "ENG", [l]),
      { wrapper: makeWrapper() },
    );

    await act(async () => {
      result.current.deleteMutation.mutate(l);
    });

    expect(del).toHaveBeenCalledWith(
      "/api/v1/workspaces/acme/projects/ENG/issues/a/links/link-1",
    );
    expect(result.current.links).toEqual([]);
  });

  it("rolls back the optimistic delete and toasts on delete failure", async () => {
    del.mockRejectedValue(new Error("boom"));
    const l = link();
    const { result } = renderHook(
      () => useGanttLinkMutations("acme", "ENG", [l]),
      { wrapper: makeWrapper() },
    );

    await act(async () => {
      try {
        await result.current.deleteMutation.mutateAsync(l);
      } catch {
        /* expected */
      }
    });

    expect(result.current.links.map((x) => x.id)).toEqual(["link-1"]);
    expect(toastAdd).toHaveBeenCalledWith(
      expect.objectContaining({ type: "error", title: "Failed to remove dependency" }),
    );
  });

  it("deletes a not-yet-synced optimistic link without hitting the API", async () => {
    const { result } = renderHook(
      () => useGanttLinkMutations("acme", "ENG", []),
      { wrapper: makeWrapper() },
    );
    post.mockResolvedValue(link({ id: "srv-9", issue_id: "a", target_issue_id: "b" }));
    // Never-resolving create keeps the temp link pending for this test.
    post.mockImplementation(() => new Promise(() => {}));

    await act(async () => {
      result.current.createMutation.mutate({ fromId: "a", toId: "b" });
    });
    const temp = result.current.links[0];
    expect(isTempLinkId(temp.id)).toBe(true);

    await act(async () => {
      await result.current.deleteMutation.mutateAsync(temp);
    });

    expect(del).not.toHaveBeenCalled();
    expect(result.current.links).toEqual([]);
  });
});
