// @vitest-environment jsdom
// Tests for the realtime → TanStack Query invalidation map and the
// ?updated_after= reconnect resync (Task 25, cycle 41: six *.updated
// handlers + workspace subscription; C41T2: notification.created handler
// + user-channel subscription).

import { afterEach, describe, expect, it, vi } from "vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderHook } from "@testing-library/react";
import { createElement, type ReactNode } from "react";
import {
  handleRealtimeEvent,
  resyncProject,
  useProjectRealtime,
} from "./realtime";
import type { WsEventFrame, WsEventHandler } from "./ws";

// The hook test needs a fake WS client; the handler tests only use the
// WsEventFrame type from ./ws, so mocking the module is safe here.
const wsMock = vi.hoisted(() => {
  const subscriptions = new Map<string, WsEventHandler[]>();
  const client = {
    connect: vi.fn(),
    subscribe: vi.fn((channel: string, handler: WsEventHandler) => {
      const list = subscriptions.get(channel) ?? [];
      list.push(handler);
      subscriptions.set(channel, list);
      return () => {};
    }),
  };
  return { subscriptions, client };
});
vi.mock("./ws", () => ({
  getRealtimeClient: () => wsMock.client,
  workspaceChannel: (slug: string) => `workspace:${slug}`,
  projectChannel: (slug: string, identifier: string) =>
    `project:${slug}:${identifier}`,
  issueChannel: (uuid: string) => `issue:${uuid}`,
  userChannel: (id: string) => `user:${id}`,
}));

function frame(event: string, data: unknown): WsEventFrame {
  return { event, channel: "project:acme:ENG", data, at: "2026-10-06T00:00:00Z" };
}

function spiedClient(): { qc: QueryClient; keys: unknown[][] } {
  const qc = new QueryClient();
  const keys: unknown[][] = [];
  vi.spyOn(qc, "invalidateQueries").mockImplementation(async (opts) => {
    keys.push(opts?.queryKey as unknown[]);
  });
  return { qc, keys };
}

const CTX = { slug: "acme", identifier: "ENG" };

describe("handleRealtimeEvent", () => {
  it("issue.* invalidates list, board, detail, and history — targeted, not global", () => {
    const { qc, keys } = spiedClient();
    handleRealtimeEvent(qc, CTX, frame("issue.updated", { id: "uuid-9" }));
    expect(keys).toContainEqual(["issues", "acme", "ENG"]);
    expect(keys).toContainEqual(["board-issues", "acme", "ENG"]);
    expect(keys).toContainEqual(["issue", "acme", "ENG", "uuid-9"]);
    expect(keys).toContainEqual(["history", "acme", "ENG", "uuid-9"]);
    // No bare/global invalidation: other projects' caches are untouched.
    expect(keys.every((k) => k[1] === "acme" && k[2] === "ENG")).toBe(true);
  });

  it("comment.created invalidates comments + history for the issue", () => {
    const { qc, keys } = spiedClient();
    handleRealtimeEvent(qc, CTX, frame("comment.created", { id: "c1", issue_id: "uuid-9" }));
    expect(keys).toContainEqual(["comments", "acme", "ENG", "uuid-9"]);
    expect(keys).toContainEqual(["history", "acme", "ENG", "uuid-9"]);
    expect(keys).toHaveLength(2);
  });

  it("cycle.updated invalidates cycles and cycle-issues", () => {
    const { qc, keys } = spiedClient();
    handleRealtimeEvent(qc, CTX, frame("cycle.updated", { id: "cy1" }));
    expect(keys).toContainEqual(["cycles", "acme", "ENG"]);
    expect(keys).toContainEqual(["cycle-issues", "acme", "ENG"]);
  });

  it("intake.updated invalidates the intake inbox", () => {
    const { qc, keys } = spiedClient();
    handleRealtimeEvent(qc, CTX, frame("intake.updated", { id: "in1" }));
    expect(keys).toContainEqual(["intake", "acme", "ENG"]);
  });

  it("module.updated invalidates modules and module-issues", () => {
    const { qc, keys } = spiedClient();
    handleRealtimeEvent(qc, CTX, frame("module.updated", { id: "m1" }));
    expect(keys).toEqual([
      ["modules", "acme", "ENG"],
      ["module-issues", "acme", "ENG"],
    ]);
  });

  it("page.updated invalidates pages, page-revisions, and page-detail", () => {
    const { qc, keys } = spiedClient();
    handleRealtimeEvent(qc, CTX, frame("page.updated", { id: "pg1" }));
    expect(keys).toEqual([
      ["pages", "acme", "ENG"],
      ["page-revisions", "acme", "ENG"],
      ["page-detail", "acme", "ENG", "pg1"],
    ]);
  });

  it("release.updated invalidates releases, release-issues, and release-notes", () => {
    const { qc, keys } = spiedClient();
    handleRealtimeEvent(qc, CTX, frame("release.updated", { id: "r1" }));
    expect(keys).toEqual([
      ["releases", "acme", "ENG"],
      ["release-issues", "acme", "ENG"],
      ["release-notes", "acme", "ENG"],
    ]);
  });

  it("template.updated invalidates the templates list", () => {
    const { qc, keys } = spiedClient();
    handleRealtimeEvent(qc, CTX, frame("template.updated", { id: "t1" }));
    expect(keys).toEqual([["templates", "acme", "ENG"]]);
  });

  it("custom_field.updated (value write) invalidates custom-fields and the issue detail", () => {
    // Value writes announce with id = the issue id (custom_fields.go);
    // custom values ride inside the issue-detail query (?include_custom=1).
    const { qc, keys } = spiedClient();
    handleRealtimeEvent(qc, CTX, frame("custom_field.updated", { id: "uuid-9" }));
    expect(keys).toEqual([
      ["custom-fields", "acme", "ENG"],
      ["issue", "acme", "ENG", "uuid-9"],
    ]);
  });

  it("custom_field.updated without an id invalidates only the custom-fields list", () => {
    const { qc, keys } = spiedClient();
    handleRealtimeEvent(qc, CTX, frame("custom_field.updated", {}));
    expect(keys).toEqual([["custom-fields", "acme", "ENG"]]);
  });

  it("initiative.updated invalidates initiatives, detail, and rollup", () => {
    const { qc, keys } = spiedClient();
    handleRealtimeEvent(qc, CTX, frame("initiative.updated", { id: "init-1" }));
    expect(keys).toEqual([
      ["initiatives", "acme"],
      ["initiative", "acme", "init-1"],
      ["initiative-rollup", "acme", "init-1"],
    ]);
  });

  it("initiative.updated without an id invalidates only the initiatives list", () => {
    const { qc, keys } = spiedClient();
    handleRealtimeEvent(qc, CTX, frame("initiative.updated", {}));
    expect(keys).toEqual([["initiatives", "acme"]]);
  });

  it("notification.created invalidates ONLY the notifications prefix", () => {
    const { qc, keys } = spiedClient();
    handleRealtimeEvent(qc, CTX, frame("notification.created", { id: "n1" }));
    expect(keys).toEqual([["notifications"]]);
  });

  it("ignores unknown future events", () => {
    const { qc, keys } = spiedClient();
    handleRealtimeEvent(qc, CTX, frame("something.else", { id: "x1" }));
    expect(keys).toHaveLength(0);
  });
});

