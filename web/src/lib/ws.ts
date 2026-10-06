// Realtime WebSocket client for the glance hub (spec §6, Task 25).
//
// Wire protocol (internal/realtime/events.go):
//   client → server: {"action":"subscribe"|"unsubscribe","channel":"..."}
//   server → client: {"action":"subscribed"|"unsubscribed","channel":"..."}
//                    {"action":"error","message":"..."}
//   server → client: {"event":"issue.created","channel":"project:acme:ENG",
//                     "data":{...},"at":"2026-10-06T..."}
// Channels are workspace-qualified (R11): project:{slug}:{identifier} —
// the bare project:{identifier} scheme leaked events across workspaces.
//
// Auth: the browser sends the session cookie automatically on the WS
// upgrade (same-origin /ws). A single-use ticket (?ticket=, minted at
// POST /api/v1/ws/ticket) is supported via ticketProvider for non-cookie
// clients, but the cookie path is preferred.

export type ConnectionStatus = "connecting" | "open" | "closed";

export interface WsEventFrame {
  event: string;
  channel: string;
  data: unknown;
  at: string;
}

export interface WsControlFrame {
  action: "subscribed" | "unsubscribed" | "error";
  channel?: string;
  message?: string;
}

export type WsFrame = WsEventFrame | WsControlFrame;

// Channel builders — mirror internal/service/broadcast.go exactly.
export function workspaceChannel(slug: string): string {
  return `workspace:${slug}`;
}
export function projectChannel(slug: string, identifier: string): string {
  return `project:${slug}:${identifier}`;
}
export function issueChannel(uuid: string): string {
  return `issue:${uuid}`;
}
export function userChannel(id: string): string {
  return `user:${id}`;
}

// backoffDelayMs: 1s, 2s, 4s, 8s … capped at 30s. attempt starts at 0.
export function backoffDelayMs(attempt: number): number {
  const a = Math.max(0, Math.floor(attempt));
  return Math.min(1000 * 2 ** a, 30_000);
}

// parseFrame classifies one server → client text frame. Returns null for
// malformed JSON or unrecognized shapes (never throws).
export function parseFrame(raw: string): WsFrame | null {
  let parsed: unknown;
  try {
    parsed = JSON.parse(raw);
  } catch {
    return null;
  }
  if (typeof parsed !== "object" || parsed === null) return null;
  const o = parsed as Record<string, unknown>;
  if (typeof o.event === "string" && typeof o.channel === "string") {
    return {
      event: o.event,
      channel: o.channel,
      data: o.data,
      at: typeof o.at === "string" ? o.at : "",
    };
  }
  if (typeof o.action === "string") {
    const frame: WsControlFrame = { action: o.action } as WsControlFrame;
    if (typeof o.channel === "string") frame.channel = o.channel;
    if (typeof o.message === "string") frame.message = o.message;
    return frame;
  }
  return null;
}

export type WsEventHandler = (evt: WsEventFrame) => void;
export type StatusHandler = (status: ConnectionStatus) => void;

export interface RealtimeClientOptions {
  /** Override the WS URL (default: same-origin /ws). */
  url?: string;
  /** Mint a single-use ticket for ?ticket= auth instead of the session
   *  cookie. Called fresh on every (re)connect — tickets are single-use. */
  ticketProvider?: () => Promise<string>;
  /** Called on every reconnect after the first, with the `at` of the last
   *  received event (null when none arrived yet). The app resyncs via the
   *  ?updated_after= delta endpoint (spec §6: no replay buffer). */
  onReconnect?: (since: string | null) => void;
}

function defaultUrl(): string {
  const proto = window.location.protocol === "https:" ? "wss:" : "ws:";
  return `${proto}//${window.location.host}/ws`;
}

export class RealtimeClient {
  private opts: RealtimeClientOptions;
  private ws: WebSocket | null = null;
  private subs = new Map<string, Set<WsEventHandler>>();
  private statusHandlers = new Set<StatusHandler>();
  private status: ConnectionStatus = "closed";
  private attempt = 0;
  private reconnectTimer: ReturnType<typeof setTimeout> | null = null;
  private manualClose = false;
  private everConnected = false;
  private lastEventAt: string | null = null;

  constructor(opts: RealtimeClientOptions = {}) {
    this.opts = opts;
  }

  /** Merge new options into the live client (used by getRealtimeClient). */
  updateOptions(opts: RealtimeClientOptions): void {
    this.opts = { ...this.opts, ...opts };
  }

  get connectionStatus(): ConnectionStatus {
    return this.status;
  }

  get lastReceivedAt(): string | null {
    return this.lastEventAt;
  }

