// @vitest-environment jsdom
// C124T0: list-view "Show sub-work items" DATA wiring (render sites
// are T1's): the list fetch passes include_subtasks=1 only when the
// toggle is on, and the pre-grouping split hides flat child rows in
// the flat, grouped, sub-grouped, and epic multi-appearance paths.
// Toggle off = today's flat layout. Follows the IssuesSubGroup
// test pattern (mocked useDisplaySettings + mocked api.get).
import {
  afterEach,
  beforeEach,
  describe,
  expect,
  it,
  vi,
} from "vitest";
import { cleanup, render, screen, within } from "@testing-library/react";
import { QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import Issues from "./Issues";
import { api } from "../lib/api";
import type {
  DisplaySettings,
  SubGroupBy,
} from "../components/issue/useDisplaySettings";
import type { Issue, IssueState, SubtaskRow } from "../lib/types";
import {
  makeQueryClient,
  stubMatchMedia,
} from "../components/shell/test-utils";

const mockState = vi.hoisted(() => ({
  settings: null as unknown as DisplaySettings,
  issues: [] as Issue[],
  toastAdd: vi.fn(),
}));

vi.mock("../components/issue/useDisplaySettings", async (importOriginal) => {
  const actual =
    await importOriginal<typeof import("../components/issue/useDisplaySettings")>();
  return {
    ...actual,
    useDisplaySettings: () => ({
      settings: mockState.settings,
      update: vi.fn(),
      updateFields: vi.fn(),
      reset: vi.fn(),
    }),
  };
});

vi.mock("../lib/auth", () => ({
  useAuth: () => ({ user: { id: "u-1", email: "u@x.com" }, loading: false }),
}));

vi.mock("../components/ui/toast", () => ({
  toast: { add: mockState.toastAdd },
}));

const ENG = "/api/v1/workspaces/acme/projects/ENG";

const states: IssueState[] = [
  { id: "s-todo", project_id: "p", name: "Todo", group: "unstarted", color: "#888", sequence: 1, created_at: "", updated_at: "" },
  { id: "s-doing", project_id: "p", name: "Doing", group: "started", color: "#888", sequence: 2, created_at: "", updated_at: "" },
  { id: "s-done", project_id: "p", name: "Done", group: "completed", color: "#888", sequence: 3, created_at: "", updated_at: "" },
];

function mkIssue(
  id: string,
  displayId: string,
  name: string,
  stateId: string,
  priority: number,
  extra: Partial<Issue> = {},
): Issue {
  return {
    id,
    project_id: "p",
    sequence_id: 1,
    display_id: displayId,
    name,
    priority,
    state_id: stateId,
    sort_order: 0,
    is_draft: false,
    created_by: "u-1",
    created_at: "",
    updated_at: "",
    assignees: [],
    labels: [],
    ...extra,
  };
}

function childRow(id: string, title: string, sortOrder: number): SubtaskRow {
  return {
    id,
    display_id: id.toUpperCase(),
    title,
    state_id: "s-done",
    priority: 3,
    assignee_ids: [],
    sort_order: sortOrder,
  };
}

function setSettings(over: {
  groupBy: DisplaySettings["groupBy"];
  subGroupBy: SubGroupBy;
  showSubWorkItems: boolean;
  showEmptyGroups?: boolean;
}) {
  mockState.settings = {
    fields: {
      state: true, priority: true, labels: true, assignees: true,
      dates: true, id: true, subitems: true, modules: true, cycle: true,
      release: true, attachments: true, links: true, dependencies: true,
    },
    groupBy: over.groupBy,
    orderBy: "-updated_at",
    showEmptyGroups: over.showEmptyGroups ?? true,
    showSubWorkItems: over.showSubWorkItems,
    subGroupBy: over.subGroupBy,
  };
}

// Parent (Todo) with one nested child (Done); the child carries a
// grandchild row — the grandchild stays flat (one level). Orphan:
// parent not in the result set — stays a flat row.
function fixtureIssues(): Issue[] {
  const parent = mkIssue("i-p", "ENG-1", "Parent task", "s-todo", 3, {
    subtasks: [childRow("i-c", "Child task", 1)],
  });
  const child = mkIssue("i-c", "ENG-2", "Child task", "s-done", 3, {
    subtasks: [childRow("i-g", "Grandchild task", 1)],
  });
  const grandchild = mkIssue("i-g", "ENG-3", "Grandchild task", "s-todo", 3);
  const orphan = mkIssue("i-o", "ENG-4", "Orphan task", "s-done", 3);
  return [parent, child, grandchild, orphan];
}

function renderIssues() {
  const get = vi.spyOn(api, "get").mockImplementation((path: string) => {
    if (path.endsWith("/states")) return Promise.resolve({ states });
    if (path.includes("/views")) return Promise.resolve({ views: [] });
    if (path.startsWith(`${ENG}/issues`))
      return Promise.resolve({ results: mockState.issues, next_cursor: null });
    if (path === ENG)
      return Promise.resolve({ id: "p", name: "ENG", identifier: "ENG", created_at: "", updated_at: "" });
    if (path.includes("/members")) return Promise.resolve({ members: [] });
    if (path.includes("/labels")) return Promise.resolve({ labels: [] });
    if (path.includes("/templates")) return Promise.resolve({ templates: [] });
    if (path.includes("/estimates")) return Promise.resolve({ estimates: [] });
    if (path.includes("/custom-fields")) return Promise.resolve({ custom_fields: [] });
    if (path.includes("/cycles")) return Promise.resolve({ cycles: [] });
    if (path.includes("/modules")) return Promise.resolve({ modules: [] });
    if (path.includes("/epics")) return Promise.resolve({ epics: [] });
    return Promise.reject(new Error(`unexpected GET ${path}`));
  });
  render(
    <MemoryRouter initialEntries={["/w/acme/p/ENG"]}>
      <QueryClientProvider client={makeQueryClient()}>
        <Routes>
          <Route path="/w/:slug/p/:identifier" element={<Issues />} />
        </Routes>
      </QueryClientProvider>
    </MemoryRouter>,
  );
  return { get };
}

function issueCalls(get: ReturnType<typeof renderIssues>["get"]) {
  return get.mock.calls
    .map((c) => c[0] as string)
    .filter((p) => p.includes("/issues?"))
    .map((p) => new URLSearchParams(p.split("?")[1] ?? ""));
}

/** Header count next to a primary group title (h2's sibling span). */
function groupCount(title: string): string | null {
  const section = screen.getByRole("region", { name: title });
  const h2 = within(section).getByRole("heading", { level: 2, name: title });
  return h2.nextElementSibling?.textContent ?? null;
}

beforeEach(() => {
  stubMatchMedia(false);
  localStorage.clear();
  mockState.issues = [];
});

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

describe("list sub-work items data (C124T0)", () => {
  it("passes include_subtasks=1 on the list fetch when the toggle is on", async () => {
    setSettings({ groupBy: "none", subGroupBy: "none", showSubWorkItems: true });
    mockState.issues = fixtureIssues();
    const { get } = renderIssues();
    await screen.findByText("Parent task");
    const calls = issueCalls(get);
    expect(calls.length).toBeGreaterThan(0);
    for (const qs of calls) {
      expect(qs.get("include_subtasks")).toBe("1");
    }
  });

  it("omits include_subtasks when the toggle is off", async () => {
    setSettings({ groupBy: "none", subGroupBy: "none", showSubWorkItems: false });
    mockState.issues = fixtureIssues();
    const { get } = renderIssues();
    await screen.findByText("Parent task");
    const calls = issueCalls(get);
    expect(calls.length).toBeGreaterThan(0);
    for (const qs of calls) {
      expect(qs.get("include_subtasks")).toBeNull();
    }
  });

  it("flat path: nested child row hidden, orphan + grandchild stay flat", async () => {
    setSettings({ groupBy: "none", subGroupBy: "none", showSubWorkItems: true });
    mockState.issues = fixtureIssues();
    renderIssues();
    await screen.findByText("Parent task");
    // Merged C124: the flat child row is hidden AND T1 renders the
    // child as a nested row under the parent — so the title appears
    // exactly once, inside a nested container, never as a flat row.
    expect(
      screen.getByText("Child task").closest("[data-nested-subtask]"),
    ).not.toBeNull();
    expect(screen.getByText("Orphan task")).toBeInTheDocument();
    expect(screen.getByText("Grandchild task")).toBeInTheDocument();
  });

  it("toggle off flat path = today's layout: every row present", async () => {
    setSettings({ groupBy: "none", subGroupBy: "none", showSubWorkItems: false });
    mockState.issues = fixtureIssues();
    renderIssues();
    await screen.findByText("Parent task");
    expect(screen.getByText("Child task")).toBeInTheDocument();
    expect(screen.getByText("Orphan task")).toBeInTheDocument();
    expect(screen.getByText("Grandchild task")).toBeInTheDocument();
  });

  it("grouped path: group counts reflect visible rows, child hidden from its group", async () => {
    setSettings({ groupBy: "state", subGroupBy: "none", showSubWorkItems: true });
    mockState.issues = fixtureIssues();
    renderIssues();
    await screen.findByText("Parent task");
    // Merged C124: child appears only as a nested row under its
    // parent, never as a flat row in any group.
    expect(
      screen.getByText("Child task").closest("[data-nested-subtask]"),
    ).not.toBeNull();
    // Todo: parent + grandchild (child is Done, so it never counted
    // here); Done: orphan only (nested child hidden, not counted).
    expect(groupCount("Todo")).toBe("2");
    expect(groupCount("Done")).toBe("1");
  });

  it("sub-grouped path: child hidden inside state > priority sections", async () => {
    setSettings({ groupBy: "state", subGroupBy: "priority", showSubWorkItems: true });
    mockState.issues = fixtureIssues();
    renderIssues();
    await screen.findByText("Parent task");
    // Merged C124: child appears only as a nested row under its
    // parent, never as a flat row in any section.
    expect(
      screen.getByText("Child task").closest("[data-nested-subtask]"),
    ).not.toBeNull();
    expect(screen.getByText("Orphan task")).toBeInTheDocument();
    expect(screen.getByText("Grandchild task")).toBeInTheDocument();
  });

  it("epic grouping: parent appears in each epic group, child hidden in both", async () => {
    setSettings({ groupBy: "epic", subGroupBy: "none", showSubWorkItems: true });
    const parent = mkIssue("i-p", "ENG-1", "Parent task", "s-todo", 3, {
      subtasks: [childRow("i-c", "Child task", 1)],
      epics: [
        { id: "e-1", name: "Auth" },
        { id: "e-2", name: "Checkout" },
      ],
    });
    mockState.issues = [parent, mkIssue("i-c", "ENG-2", "Child task", "s-done", 3)];
    renderIssues();
    await screen.findAllByText("Parent task");
    // Multi-appearance: one visible row per epic group.
    expect(screen.getAllByText("Parent task")).toHaveLength(2);
    // Merged C124: child appears nested under EACH parent
    // appearance, never as a flat row in either group.
    const nested = screen.getAllByText("Child task");
    expect(nested).toHaveLength(2);
    for (const el of nested) {
      expect(el.closest("[data-nested-subtask]")).not.toBeNull();
    }
  });
});
