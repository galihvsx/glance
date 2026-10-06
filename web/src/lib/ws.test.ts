// Unit tests for the realtime WS client (Task 25): channel builders,
// backoff, frame parsing, and client subscribe/reconnect behavior against
// a fake WebSocket.

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  RealtimeClient,
  backoffDelayMs,
  issueChannel,
  parseFrame,
  projectChannel,
  userChannel,
  workspaceChannel,
  type WsEventFrame,
} from "./ws";

describe("channel builders", () => {
  it("builds workspace-qualified project channels (R11)", () => {
    expect(projectChannel("acme", "ENG")).toBe("project:acme:ENG");
    // The bare project:ENG scheme leaked across workspaces — must not appear.
    expect(projectChannel("acme", "ENG")).not.toBe("project:ENG");
  });
  it("builds the other channels", () => {
    expect(workspaceChannel("acme")).toBe("workspace:acme");
    expect(issueChannel("uuid-1")).toBe("issue:uuid-1");
    expect(userChannel("user-1")).toBe("user:user-1");
  });
});

describe("backoffDelayMs", () => {
  it("doubles from 1s and caps at 30s", () => {
    expect(backoffDelayMs(0)).toBe(1000);
    expect(backoffDelayMs(1)).toBe(2000);
    expect(backoffDelayMs(2)).toBe(4000);
    expect(backoffDelayMs(10)).toBe(30_000);
    expect(backoffDelayMs(100)).toBe(30_000);
  });
  it("clamps negative/NaN attempts", () => {
    expect(backoffDelayMs(-3)).toBe(1000);
  });
});

describe("parseFrame", () => {
  it("parses event frames", () => {
    const f = parseFrame(
      '{"event":"issue.created","channel":"project:acme:ENG","data":{"id":"1"},"at":"2026-10-06T00:00:00Z"}',
    );
    expect(f).toEqual({
      event: "issue.created",
      channel: "project:acme:ENG",
      data: { id: "1" },
      at: "2026-10-06T00:00:00Z",
    });
  });
  it("parses control frames", () => {
    expect(parseFrame('{"action":"subscribed","channel":"project:acme:ENG"}')).toEqual({
      action: "subscribed",
      channel: "project:acme:ENG",
    });
    expect(parseFrame('{"action":"error","message":"forbidden channel"}')).toEqual({
      action: "error",
      message: "forbidden channel",
    });
  });
  it("returns null for garbage", () => {
    expect(parseFrame("not json")).toBeNull();
    expect(parseFrame('{"foo":"bar"}')).toBeNull();
    expect(parseFrame("[1,2]")).toBeNull();
  });
});

// Fake WebSocket: the client under test talks to this instead of the network.
class FakeWS {
  static OPEN = 1;
  static CONNECTING = 0;
  static CLOSED = 3;
  static instances: FakeWS[] = [];

  url: string;
  readyState = FakeWS.CONNECTING;
  sent: string[] = [];
  onopen: (() => void) | null = null;
  onmessage: ((ev: { data: string }) => void) | null = null;
  onclose: (() => void) | null = null;
  onerror: (() => void) | null = null;

  constructor(url: string) {
    this.url = url;
    FakeWS.instances.push(this);
  }
  send(data: string): void {
    this.sent.push(data);
  }
  close(): void {
    this.readyState = FakeWS.CLOSED;
    this.onclose?.();
  }
  // --- test driver helpers ---
  open(): void {
    this.readyState = FakeWS.OPEN;
    this.onopen?.();
  }
  receive(raw: string): void {
    this.onmessage?.({ data: raw });
  }
  drop(): void {
    this.readyState = FakeWS.CLOSED;
    this.onclose?.();
  }
}

