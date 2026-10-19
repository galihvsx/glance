// @vitest-environment jsdom
// Tests for the realtime → TanStack Query invalidation map and the
// ?updated_after= reconnect resync (Task 25, cycle 41: six *.updated
// handlers + workspace subscription; C41T2: notification.created handler
// + user-channel subscription; C43T0: WorkspaceScope hook, objective
// handler, resyncWorkspace, double-mount safety).

import { afterEach, describe, expect, it, vi } from "vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderHook } from "@testing-library/react";
import { createElement, type ReactNode } from "react";
import {
  handleRealtimeEvent,
  handleReconnect,
  registerGlobalReconnect,
  registerProjectReconnect,
  registerWorkspaceReconnect,
  resyncNotifications,
  resyncProject,
  resyncWorkspace,
  useGlobalRealtime,
  useProjectRealtime,
  useWorkspaceRealtime,
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
      return () => {
        const live = subscriptions.get(channel);
        if (!live) return;
        const i = live.indexOf(handler);
        if (i >= 0) live.splice(i, 1);
      };
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

  it("epic.updated invalidates epics list, detail, and issues", () => {
    const { qc, keys } = spiedClient();
    handleRealtimeEvent(qc, CTX, frame("epic.updated", { id: "epic-1" }));
    expect(keys).toEqual([
      ["epics", "acme", "ENG"],
      ["epic-detail", "acme", "ENG", "epic-1"],
      ["epic-issues", "acme", "ENG", "epic-1"],
    ]);
  });

  it("epic.updated without an id invalidates only the epics list", () => {
    const { qc, keys } = spiedClient();
    handleRealtimeEvent(qc, CTX, frame("epic.updated", {}));
    expect(keys).toEqual([["epics", "acme", "ENG"]]);
  });

  it("notification.created invalidates ONLY the notifications prefix", () => {
    const { qc, keys } = spiedClient();
    handleRealtimeEvent(qc, CTX, frame("notification.created", { id: "n1" }));
    expect(keys).toEqual([["notifications"]]);
  });

  it("objective.updated invalidates objectives list, objective detail, and kr-checkins prefix", () => {
    // C43T0: synthetic event — the backend broadcast lands in C43T1;
    // the case is implemented from the cross-task contract alone.
    const { qc, keys } = spiedClient();
    handleRealtimeEvent(qc, CTX, frame("objective.updated", { id: "obj-1" }));
    expect(keys).toEqual([
      ["objectives", "acme"],
      ["objective", "acme", "obj-1"],
      ["kr-checkins", "acme", "obj-1"],
    ]);
  });

  it("objective.updated without an id invalidates only the objectives list", () => {
    const { qc, keys } = spiedClient();
    handleRealtimeEvent(qc, CTX, frame("objective.updated", {}));
    expect(keys).toEqual([["objectives", "acme"]]);
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

describe("useWorkspaceRealtime subscription set (C43T0)", () => {
  afterEach(() => {
    wsMock.subscriptions.clear();
    vi.clearAllMocks();
  });

  function mountHook(opts: { slug: string; userId?: string }) {
    const qc = new QueryClient();
    const keys: unknown[][] = [];
    vi.spyOn(qc, "invalidateQueries").mockImplementation(async (o) => {
      keys.push(o?.queryKey as unknown[]);
    });
    const wrapper = ({ children }: { children: ReactNode }) =>
      createElement(QueryClientProvider, { client: qc }, children);
    const { unmount } = renderHook(() => useWorkspaceRealtime(opts), {
      wrapper,
    });
    return { qc, keys, unmount };
  }

  it("subscribes to workspace + user channels, never the project channel", () => {
    const { keys, unmount } = mountHook({ slug: "acme", userId: "u-1" });
    try {
      const channels = [...wsMock.subscriptions.keys()];
      expect(channels).toContain("workspace:acme");
      expect(channels).toContain("user:u-1");
      expect(channels.filter((c) => c.startsWith("project:"))).toHaveLength(0);
      // Workspace-channel events invalidate workspace keys…
      wsMock.subscriptions
        .get("workspace:acme")![0](frame("objective.updated", { id: "obj-1" }));
      expect(keys).toContainEqual(["objectives", "acme"]);
      expect(keys).toContainEqual(["objective", "acme", "obj-1"]);
      expect(keys).toContainEqual(["kr-checkins", "acme", "obj-1"]);
      // …and user-channel events invalidate notifications.
      keys.length = 0;
      wsMock.subscriptions
        .get("user:u-1")![0](frame("notification.created", { id: "n1" }));
      expect(keys).toEqual([["notifications"]]);
    } finally {
      unmount();
    }
  });

  it("does NOT subscribe to a user channel when userId is omitted (auth loading / logged out)", () => {
    const { unmount } = mountHook({ slug: "acme" });
    try {
      const channels = [...wsMock.subscriptions.keys()];
      expect(channels).toContain("workspace:acme");
      expect(channels.filter((c) => c.startsWith("user:"))).toHaveLength(0);
    } finally {
      unmount();
    }
  });
});

describe("resyncWorkspace (C43T0)", () => {
  it("invalidates workspace-level keys ONLY — never project keys", async () => {
    const { qc, keys } = spiedClient();
    await resyncWorkspace(qc, { slug: "acme" }, "2026-10-06T00:00:00Z");
    expect(keys).toEqual([
      ["initiatives", "acme"],
      ["initiative", "acme"],
      ["initiative-rollup", "acme"],
      ["objectives", "acme"],
      ["objective", "acme"],
      ["kr-checkins", "acme"],
      ["notifications"],
      ["workload", "acme"],
      ["risk", "acme"],
      ["mywork", "acme"],
      ["projects", "acme"],
      ["workspace", "acme"],
      ["members", "acme"],
      ["webhooks", "acme"],
      ["project-templates", "acme"],
      ["workspace-audit-log", "acme"],
    ]);
    // Explicitly: no project key may leak in — every key is either the
    // bare ["notifications"] prefix or slug-scoped with NO identifier.
    const projectKeys: unknown[][] = [
      ["issues", "acme", "ENG"],
      ["board-issues", "acme", "ENG"],
      ["issue", "acme", "ENG"],
      ["history", "acme", "ENG"],
      ["comments", "acme", "ENG"],
      ["cycles", "acme", "ENG"],
      ["cycle-issues", "acme", "ENG"],
      ["intake", "acme", "ENG"],
      ["modules", "acme", "ENG"],
      ["module-issues", "acme", "ENG"],
      ["pages", "acme", "ENG"],
      ["page-revisions", "acme", "ENG"],
      ["page-detail", "acme", "ENG"],
      ["releases", "acme", "ENG"],
      ["release-issues", "acme", "ENG"],
      ["release-notes", "acme", "ENG"],
      ["templates", "acme", "ENG"],
      ["custom-fields", "acme", "ENG"],
    ];
    for (const k of projectKeys) expect(keys).not.toContainEqual(k);
    expect(keys.every((k) => k.length <= 2 || k[0] === "notifications")).toBe(
      true,
    );
  });

  it("is unconditional: same key set with a null `since` (first connect)", async () => {
    const { qc, keys } = spiedClient();
    await resyncWorkspace(qc, { slug: "acme" }, null);
    expect(keys).toContainEqual(["objectives", "acme"]);
    expect(keys).toContainEqual(["notifications"]);
    expect(keys).toHaveLength(16);
  });
});

describe("useGlobalRealtime subscription set (C44T0)", () => {
  afterEach(() => {
    wsMock.subscriptions.clear();
    vi.clearAllMocks();
  });

  function mountHook(opts: { userId?: string }) {
    const qc = new QueryClient();
    const keys: unknown[][] = [];
    vi.spyOn(qc, "invalidateQueries").mockImplementation(async (o) => {
      keys.push(o?.queryKey as unknown[]);
    });
    const wrapper = ({ children }: { children: ReactNode }) =>
      createElement(QueryClientProvider, { client: qc }, children);
    const { unmount } = renderHook(() => useGlobalRealtime(opts), {
      wrapper,
    });
    return { qc, keys, unmount };
  }

  it("subscribes ONLY to the user channel and notification.created there invalidates notifications", () => {
    const { keys, unmount } = mountHook({ userId: "u-1" });
    try {
      expect([...wsMock.subscriptions.keys()]).toEqual(["user:u-1"]);
      wsMock.subscriptions
        .get("user:u-1")![0](frame("notification.created", { id: "n1" }));
      expect(keys).toEqual([["notifications"]]);
    } finally {
      unmount();
    }
  });

  it("does NOT subscribe to any channel when userId is omitted (auth loading / logged out)", () => {
    const { unmount } = mountHook({});
    try {
      expect([...wsMock.subscriptions.keys()]).toHaveLength(0);
    } finally {
      unmount();
    }
  });
});

describe("resyncNotifications (C44T0)", () => {
  it("invalidates ONLY the notifications prefix", async () => {
    const { qc, keys } = spiedClient();
    await resyncNotifications(qc);
    expect(keys).toEqual([["notifications"]]);
  });
});

describe("handleReconnect dispatch (C43T0 review fix)", () => {
  it("workspace-only registration resyncs workspace keys, never project keys", async () => {
    const { qc, keys } = spiedClient();
    const unregister = registerWorkspaceReconnect({
      queryClient: qc,
      slug: "acme",
    });
    try {
      await handleReconnect(null);
      expect(keys).toContainEqual(["objectives", "acme"]);
      expect(keys).toContainEqual(["notifications"]);
      expect(keys).not.toContainEqual(["issues", "acme", "ENG"]);
      expect(keys).not.toContainEqual(["cycles", "acme", "ENG"]);
    } finally {
      unregister();
    }
  });

  it("both registrations resync both key sets", async () => {
    const { qc, keys } = spiedClient();
    const unregisterWs = registerWorkspaceReconnect({
      queryClient: qc,
      slug: "acme",
    });
    const unregisterPr = registerProjectReconnect({
      queryClient: qc,
      slug: "acme",
      identifier: "ENG",
    });
    try {
      await handleReconnect(null);
      expect(keys).toContainEqual(["issues", "acme", "ENG"]);
      expect(keys).toContainEqual(["objectives", "acme"]);
    } finally {
      unregisterWs();
      unregisterPr();
    }
  });

  it("stale cleanup never clears a newer registration", async () => {
    const { qc, keys } = spiedClient();
    const unregisterOld = registerWorkspaceReconnect({
      queryClient: qc,
      slug: "acme",
    });
    const unregisterNew = registerWorkspaceReconnect({
      queryClient: qc,
      slug: "beta",
    });
    try {
      unregisterOld(); // stale cleanup: must NOT clear the beta registration
      await handleReconnect(null);
      expect(keys).toContainEqual(["objectives", "beta"]);
      expect(keys).not.toContainEqual(["objectives", "acme"]);
    } finally {
      unregisterNew();
    }
  });

  it("after unregister, reconnect resyncs nothing", async () => {
    const { qc, keys } = spiedClient();
    const unregister = registerWorkspaceReconnect({
      queryClient: qc,
      slug: "acme",
    });
    unregister();
    await handleReconnect(null);
    expect(keys).toHaveLength(0);
  });

  it("C44T0: global-only registration resyncs ONLY the notifications keys", async () => {
    const { qc, keys } = spiedClient();
    const unregister = registerGlobalReconnect({ queryClient: qc });
    try {
      await handleReconnect(null);
      expect(keys).toEqual([["notifications"]]);
    } finally {
      unregister();
    }
  });

  it("C44T0: global + workspace + project registrations compose — all three key sets resync", async () => {
    // Regression guard for the C43T0 dispatcher fix, extended to the
    // global callback: one stable handleReconnect must run every
    // registered resync, regardless of mount order.
    const { qc, keys } = spiedClient();
    const unregisterGlobal = registerGlobalReconnect({ queryClient: qc });
    const unregisterWs = registerWorkspaceReconnect({
      queryClient: qc,
      slug: "acme",
    });
    const unregisterPr = registerProjectReconnect({
      queryClient: qc,
      slug: "acme",
      identifier: "ENG",
    });
    try {
      await handleReconnect(null);
      expect(keys).toContainEqual(["issues", "acme", "ENG"]);
      expect(keys).toContainEqual(["objectives", "acme"]);
      expect(keys).toContainEqual(["notifications"]);
    } finally {
      unregisterGlobal();
      unregisterWs();
      unregisterPr();
    }
  });

  it("C44T0: stale global cleanup never clears a newer registration", async () => {
    const { qc, keys } = spiedClient();
    const unregisterOld = registerGlobalReconnect({ queryClient: qc });
    const qcNew = new QueryClient();
    const keysNew: unknown[][] = [];
    vi.spyOn(qcNew, "invalidateQueries").mockImplementation(async (o) => {
      keysNew.push(o?.queryKey as unknown[]);
    });
    const unregisterNew = registerGlobalReconnect({ queryClient: qcNew });
    try {
      unregisterOld(); // stale cleanup: must NOT clear the new registration
      await handleReconnect(null);
      expect(keys).toHaveLength(0);
      expect(keysNew).toEqual([["notifications"]]);
    } finally {
      unregisterNew();
    }
  });

  it("C44T0: after global unregister, reconnect resyncs nothing", async () => {
    const { qc, keys } = spiedClient();
    const unregister = registerGlobalReconnect({ queryClient: qc });
    unregister();
    await handleReconnect(null);
    expect(keys).toHaveLength(0);
  });
});

describe("double-mount refcount safety (C43T0)", () => {
  afterEach(() => {
    wsMock.subscriptions.clear();
    vi.clearAllMocks();
  });

  it("WorkspaceScope + ProjectScope mounted together: handler runs, unmounting one leaves the other live", () => {
    const qc = new QueryClient();
    const keys: unknown[][] = [];
    vi.spyOn(qc, "invalidateQueries").mockImplementation(async (o) => {
      keys.push(o?.queryKey as unknown[]);
    });
    const wrapper = ({ children }: { children: ReactNode }) =>
      createElement(QueryClientProvider, { client: qc }, children);
    // WorkspaceScope's mount (workspace + user channels)…
    const wsMount = renderHook(
      () => useWorkspaceRealtime({ slug: "acme", userId: "u-1" }),
      { wrapper },
    );
    // …plus ProjectScope's mount (project + workspace + user channels).
    const projMount = renderHook(
      () =>
        useProjectRealtime({ slug: "acme", identifier: "ENG", userId: "u-1" }),
      { wrapper },
    );
    try {
      // Both hooks registered a handler on the shared workspace channel.
      expect(wsMock.subscriptions.get("workspace:acme")).toHaveLength(2);
      expect(wsMock.subscriptions.get("user:u-1")).toHaveLength(2);
      expect(wsMock.subscriptions.get("project:acme:ENG")).toHaveLength(1);

      // One event on the shared channel runs the handler (no crash,
      // idempotent duplicate invalidation — the documented double
      // delivery). Wire-level "one subscribe per channel" is covered
      // by ws.test.ts ("sends subscribe … once per channel").
      wsMock.subscriptions
        .get("workspace:acme")!
        .forEach((h) => h(frame("initiative.updated", { id: "init-1" })));
      expect(keys).toContainEqual(["initiatives", "acme"]);

      // Unmounting the workspace hook leaves the project hook's handler.
      wsMount.unmount();
      expect(wsMock.subscriptions.get("workspace:acme")).toHaveLength(1);
      keys.length = 0;
      wsMock.subscriptions
        .get("workspace:acme")!
        .forEach((h) => h(frame("initiative.updated", { id: "init-1" })));
      expect(keys).toContainEqual(["initiatives", "acme"]);
    } finally {
      projMount.unmount();
    }
    expect(wsMock.subscriptions.get("workspace:acme")).toHaveLength(0);
    expect(wsMock.subscriptions.get("project:acme:ENG")).toHaveLength(0);
    expect(wsMock.subscriptions.get("user:u-1")).toHaveLength(0);
  });
});
