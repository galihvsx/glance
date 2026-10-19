// @vitest-environment jsdom
// C71T1: the issue activity feed renders the epic_* rows the backend
// writes (attach/detach/archive/unarchive), with a graceful fallback
// when new_value is missing or malformed.
import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import ActivitySection from "./ActivitySection";
import { api } from "../../lib/api";
import type { HistoryEntry } from "../../lib/types";

vi.mock("../../lib/api", () => ({
  api: {
    get: vi.fn(),
    post: vi.fn(),
    patch: vi.fn(),
    del: vi.fn(),
  },
}));

const mockApiGet = vi.mocked(api.get);

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

const now = "2026-10-19T12:00:00Z";
const actor = { id: "u-galih", name: "Galih", email: "galih@example.com" };

function entry(over: Partial<HistoryEntry> & { id: string; field: string }): HistoryEntry {
  return {
    old_value: null,
    new_value: null,
    created_at: now,
    actor,
    ...over,
  } as HistoryEntry;
}

function renderWithHistory(history: HistoryEntry[]) {
  mockApiGet.mockImplementation((path: string) => {
    if (path.includes("/comments")) return Promise.resolve({ comments: [] });
    if (path.includes("/history")) return Promise.resolve({ history });
    if (path.includes("/members")) return Promise.resolve({ members: [] });
    return Promise.reject(new Error(`unexpected GET ${path}`));
  });
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return render(
    <QueryClientProvider client={client}>
      <ActivitySection
        slug="acme"
        identifier="ENG"
        uuid="issue-1"
        userId="u-galih"
        states={[]}
        activeTab="activity"
        onTabChange={() => {}}
        onError={() => {}}
      />
    </QueryClientProvider>,
  );
}

describe("ActivitySection epic activity rows (C71T1)", () => {
  it("renders epic_added as 'attached epic <name>'", async () => {
    renderWithHistory([
      entry({
        id: "h1",
        field: "epic_added",
        new_value: JSON.stringify({ epic_id: "epic-1", epic_name: "Auth revamp" }),
      }),
    ]);
    expect(await screen.findByText("attached epic")).toBeTruthy();
    expect(screen.getByText("Auth revamp")).toBeTruthy();
  });

  it("renders epic_removed as 'detached epic <name>'", async () => {
    renderWithHistory([
      entry({
        id: "h1",
        field: "epic_removed",
        new_value: { epic_id: "epic-1", epic_name: "Auth revamp" },
      }),
    ]);
    expect(await screen.findByText("detached epic")).toBeTruthy();
  });

  it("renders epic_archived / epic_unarchived", async () => {
    renderWithHistory([
      entry({
        id: "h1",
        field: "epic_archived",
        new_value: JSON.stringify({ epic_id: "epic-1", epic_name: "Old epic" }),
      }),
      entry({
        id: "h2",
        field: "epic_unarchived",
        new_value: JSON.stringify({ epic_id: "epic-1", epic_name: "Old epic" }),
      }),
    ]);
    expect(await screen.findByText("archived epic")).toBeTruthy();
    expect(screen.getByText("restored epic")).toBeTruthy();
  });

  it("falls back gracefully when new_value is missing or malformed", async () => {
    renderWithHistory([
      entry({ id: "h1", field: "epic_added", new_value: null }),
      entry({ id: "h2", field: "epic_removed", new_value: "not-json{{" }),
    ]);
    expect(await screen.findByText("attached epic")).toBeTruthy();
    // Missing name renders the em-dash placeholder, never "[object Object]".
    expect(screen.getAllByText("—").length).toBeGreaterThan(0);
    expect(document.body.textContent).not.toContain("[object Object]");
  });
});
