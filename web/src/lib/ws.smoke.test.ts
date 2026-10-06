// Live-server smoke test for the realtime WS client (Task 25).
//
// Gated: runs only when GLANCE_SMOKE is set (e.g. GLANCE_SMOKE=1 with
// GLANCE_SMOKE_URL=http://127.0.0.1:18080 and GLANCE_SMOKE_TOKEN=<a valid
// glance_session token>). Exercises the REAL RealtimeClient against a REAL
// server: ticket auth, subscribe, and an end-to-end issue.created event
// produced by a REST create.
//
// Seed the server DB first (see the Task 25 report for the SQL): a user
// with a session row for GLANCE_SMOKE_TOKEN, workspace slug `acme` with
// the user as admin, and project identifier `ENG`.

import { describe, expect, it } from "vitest";
import { RealtimeClient, type WsEventFrame } from "./ws";

// vitest runs in node, but the app tsconfig only includes vite/client
// types — declare the sliver of process we need (file-local, erasable).
declare const process: { env: Record<string, string | undefined> };

const SMOKE = process.env.GLANCE_SMOKE;
const BASE = process.env.GLANCE_SMOKE_URL ?? "http://127.0.0.1:18080";
const TOKEN = process.env.GLANCE_SMOKE_TOKEN ?? "";

const cookie = `glance_session=${TOKEN}`;

async function mintTicket(): Promise<string> {
  const res = await fetch(`${BASE}/api/v1/ws/ticket`, {
    method: "POST",
    headers: { Cookie: cookie },
  });
  if (!res.ok) throw new Error(`ticket mint failed: ${res.status}`);
  const body = (await res.json()) as { ticket: string };
  return body.ticket;
}

describe.skipIf(!SMOKE)("realtime smoke (live server)", () => {
  it("ticket auth → subscribe → issue.created over the real socket", async () => {
    const client = new RealtimeClient({
      url: `${BASE.replace(/^http/, "ws")}/ws`,
      ticketProvider: mintTicket,
    });
    try {
      const opened = new Promise<void>((resolve, reject) => {
        const timer = setTimeout(() => reject(new Error("open timeout")), 8000);
        const un = client.onStatusChange((s) => {
          if (s === "open") {
            clearTimeout(timer);
            un();
            resolve();
          }
        });
      });
      client.connect();
      await opened;

      const gotEvent = new Promise<WsEventFrame>((resolve, reject) => {
        const timer = setTimeout(() => reject(new Error("event timeout")), 8000);
        client.subscribe("project:acme:ENG", (evt) => {
          clearTimeout(timer);
          resolve(evt);
        });
      });

      const createRes = await fetch(
        `${BASE}/api/v1/workspaces/acme/projects/ENG/issues`,
        {
          method: "POST",
          headers: { "Content-Type": "application/json", Cookie: cookie },
          body: JSON.stringify({ name: "smoke test issue" }),
        },
      );
      expect(createRes.status).toBe(201);

      const evt = await gotEvent;
      expect(evt.event).toBe("issue.created");
      expect(evt.channel).toBe("project:acme:ENG");
      expect((evt.data as { name?: string }).name).toBe("smoke test issue");
      expect(typeof evt.at).toBe("string");
    } finally {
      client.disconnect();
    }
  }, 20000);

  it("foreign-channel subscribe gets an error frame, connection survives", async () => {
    const warnings: unknown[][] = [];
    const origWarn = console.warn;
    console.warn = (...args: unknown[]) => {
      warnings.push(args);
    };
    const client = new RealtimeClient({
      url: `${BASE.replace(/^http/, "ws")}/ws`,
      ticketProvider: mintTicket,
    });
    try {
      const opened = new Promise<void>((resolve, reject) => {
        const timer = setTimeout(() => reject(new Error("open timeout")), 8000);
        const un = client.onStatusChange((s) => {
          if (s === "open") {
            clearTimeout(timer);
            un();
            resolve();
          }
        });
      });
      client.connect();
      await opened;
      // `other` is a workspace the user is not a member of → error frame.
      client.subscribe("project:other:ENG", () => {});
      await new Promise((r) => setTimeout(r, 1500));
      expect(client.connectionStatus).toBe("open");
      expect(warnings.some((w) => String(w[0]).includes("[realtime]"))).toBe(true);
    } finally {
      console.warn = origWarn;
      client.disconnect();
    }
  }, 20000);
});
