// @vitest-environment jsdom
// C44T0: GlobalScope owns the single ConnectionDot for every
// authenticated route + the user:{id} subscription (notification.created
// arrives ONLY there). Asserts: exactly one dot for every nesting,
// user-only subscription on slug-less routes, and live
// notification.created invalidation via the global owner.

import { afterEach, describe, expect, it, vi } from "vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import { createElement, type ReactNode } from "react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import GlobalScope from "./GlobalScope";
import WorkspaceScope from "./WorkspaceScope";
import ProjectScope from "./ProjectScope";
import type { WsEventFrame, WsEventHandler } from "../lib/ws";

vi.mock("../lib/auth", () => ({
  useAuth: () => ({ user: { id: "u-1" } }),
}));

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
    onStatusChange: vi.fn((cb: (s: string) => void) => {
      cb("closed");
      return () => {};
    }),
  };
  return { subscriptions, client };
});
vi.mock("../lib/ws", () => ({
  getRealtimeClient: () => wsMock.client,
  workspaceChannel: (slug: string) => `workspace:${slug}`,
  projectChannel: (slug: string, identifier: string) =>
    `project:${slug}:${identifier}`,
  issueChannel: (uuid: string) => `issue:${uuid}`,
  userChannel: (id: string) => `user:${id}`,
}));

function renderAt(path: string, children: ReactNode, qc: QueryClient) {
  const wrapper = ({ children: c }: { children: ReactNode }) =>
    createElement(QueryClientProvider, { client: qc }, c);
  return render(createElement(MemoryRouter, { initialEntries: [path] }, children), {
    wrapper,
  });
}

function spiedClient(): { qc: QueryClient; keys: unknown[][] } {
  const qc = new QueryClient();
  const keys: unknown[][] = [];
  vi.spyOn(qc, "invalidateQueries").mockImplementation(async (opts) => {
    keys.push(opts?.queryKey as unknown[]);
  });
  return { qc, keys };
}

function frame(event: string, data: unknown): WsEventFrame {
  return { event, channel: "user:u-1", data, at: "2026-10-06T00:00:00Z" };
}

describe("GlobalScope (C44T0)", () => {
  afterEach(() => {
    wsMock.subscriptions.clear();
    vi.clearAllMocks();
  });

  it("a slug-less route (/notifications) shows exactly ONE ConnectionDot and subscribes ONLY the user channel", () => {
    const { qc, keys } = spiedClient();
    const { unmount } = renderAt(
      "/notifications",
      createElement(
        Routes,
        null,
        createElement(
          Route,
          { element: createElement(GlobalScope) },
          createElement(Route, {
            path: "/notifications",
            element: createElement("div", null, "notifications page"),
          }),
        ),
      ),
      qc,
    );
    try {
      expect(screen.getByText("notifications page")).toBeTruthy();
      // ConnectionDot renders aria-label "Realtime disconnected" while
      // the mocked client reports status "closed".
      expect(
        screen.getAllByLabelText("Realtime disconnected"),
      ).toHaveLength(1);
      expect([...wsMock.subscriptions.keys()]).toEqual(["user:u-1"]);
      // notification.created via the global owner invalidates the
      // notifications prefix — the same handleRealtimeEvent case the
      // workspace/project hooks use, no second handler.
      wsMock.subscriptions
        .get("user:u-1")![0](frame("notification.created", { id: "n1" }));
      expect(keys).toEqual([["notifications"]]);
    } finally {
      unmount();
    }
  });

  it("a workspace route (GlobalScope > WorkspaceScope) shows exactly ONE ConnectionDot", () => {
    const { qc } = spiedClient();
    const { unmount } = renderAt(
      "/w/acme/goals",
      createElement(
        Routes,
        null,
        createElement(
          Route,
          { element: createElement(GlobalScope) },
          createElement(
            Route,
            { element: createElement(WorkspaceScope) },
            createElement(Route, {
              path: "/w/:slug/goals",
              element: createElement("div", null, "goals page"),
            }),
          ),
        ),
      ),
      qc,
    );
    try {
      expect(screen.getByText("goals page")).toBeTruthy();
      expect(
        screen.getAllByLabelText("Realtime disconnected"),
      ).toHaveLength(1);
      // Global + workspace subscriptions: user channel x2 (benign
      // refcounted overlap), workspace channel x1, no project channel.
      expect(wsMock.subscriptions.get("user:u-1")).toHaveLength(2);
      expect(wsMock.subscriptions.get("workspace:acme")).toHaveLength(1);
      expect(
        [...wsMock.subscriptions.keys()].filter((c) =>
          c.startsWith("project:"),
        ),
      ).toHaveLength(0);
    } finally {
      unmount();
    }
  });

  it("a project page (GlobalScope > WorkspaceScope > ProjectScope) shows exactly ONE ConnectionDot", () => {
    const { qc } = spiedClient();
    const { unmount } = renderAt(
      "/w/acme/p/ENG",
      createElement(
        Routes,
        null,
        createElement(
          Route,
          { element: createElement(GlobalScope) },
          createElement(
            Route,
            { element: createElement(WorkspaceScope) },
            createElement(
              Route,
              { element: createElement(ProjectScope) },
              createElement(Route, {
                path: "/w/:slug/p/:identifier",
                element: createElement("div", null, "project page"),
              }),
            ),
          ),
        ),
      ),
      qc,
    );
    try {
      expect(screen.getByText("project page")).toBeTruthy();
      expect(
        screen.getAllByLabelText("Realtime disconnected"),
      ).toHaveLength(1);
      // Triple user subscription (global + workspace + project) —
      // refcounted-benign, one wire subscription, idempotent
      // invalidation. Workspace x2, project x1.
      expect(wsMock.subscriptions.get("user:u-1")).toHaveLength(3);
      expect(wsMock.subscriptions.get("workspace:acme")).toHaveLength(2);
      expect(wsMock.subscriptions.get("project:acme:ENG")).toHaveLength(1);
    } finally {
      unmount();
    }
  });

  it("unmounting GlobalScope drops its user-channel handler", () => {
    const { qc } = spiedClient();
    const { unmount } = renderAt(
      "/notifications",
      createElement(
        Routes,
        null,
        createElement(
          Route,
          { element: createElement(GlobalScope) },
          createElement(Route, {
            path: "/notifications",
            element: createElement("div", null, "notifications page"),
          }),
        ),
      ),
      qc,
    );
    expect(wsMock.subscriptions.get("user:u-1")).toHaveLength(1);
    unmount();
    expect(wsMock.subscriptions.get("user:u-1")).toHaveLength(0);
  });
});
