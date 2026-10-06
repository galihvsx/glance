// Tests for the realtime → TanStack Query invalidation map and the
// ?updated_after= reconnect resync (Task 25).

import { afterEach, describe, expect, it, vi } from "vitest";
import { QueryClient } from "@tanstack/react-query";
import {
  handleRealtimeEvent,
  resyncProject,
} from "./realtime";
import type { WsEventFrame } from "./ws";

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

  it("ignores unknown future events", () => {
    const { qc, keys } = spiedClient();
    handleRealtimeEvent(qc, CTX, frame("notification.created", { id: "n1" }));
    expect(keys).toHaveLength(0);
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
  });

  it("skips the issue caches when the delta probe is empty", async () => {
    stubProbe([]);
    const { qc, keys } = spiedClient();
    await resyncProject(qc, CTX, "2026-10-06T00:00:00Z");
    expect(keys).not.toContainEqual(["issues", "acme", "ENG"]);
    expect(keys).not.toContainEqual(["board-issues", "acme", "ENG"]);
    // Cycles/intake are cheap — still resynced unconditionally.
    expect(keys).toContainEqual(["cycles", "acme", "ENG"]);
    expect(keys).toContainEqual(["intake", "acme", "ENG"]);
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