describe("RealtimeClient", () => {
  beforeEach(() => {
    FakeWS.instances = [];
    vi.stubGlobal("WebSocket", FakeWS);
    vi.useFakeTimers();
  });
  afterEach(() => {
    vi.unstubAllGlobals();
    vi.useRealTimers();
  });

  function frames(ws: FakeWS): unknown[] {
    return ws.sent.map((s) => JSON.parse(s));
  }

  it("sends subscribe for pre-registered channels on open", () => {
    const c = new RealtimeClient({ url: "ws://test/ws" });
    const h = vi.fn();
    c.subscribe("project:acme:ENG", h);
    c.connect();
    const ws = FakeWS.instances[0];
    expect(frames(ws)).toEqual([]); // not open yet
    ws.open();
    expect(frames(ws)).toEqual([
      { action: "subscribe", channel: "project:acme:ENG" },
    ]);
    c.disconnect();
  });

  it("sends subscribe immediately when already open, once per channel", () => {
    const c = new RealtimeClient({ url: "ws://test/ws" });
    c.connect();
    const ws = FakeWS.instances[0];
    ws.open();
    const h1 = vi.fn();
    const h2 = vi.fn();
    const un1 = c.subscribe("project:acme:ENG", h1);
    c.subscribe("project:acme:ENG", h2);
    expect(frames(ws)).toEqual([
      { action: "subscribe", channel: "project:acme:ENG" },
    ]);
    // Unsubscribing one of two handlers keeps the wire subscription.
    un1();
    expect(frames(ws)).toHaveLength(1);
    c.disconnect();
  });

  it("sends unsubscribe when the last handler leaves", () => {
    const c = new RealtimeClient({ url: "ws://test/ws" });
    c.connect();
    const ws = FakeWS.instances[0];
    ws.open();
    const un = c.subscribe("project:acme:ENG", vi.fn());
    un();
    expect(frames(ws)).toEqual([
      { action: "subscribe", channel: "project:acme:ENG" },
      { action: "unsubscribe", channel: "project:acme:ENG" },
    ]);
    c.disconnect();
  });

  it("dispatches event frames to channel handlers and tracks lastEventAt", () => {
    const c = new RealtimeClient({ url: "ws://test/ws" });
    const h = vi.fn();
    const other = vi.fn();
    c.subscribe("project:acme:ENG", h);
    c.subscribe("project:acme:OPS", other);
    c.connect();
    FakeWS.instances[0].open();
    FakeWS.instances[0].receive(
      '{"event":"issue.created","channel":"project:acme:ENG","data":{"id":"9"},"at":"2026-10-06T01:00:00Z"}',
    );
    expect(h).toHaveBeenCalledTimes(1);
    const evt = h.mock.calls[0][0] as WsEventFrame;
    expect(evt.event).toBe("issue.created");
    expect((evt.data as { id: string }).id).toBe("9");
    expect(other).not.toHaveBeenCalled();
    expect(c.lastReceivedAt).toBe("2026-10-06T01:00:00Z");
    // Control frames don't reach event handlers.
    FakeWS.instances[0].receive('{"action":"subscribed","channel":"project:acme:ENG"}');
    expect(h).toHaveBeenCalledTimes(1);
    c.disconnect();
  });

  it("reconnects with backoff, resubscribes, and fires onReconnect once", () => {
    const onReconnect = vi.fn();
    const c = new RealtimeClient({ url: "ws://test/ws", onReconnect });
    const statuses: string[] = [];
    c.onStatusChange((s) => statuses.push(s));
    c.subscribe("project:acme:ENG", vi.fn());
    c.connect();
    FakeWS.instances[0].open();
    expect(onReconnect).not.toHaveBeenCalled(); // first connect: no resync

    FakeWS.instances[0].drop();
    expect(c.connectionStatus).toBe("closed");
    // First reconnect after 1s.
    vi.advanceTimersByTime(999);
    expect(FakeWS.instances).toHaveLength(1);
    vi.advanceTimersByTime(1);
    expect(FakeWS.instances).toHaveLength(2);
    const ws2 = FakeWS.instances[1];
    ws2.open();
    expect(frames(ws2)).toEqual([
      { action: "subscribe", channel: "project:acme:ENG" },
    ]);
    expect(onReconnect).toHaveBeenCalledTimes(1);
    expect(onReconnect).toHaveBeenCalledWith(null); // no events seen yet
    expect(statuses).toContain("open");
    c.disconnect();
  });

  it("passes lastEventAt to onReconnect", () => {
    const onReconnect = vi.fn();
    const c = new RealtimeClient({ url: "ws://test/ws", onReconnect });
    c.subscribe("project:acme:ENG", vi.fn());
    c.connect();
    const ws1 = FakeWS.instances[0];
    ws1.open();
    ws1.receive(
      '{"event":"issue.updated","channel":"project:acme:ENG","data":{},"at":"2026-10-06T02:00:00Z"}',
    );
    ws1.drop();
    vi.advanceTimersByTime(1000);
    FakeWS.instances[1].open();
    expect(onReconnect).toHaveBeenCalledWith("2026-10-06T02:00:00Z");
    c.disconnect();
  });

  it("disconnect() stops reconnection", () => {
    const c = new RealtimeClient({ url: "ws://test/ws" });
    c.connect();
    FakeWS.instances[0].open();
    c.disconnect();
    expect(c.connectionStatus).toBe("closed");
    vi.advanceTimersByTime(60_000);
    expect(FakeWS.instances).toHaveLength(1);
  });

  it("appends a fresh ticket on every connect when ticketProvider is set", async () => {
    const tickets = ["t1", "t2"];
    const c = new RealtimeClient({
      url: "ws://test/ws",
      ticketProvider: async () => tickets.shift()!,
    });
    c.connect();
    await vi.advanceTimersByTimeAsync(0);
    expect(FakeWS.instances[0].url).toBe("ws://test/ws?ticket=t1");
    FakeWS.instances[0].open();
    FakeWS.instances[0].drop();
    await vi.advanceTimersByTimeAsync(1000);
    expect(FakeWS.instances[1].url).toBe("ws://test/ws?ticket=t2");
    c.disconnect();
  });
});