  onStatusChange(cb: StatusHandler): () => void {
    this.statusHandlers.add(cb);
    cb(this.status);
    return () => {
      this.statusHandlers.delete(cb);
    };
  }

  private setStatus(s: ConnectionStatus): void {
    if (this.status === s) return;
    this.status = s;
    this.statusHandlers.forEach((h) => {
      try {
        h(s);
      } catch {
        // A status listener must never break the client.
      }
    });
  }

  connect(): void {
    if (this.ws || this.reconnectTimer) return; // connecting, open, or scheduled
    this.manualClose = false;
    void this.openSocket();
  }

  private async openSocket(): Promise<void> {
    this.setStatus("connecting");
    let url =
      this.opts.url ??
      (typeof window !== "undefined" ? defaultUrl() : "ws://localhost/ws");
    if (this.opts.ticketProvider) {
      try {
        const ticket = await this.opts.ticketProvider();
        url += `${url.includes("?") ? "&" : "?"}ticket=${encodeURIComponent(ticket)}`;
      } catch {
        // Ticket minting failed (e.g. session expired) — back off and retry;
        // a fresh ticket will be minted on the next attempt.
        this.scheduleReconnect();
        return;
      }
    }
    if (this.manualClose) return;
    const ws = new WebSocket(url);
    this.ws = ws;
    ws.onopen = () => {
      this.attempt = 0;
      this.setStatus("open");
      // Re-subscribe to everything — the server drops subscriptions with
      // the old connection.
      for (const channel of this.subs.keys()) {
        this.send({ action: "subscribe", channel });
      }
      const first = !this.everConnected;
      this.everConnected = true;
      if (!first) this.opts.onReconnect?.(this.lastEventAt);
    };
    ws.onmessage = (ev: MessageEvent) => {
      const frame =
        typeof ev.data === "string" ? parseFrame(ev.data) : null;
      if (!frame) return;
      if (!("event" in frame)) {
        if (frame.action === "error") {
          // Foreign-channel subscribe or similar — connection stays open.
          console.warn("[realtime] server error frame:", frame.message);
        }
        return;
      }
      if (frame.at) this.lastEventAt = frame.at;
      this.subs.get(frame.channel)?.forEach((h) => {
        try {
          h(frame);
        } catch {
          // A handler bug must never kill the socket.
        }
      });
    };
    ws.onerror = () => {
      // onclose follows with the details; nothing to do here.
    };
    ws.onclose = () => {
      if (this.ws === ws) this.ws = null;
      this.setStatus("closed");
      if (!this.manualClose) this.scheduleReconnect();
    };
  }

  private scheduleReconnect(): void {
    if (this.manualClose || this.reconnectTimer) return;
    const delay = backoffDelayMs(this.attempt);
    this.attempt += 1;
    this.reconnectTimer = setTimeout(() => {
      this.reconnectTimer = null;
      void this.openSocket();
    }, delay);
  }

  disconnect(): void {
    this.manualClose = true;
    if (this.reconnectTimer) {
      clearTimeout(this.reconnectTimer);
      this.reconnectTimer = null;
    }
    this.ws?.close();
    this.ws = null;
    this.setStatus("closed");
  }

  // subscribe registers a handler for one channel (refcounted: the wire
  // subscribe goes out once per channel no matter how many handlers).
  // Returns an unsubscribe function.
  subscribe(channel: string, handler: WsEventHandler): () => void {
    let set = this.subs.get(channel);
    if (!set) {
      set = new Set();
      this.subs.set(channel, set);
    }
    const first = set.size === 0;
    set.add(handler);
    if (first && this.ws && this.ws.readyState === WebSocket.OPEN) {
      this.send({ action: "subscribe", channel });
    }
    return () => {
      const s = this.subs.get(channel);
      if (!s) return;
      s.delete(handler);
      if (s.size === 0) {
        this.subs.delete(channel);
        if (this.ws && this.ws.readyState === WebSocket.OPEN) {
          this.send({ action: "unsubscribe", channel });
        }
      }
    };
  }

  private send(msg: unknown): void {
    try {
      this.ws?.send(JSON.stringify(msg));
    } catch {
      // Send failed — the reconnect path re-subscribes from scratch.
    }
  }
}

// Module singleton: one socket per tab. Options passed on later calls are
// merged in (the first caller doesn't permanently win).
let singleton: RealtimeClient | null = null;
export function getRealtimeClient(
  opts?: RealtimeClientOptions,
): RealtimeClient {
  if (!singleton) {
    singleton = new RealtimeClient(opts);
  } else if (opts) {
    singleton.updateOptions(opts);
  }
  return singleton;
}

/** Test-only: drop the singleton so tests start from a clean client. */
export function resetRealtimeClientForTests(): void {
  singleton?.disconnect();
  singleton = null;
}
