// @vitest-environment jsdom
// C43T0: WorkspaceScope owns the single ConnectionDot; ProjectScope must
// NOT render its own, so a project page (WorkspaceScope > ProjectScope
// nested) shows exactly one dot. Also asserts the nested subscription
// set at the layout level.

import { afterEach, describe, expect, it, vi } from "vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import { createElement, type ReactNode } from "react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import WorkspaceScope from "./WorkspaceScope";
import ProjectScope from "./ProjectScope";
import type { WsEventHandler } from "../lib/ws";

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

function renderAt(path: string, children: ReactNode) {
  const qc = new QueryClient();
  const wrapper = ({ children: c }: { children: ReactNode }) =>
    createElement(QueryClientProvider, { client: qc }, c);
  const utils = render(
    createElement(
      MemoryRouter,
      { initialEntries: [path] },
      children,
    ),
    { wrapper },
  );
  return utils;
}

describe("WorkspaceScope / ProjectScope ConnectionDot ownership (C43T0)", () => {
  afterEach(() => {
    wsMock.subscriptions.clear();
    vi.clearAllMocks();
  });

  it("a project page (WorkspaceScope > ProjectScope) shows exactly ONE ConnectionDot", () => {
    const { unmount } = renderAt(
      "/w/acme/p/ENG",
      createElement(
        Routes,
        null,
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
    );
    try {
      expect(screen.getByText("project page")).toBeTruthy();
      // ConnectionDot renders aria-label "Realtime disconnected" while
      // the mocked client reports status "closed".
      expect(
        screen.getAllByLabelText("Realtime disconnected"),
      ).toHaveLength(1);
      // Both scopes subscribed the shared channels; the project channel
      // belongs to ProjectScope alone.
      expect(wsMock.subscriptions.get("workspace:acme")).toHaveLength(2);
      expect(wsMock.subscriptions.get("user:u-1")).toHaveLength(2);
      expect(wsMock.subscriptions.get("project:acme:ENG")).toHaveLength(1);
    } finally {
      unmount();
    }
  });

  it("a workspace page (WorkspaceScope alone) shows exactly one ConnectionDot and no project subscription", () => {
    const { unmount } = renderAt(
      "/w/acme/goals",
      createElement(
        Routes,
        null,
        createElement(
          Route,
          { element: createElement(WorkspaceScope) },
          createElement(Route, {
            path: "/w/:slug/goals",
            element: createElement("div", null, "goals page"),
          }),
        ),
      ),
    );
    try {
      expect(screen.getByText("goals page")).toBeTruthy();
      expect(
        screen.getAllByLabelText("Realtime disconnected"),
      ).toHaveLength(1);
      expect(wsMock.subscriptions.get("workspace:acme")).toHaveLength(1);
      expect(wsMock.subscriptions.get("user:u-1")).toHaveLength(1);
      expect(
        [...wsMock.subscriptions.keys()].filter((c) =>
          c.startsWith("project:"),
        ),
      ).toHaveLength(0);
    } finally {
      unmount();
    }
  });
});
