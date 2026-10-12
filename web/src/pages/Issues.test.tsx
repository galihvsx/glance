// @vitest-environment jsdom
// C26T3: the create-issue dialog opens with a prefilled target (due) date
// when navigated to with location.state { newIssue: true,
// newIssueTargetDate }, and creating invalidates the calendar caches so the
// new chip appears on its day.
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";
import Issues from "./Issues";
import type { Issue } from "../lib/types";

const BASE = "/api/v1/workspaces/acme/projects/ENG";

function mkIssue(over: Partial<Issue> & { id: string }): Issue {
  return {
    project_id: "proj-1",
    sequence_id: 9,
    display_id: "ENG-9",
    name: "test",
    priority: 0,
    state_id: "s",
    sort_order: 0,
    is_draft: false,
    created_by: "u",
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    assignees: [],
    labels: [],
    ...over,
  };
}

const mockState = vi.hoisted(() => ({
  posted: [] as { path: string; body: unknown }[],
  toastAdd: vi.fn(),
}));

vi.mock("../lib/api", () => ({
  api: {
    get: vi.fn((path: string) => {
      if (path.includes("/states")) return Promise.resolve({ states: [] });
      if (path.includes("/templates")) return Promise.resolve({ templates: [] });
      if (path.includes("/estimates")) return Promise.resolve({ estimates: [] });
      if (path.includes("/labels")) return Promise.resolve({ labels: [] });
      if (path.includes("/custom-fields"))
        return Promise.resolve({ custom_fields: [] });
      if (path.includes("/views")) return Promise.resolve({ views: [] });
      if (path.includes("/members")) return Promise.resolve({ members: [] });
      if (path.includes("/issues/similar")) return Promise.resolve([]);
      if (path.includes("/issues"))
        return Promise.resolve({ results: [], next_cursor: undefined });
      if (path === BASE)
        return Promise.resolve({
          id: "proj-1",
          name: "ENG",
          identifier: "ENG",
          created_at: "2026-01-01T00:00:00Z",
          updated_at: "2026-01-01T00:00:00Z",
        });
      return Promise.reject(new Error("unexpected path: " + path));
    }),
    post: vi.fn((path: string, body: unknown) => {
      mockState.posted.push({ path, body });
      const b = body as { name?: string };
      return Promise.resolve(
        mkIssue({ id: "new-1", display_id: "ENG-9", name: b.name ?? "x" }),
      );
    }),
  },
  ApiError: class ApiError extends Error {
    status: number;
    constructor(status: number, body: string) {
      super(body);
      this.status = status;
    }
  },
}));

vi.mock("../components/ui/toast", () => ({
  toast: { add: mockState.toastAdd },
}));

// Issues renders SavedViewsMenu → useAuth; the real provider needs a
// session — stub the auth context for these dialog tests.
vi.mock("../lib/auth", () => ({
  useAuth: () => ({ user: { id: "u", email: "u@x.com" }, loading: false }),
}));

function renderIssues(locationState?: unknown) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  const invalidateSpy = vi.spyOn(client, "invalidateQueries");
  render(
    <MemoryRouter
      initialEntries={[
        { pathname: "/w/acme/p/ENG", state: locationState ?? null },
      ]}
    >
      <QueryClientProvider client={client}>
        <Routes>
          <Route path="/w/:slug/p/:identifier" element={<Issues />} />
        </Routes>
      </QueryClientProvider>
    </MemoryRouter>,
  );
  return { invalidateSpy };
}

beforeEach(() => {
  vi.clearAllMocks();
  mockState.posted = [];
});

describe("Issues create dialog date prefill (C26T3)", () => {
  it("opens the dialog with the due date prefilled from location state", async () => {
    renderIssues({ newIssue: true, newIssueTargetDate: "2026-10-15" });
    await screen.findByRole("dialog");
    const due = screen.getByLabelText("Due date") as HTMLInputElement;
    expect(due.value).toBe("2026-10-15");
    const start = screen.getByLabelText("Start date") as HTMLInputElement;
    expect(start.value).toBe("");
  });

  it("sends target_date in the create body and invalidates the calendar caches", async () => {
    const user = userEvent.setup();
    const { invalidateSpy } = renderIssues({
      newIssue: true,
      newIssueTargetDate: "2026-10-15",
    });
    await screen.findByRole("dialog");
    await user.type(screen.getByLabelText("Title"), "Quickadded from calendar");
    await user.click(screen.getByRole("button", { name: "Create issue" }));

    await waitFor(() => expect(mockState.posted).toHaveLength(1));
    expect(mockState.posted[0].path).toBe(`${BASE}/issues`);
    expect(mockState.posted[0].body).toMatchObject({
      name: "Quickadded from calendar",
      target_date: "2026-10-15",
    });
    // The calendar buckets by target/start date — its caches must
    // revalidate so the new chip appears on the day.
    await waitFor(() =>
      expect(invalidateSpy).toHaveBeenCalledWith({
        queryKey: ["calendar", "acme", "ENG"],
      }),
    );
  });

  it("omits target_date from the create body when no date is set", async () => {
    const user = userEvent.setup();
    renderIssues({ newIssue: true });
    await screen.findByRole("dialog");
    await user.type(screen.getByLabelText("Title"), "Undated issue");
    await user.click(screen.getByRole("button", { name: "Create issue" }));

    await waitFor(() => expect(mockState.posted).toHaveLength(1));
    expect(mockState.posted[0].body).toMatchObject({ name: "Undated issue" });
    expect(mockState.posted[0].body).not.toHaveProperty("target_date");
  });
});
