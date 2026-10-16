// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, describe, expect, it, vi } from "vitest";
import ActivitySection from "./ActivitySection";
import { api } from "../../lib/api";
import type { CommentNode } from "../../lib/types";

vi.mock("../../lib/api", () => ({
  api: {
    get: vi.fn(),
    post: vi.fn(),
  },
}));

const mockApiGet = vi.mocked(api.get);
const mockApiPost = vi.mocked(api.post);

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

const doc = (text: string) => ({
  type: "doc",
  content: [{ type: "paragraph", content: [{ type: "text", text }] }],
});

const now = new Date().toISOString();

function makeComment(over: Partial<CommentNode> & { id: string }): CommentNode {
  return {
    issue_id: "issue-1",
    parent_id: null,
    actor: { id: "u-ada", name: "Ada", email: "ada@example.com" },
    content: doc("body"),
    created_at: now,
    updated_at: now,
    resolved_at: null,
    resolved_by: null,
    replies: [],
    ...over,
  } as CommentNode;
}

const openRoot1 = makeComment({
  id: "open-1",
  content: doc("open root one"),
  replies: [
    makeComment({
      id: "open-1-reply",
      parent_id: "open-1",
      actor: { id: "u-bo", name: "Bo", email: "bo@example.com" },
      content: doc("reply to open thread"),
    }),
  ],
});
const openRoot2 = makeComment({ id: "open-2", content: doc("open root two") });
const resolvedRoot = makeComment({
  id: "resolved-1",
  content: doc("resolved root"),
  resolved_at: now,
  resolved_by: { id: "u-bob", name: "Bob", email: "bob@example.com" },
  replies: [
    makeComment({
      id: "resolved-1-reply",
      parent_id: "resolved-1",
      actor: { id: "u-bo", name: "Bo", email: "bo@example.com" },
      content: doc("reply to resolved thread"),
    }),
  ],
});

const allRoots = [openRoot1, openRoot2, resolvedRoot];

function installGet() {
  mockApiGet.mockImplementation((path: string) => {
    if (path.includes("/comments")) {
      const m = /resolved=(all|open|resolved)/.exec(path);
      const filter = m?.[1] ?? "all";
      const roots =
        filter === "open"
          ? allRoots.filter((c) => !c.resolved_at)
          : filter === "resolved"
            ? allRoots.filter((c) => c.resolved_at)
            : allRoots;
      return Promise.resolve({ comments: roots });
    }
    if (path.includes("/history")) return Promise.resolve({ history: [] });
    if (path.includes("/members")) return Promise.resolve({ members: [] });
    return Promise.reject(new Error("unexpected path: " + path));
  });
  mockApiPost.mockImplementation(() =>
    Promise.resolve({ id: "x" } as unknown as CommentNode),
  );
}

function renderSection() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return render(
    <QueryClientProvider client={client}>
      <ActivitySection
        slug="acme"
        identifier="ENG"
        uuid="issue-1"
        userId="u-ada"
        states={[]}
        activeTab="comments"
        onTabChange={() => {}}
        onError={() => {}}
      />
    </QueryClientProvider>,
  );
}

function filterGroup() {
  return screen.getByRole("group", { name: "Filter comments by resolution" });
}

describe("ActivitySection comment resolve (C32)", () => {
  it("renders Resolve on open roots, Reopen on resolved roots, never on replies", async () => {
    installGet();
    renderSection();
    await screen.findByText("open root one");
    // Two open roots → two Resolve buttons; one resolved root → one Reopen.
    // The two replies render no resolve affordance at all.
    expect(screen.getAllByRole("button", { name: "Resolve" })).toHaveLength(2);
    expect(screen.getAllByRole("button", { name: "Reopen" })).toHaveLength(1);
  });

  it("shows the resolved badge with resolver and time", async () => {
    installGet();
    renderSection();
    await screen.findByText("open root one");
    expect(screen.getByText(/Resolved by Bob/)).toBeTruthy();
  });

  it("collapses resolved threads by default; expanding reveals the thread", async () => {
    installGet();
    renderSection();
    await screen.findByText("open root one");
    // Open thread's reply is visible; resolved thread's reply is hidden.
    expect(screen.getByText("reply to open thread")).toBeTruthy();
    expect(screen.queryByText("reply to resolved thread")).toBeNull();
    expect(screen.queryByText("resolved root")).toBeNull();

    fireEvent.click(screen.getByRole("button", { name: "Show thread" }));
    expect(screen.getByText("reply to resolved thread")).toBeTruthy();
    expect(screen.getByText("resolved root")).toBeTruthy();

    fireEvent.click(screen.getByRole("button", { name: "Hide thread" }));
    expect(screen.queryByText("reply to resolved thread")).toBeNull();
  });

  it("filters roots via ?resolved= and updates the open-thread count", async () => {
    installGet();
    renderSection();
    await screen.findByText("open root one");
    expect(screen.getByText("2 open threads")).toBeTruthy();

    fireEvent.click(within(filterGroup()).getByRole("button", { name: "Resolved" }));
    await waitFor(() => {
      expect(mockApiGet).toHaveBeenCalledWith(
        expect.stringContaining("resolved=resolved"),
      );
    });
    await waitFor(() => {
      expect(screen.queryByText("open root one")).toBeNull();
    });
    expect(screen.getByText("1 resolved thread")).toBeTruthy();
    // The resolved root is still listed (collapsed) under the filter.
    expect(screen.getByText(/Resolved by Bob/)).toBeTruthy();

    fireEvent.click(within(filterGroup()).getByRole("button", { name: "Open" }));
    await waitFor(() => {
      expect(mockApiGet).toHaveBeenCalledWith(
        expect.stringContaining("resolved=open"),
      );
    });
    await waitFor(() => {
      expect(screen.getByText("open root one")).toBeTruthy();
    });
    expect(screen.queryByText(/Resolved by Bob/)).toBeNull();
    expect(screen.getByText("2 open threads")).toBeTruthy();

    fireEvent.click(within(filterGroup()).getByRole("button", { name: "All" }));
    await waitFor(() => {
      expect(mockApiGet).toHaveBeenCalledWith(expect.stringContaining("resolved=all"));
    });
    await waitFor(() => {
      expect(screen.getByText(/Resolved by Bob/)).toBeTruthy();
    });
    expect(screen.getByText("open root one")).toBeTruthy();
  });

  it("posts to the resolve / reopen endpoints", async () => {
    installGet();
    renderSection();
    await screen.findByText("open root one");

    fireEvent.click(screen.getAllByRole("button", { name: "Resolve" })[0]);
    await waitFor(() => {
      expect(mockApiPost).toHaveBeenCalledWith(
        expect.stringContaining("/comments/open-1/resolve"),
      );
    });

    fireEvent.click(screen.getByRole("button", { name: "Reopen" }));
    await waitFor(() => {
      expect(mockApiPost).toHaveBeenCalledWith(
        expect.stringContaining("/comments/resolved-1/reopen"),
      );
    });
  });
});
