// @vitest-environment jsdom
import { cleanup, render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, describe, expect, it, vi } from "vitest";
import IssueProperties from "./IssueProperties";
import { api } from "../../lib/api";
import type { Issue } from "../../lib/types";

vi.mock("../../lib/api", () => ({
  api: {
    get: vi.fn(),
    post: vi.fn(),
    del: vi.fn(),
  },
  ApiError: class ApiError extends Error {},
}));

const mockApiGet = vi.mocked(api.get);

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

const now = new Date().toISOString();

function makeIssue(over: Partial<Issue> = {}): Issue {
  return {
    id: "issue-1",
    project_id: "p-1",
    sequence_id: 45,
    display_id: "ENG-45",
    name: "Fix the thing",
    priority: 2,
    state_id: "s-inprog",
    sort_order: 1,
    is_draft: false,
    created_by: "u-ada",
    created_at: now,
    updated_at: now,
    assignees: [],
    labels: [],
    ...over,
  } as Issue;
}

function renderProperties(issue: Issue) {
  mockApiGet.mockImplementation(() =>
    Promise.resolve({ cycles: [], modules: [], releases: [] }),
  );
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return render(
    <QueryClientProvider client={client}>
      <IssueProperties
        slug="acme"
        identifier="ENG"
        issue={issue}
        parentIssue={null}
        labels={[]}
        issueKey={["issue", "issue-1"]}
        onPatch={() => {}}
        onToggleLabel={() => {}}
        trackedSeconds={0}
        onOpenWorklogs={() => {}}
        onError={() => {}}
      />
    </QueryClientProvider>,
  );
}

describe("IssueProperties time in state", () => {
  it("renders the Time in state cell with the formatted duration", async () => {
    renderProperties(
      makeIssue({
        time_in_state_seconds: 3 * 86400 + 4 * 3600,
        state_entered_at: "2026-10-16T06:12:00Z",
      }),
    );
    expect(screen.getByText("Time in state")).toBeTruthy();
    expect(screen.getByText("3d 4h")).toBeTruthy();
  });

  it("shows a Since tooltip when state_entered_at is present", () => {
    renderProperties(
      makeIssue({
        time_in_state_seconds: 3600,
        state_entered_at: "2026-10-16T06:12:00Z",
      }),
    );
    const tip = screen.getByTitle(/^Since /);
    expect(tip).toBeTruthy();
  });

  it("renders the duration with no tooltip when state_entered_at is absent", () => {
    renderProperties(makeIssue({ time_in_state_seconds: 3600 }));
    expect(screen.getByText("1h 0m")).toBeTruthy();
    expect(screen.queryByTitle(/^Since /)).toBeNull();
  });

  it("hides the cell entirely when time_in_state_seconds is undefined", () => {
    renderProperties(makeIssue({}));
    expect(screen.queryByText("Time in state")).toBeNull();
  });

  it("hides the cell entirely when time_in_state_seconds is null", () => {
    renderProperties(makeIssue({ time_in_state_seconds: null }));
    expect(screen.queryByText("Time in state")).toBeNull();
  });
});