describe("useProjectRealtime workspace subscription", () => {
  afterEach(() => {
    wsMock.subscriptions.clear();
    vi.clearAllMocks();
  });

  it("subscribes to the workspace channel and initiative.updated there invalidates initiatives", () => {
    const qc = new QueryClient();
    const keys: unknown[][] = [];
    vi.spyOn(qc, "invalidateQueries").mockImplementation(async (opts) => {
      keys.push(opts?.queryKey as unknown[]);
    });
    const wrapper = ({ children }: { children: ReactNode }) =>
      createElement(QueryClientProvider, { client: qc }, children);
    const { unmount } = renderHook(
      () => useProjectRealtime({ slug: "acme", identifier: "ENG" }),
      { wrapper },
    );
    try {
      const channels = [...wsMock.subscriptions.keys()];
      expect(channels).toContain("project:acme:ENG");
      expect(channels).toContain("workspace:acme");
      const wsHandler = wsMock.subscriptions.get("workspace:acme");
      expect(wsHandler).toHaveLength(1);
      wsHandler![0](frame("initiative.updated", { id: "init-1" }));
      expect(keys).toContainEqual(["initiatives", "acme"]);
      expect(keys).toContainEqual(["initiative", "acme", "init-1"]);
      expect(keys).toContainEqual(["initiative-rollup", "acme", "init-1"]);
    } finally {
      unmount();
    }
  });
});

