// @vitest-environment jsdom
import { cleanup, render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, describe, expect, it, vi } from "vitest";
import ActivitySection from "./ActivitySection";
import { api } from "../../lib/api";
import type { HistoryEntry, IssueState } from "../../lib/types";

vi.mock("../../lib/api", () => ({
  api: {
    get: vi.fn(),
    post: vi.fn(),
  },
}));

const mockApiGet = vi.mocked(api.get);

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

const now = new Date().toISOString();

function makeState(id: string, name: string): IssueState {
  return {
    id,
    project_id: "p-1",
    name,
    group: "started",
    color: "#4f8ef7",
    sequence: 2,
    created_at: now,
    updated_at: now,
  };
}

const states = [makeState("s-backlog", "Backlog"), makeState("s-inprog", "In Progress")];

function makeEntry(over: Partial<HistoryEntry> & { id: string }): HistoryEntry {
  return {
    field: "state_id",
    old_value: "s-backlog",
    new_value: "s-inprog",
    actor: { id: "u-ada", name: "Ada", email: "ada@example.com" },
    created_at: now,
    ...over,
  } as HistoryEntry;
}

function renderTransition(history: HistoryEntry[]) {
  mockApiGet.mockImplementation((path: string) => {
    if (path.includes("/comments")) return Promise.resolve({ comments: [] });
    if (path.includes("/history")) return Promise.resolve({ history });
    if (path.includes("/members")) return Promise.resolve({ members: [] });
    return Promise.reject(new Error("unexpected path: " + path));
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
        userId="u-ada"
        states={states}
        activeTab="transition"
        onTabChange={() => {}}
        onError={() => {}}
      />
    </QueryClientProvider>,
  );
}

describe("Transition tab time-in-state pill", () => {
  it("renders the duration pill on a row carrying time_in_state_seconds", async () => {
    renderTransition([
      makeEntry({ id: "e1", time_in_state_seconds: 2 * 86400 + 3 * 3600 }),
    ]);
    await screen.findByText("2d 3h");
    expect(screen.getByTitle("Time spent in In Progress")).toBeTruthy();
  });

  it("hides the pill on a row without the field", async () => {
    renderTransition([
      makeEntry({ id: "e1" }),
      makeEntry({ id: "e2", time_in_state_seconds: 45 }),
    ]);
    await screen.findByText("45s");
    // Only the one pill exists; no duration text for the field-less row.
    expect(screen.getAllByTitle(/^Time spent in /)).toHaveLength(1);
  });
});