describe("useProjectRealtime user-channel subscription", () => {
  afterEach(() => {
    wsMock.subscriptions.clear();
    vi.clearAllMocks();
  });

  function mountHook(opts: { slug: string; identifier: string; userId?: string }) {
    const qc = new QueryClient();
    const keys: unknown[][] = [];
    vi.spyOn(qc, "invalidateQueries").mockImplementation(async (o) => {
      keys.push(o?.queryKey as unknown[]);
    });
    const wrapper = ({ children }: { children: ReactNode }) =>
      createElement(QueryClientProvider, { client: qc }, children);
    const { unmount } = renderHook(() => useProjectRealtime(opts), { wrapper });
    return { qc, keys, unmount };
  }

  it("subscribes to the user's own user:{id} channel and notification.created there invalidates notifications", () => {
    const { keys, unmount } = mountHook({
      slug: "acme",
      identifier: "ENG",
      userId: "u-1",
    });
    try {
      const channels = [...wsMock.subscriptions.keys()];
      expect(channels).toContain("user:u-1");
      const wsHandler = wsMock.subscriptions.get("user:u-1");
      expect(wsHandler).toHaveLength(1);
      wsHandler![0](frame("notification.created", { id: "n1" }));
      expect(keys).toEqual([["notifications"]]);
    } finally {
      unmount();
    }
  });

  it("does NOT subscribe to a user channel when userId is omitted (auth loading / logged out)", () => {
    const { unmount } = mountHook({ slug: "acme", identifier: "ENG" });
    try {
      const channels = [...wsMock.subscriptions.keys()];
      expect(channels).toContain("project:acme:ENG");
      expect(channels).toContain("workspace:acme");
      expect(channels.filter((c) => c.startsWith("user:"))).toHaveLength(0);
    } finally {
      unmount();
    }
  });
});

describe("resyncProject", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  function stubProbe(results: unknown[]): void {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () =>
        new Response(JSON.stringify({ results, next_cursor: null }), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        }),
      ),
    );
  }

  it("with no `since`, invalidates everything (first connect)", async () => {
    const { qc, keys } = spiedClient();
    await resyncProject(qc, CTX, null);
    expect(keys).toContainEqual(["issues", "acme", "ENG"]);
    expect(keys).toContainEqual(["cycles", "acme", "ENG"]);
    expect(keys).toContainEqual(["intake", "acme", "ENG"]);
    expect(keys).toContainEqual(["modules", "acme", "ENG"]);
    expect(keys).toContainEqual(["module-issues", "acme", "ENG"]);
    expect(keys).toContainEqual(["pages", "acme", "ENG"]);
    expect(keys).toContainEqual(["page-revisions", "acme", "ENG"]);
    expect(keys).toContainEqual(["page-detail", "acme", "ENG"]);
    expect(keys).toContainEqual(["releases", "acme", "ENG"]);
    expect(keys).toContainEqual(["release-issues", "acme", "ENG"]);
    expect(keys).toContainEqual(["release-notes", "acme", "ENG"]);
    expect(keys).toContainEqual(["templates", "acme", "ENG"]);
    expect(keys).toContainEqual(["custom-fields", "acme", "ENG"]);
    expect(keys).toContainEqual(["initiatives", "acme"]);
    expect(keys).toContainEqual(["initiative", "acme"]);
    expect(keys).toContainEqual(["initiative-rollup", "acme"]);
    // C41T2: notifications stay out of resync — the bell refetches on open.
    expect(keys).not.toContainEqual(["notifications"]);
  });

  it("skips the issue caches when the delta probe is empty", async () => {
    stubProbe([]);
    const { qc, keys } = spiedClient();
    await resyncProject(qc, CTX, "2026-10-06T00:00:00Z");
    expect(keys).not.toContainEqual(["issues", "acme", "ENG"]);
    expect(keys).not.toContainEqual(["board-issues", "acme", "ENG"]);
    // Cycles/intake and the six new families are cheap — still resynced
    // unconditionally.
    expect(keys).toContainEqual(["cycles", "acme", "ENG"]);
    expect(keys).toContainEqual(["intake", "acme", "ENG"]);
    expect(keys).toContainEqual(["modules", "acme", "ENG"]);
    expect(keys).toContainEqual(["pages", "acme", "ENG"]);
    expect(keys).toContainEqual(["releases", "acme", "ENG"]);
    expect(keys).toContainEqual(["templates", "acme", "ENG"]);
    expect(keys).toContainEqual(["custom-fields", "acme", "ENG"]);
    expect(keys).toContainEqual(["initiatives", "acme"]);
    // The probe used the delta endpoint.
    const fetchMock = fetch as unknown as ReturnType<typeof vi.fn>;
    expect(fetchMock.mock.calls[0][0] as string).toContain("updated_after=");
  });

  it("invalidates issue caches when the delta probe finds changes", async () => {
    stubProbe([{ id: "uuid-9" }]);
    const { qc, keys } = spiedClient();
    await resyncProject(qc, CTX, "2026-10-06T00:00:00Z");
    expect(keys).toContainEqual(["issues", "acme", "ENG"]);
    expect(keys).toContainEqual(["issue", "acme", "ENG"]);
  });

  it("resyncs everything when the probe fails", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => new Response("boom", { status: 500 })),
    );
    const { qc, keys } = spiedClient();
    await resyncProject(qc, CTX, "2026-10-06T00:00:00Z");
    expect(keys).toContainEqual(["issues", "acme", "ENG"]);
  });
});
