// @vitest-environment jsdom
// C124T1: list-view nested sub-work rows — NestedListSubtaskRow under
// its parent IssueRow in ALL THREE render paths (flat, grouped,
// sub-grouped); toggle-off renders nothing nested; nested click opens
// peek; nested rows carry no bulk checkbox and are not j/k targets.
//
// STUB SCOPE: T0's splitNestedIssues.ts + visible filtering have not
// landed yet, so these tests drive nesting through the plan-contract
// source the render sites consume today — the parent's `subtasks`
// rows — and do NOT assert that child flat rows disappear (that is
// T0's visible filtering; the T0 merge swaps the source to
// nestedByParent.get(issue.id) without changing this component).
// Fixtures therefore carry the child ONLY via parent.subtasks, never
// as a separate fetched row.
import {
  afterEach,
  beforeEach,
  describe,
  expect,
  it,
  vi,
} from "vitest";
import { cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { QueryClientProvider } from "@tanstack/react-query";
import { BrowserRouter, Route, Routes } from "react-router-dom";
import Issues from "./Issues";
import { api } from "../lib/api";
import { installGlobalShortcuts } from "../lib/shortcuts";
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
  settings: null as unknown as DisplaySettings & { subGroupBy: SubGroupBy },
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

const childOne: SubtaskRow = {
  id: "i-c1",
  display_id: "ENG-2",
  title: "Child one",
  state_id: "s-doing",
  priority: 2,
  assignee_ids: ["u-9"],
  sort_order: 1,
};

const childTwo: SubtaskRow = {
  id: "i-c2",
  display_id: "ENG-3",
  title: "Child two",
  state_id: "s-done",
  priority: null,
  assignee_ids: [],
  sort_order: 2,
};

function parentIssue(): Issue {
  return mkIssue("i-p", "ENG-1", "Parent issue", "s-todo", 4, {
    subtasks: [childOne, childTwo],
  });
}

function setSettings(over: {
  groupBy: DisplaySettings["groupBy"];
  subGroupBy: SubGroupBy;
  showSubWorkItems?: boolean;
}) {
  mockState.settings = {
    fields: {
      state: true, priority: true, labels: true, assignees: true,
      dates: true, id: true, subitems: true, modules: true, cycle: true,
      release: true, attachments: true, links: true, dependencies: true,
    },
    groupBy: over.groupBy,
    orderBy: "-updated_at",
    showEmptyGroups: true,
    showSubWorkItems: over.showSubWorkItems ?? true,
    subGroupBy: over.subGroupBy,
  };
}

function renderIssues() {
  vi.spyOn(api, "get").mockImplementation((path: string) => {
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
  // BrowserRouter (not MemoryRouter) so the peek click's ?peek=
  // search-param update is observable via window.location.search.
  window.history.pushState({}, "", "/w/acme/p/ENG");
  render(
    <BrowserRouter>
      <QueryClientProvider client={makeQueryClient()}>
        <Routes>
          <Route path="/w/:slug/p/:identifier" element={<Issues />} />
        </Routes>
      </QueryClientProvider>
    </BrowserRouter>,
  );
}

function nestedEl(id: string): HTMLElement | null {
  return document.querySelector(`[data-nested-subtask="${id}"]`);
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

describe("list nested sub-work rows (C124T1)", () => {
  it("flat path: nested rows render under the parent, in subtask order", async () => {
    setSettings({ groupBy: "none", subGroupBy: "none" });
    mockState.issues = [
      parentIssue(),
      mkIssue("i-q", "ENG-9", "Second parent", "s-doing", 0),
    ];
    renderIssues();
    await screen.findByText("Parent issue");
    expect(nestedEl("i-c1")).toBeInTheDocument();
    expect(nestedEl("i-c2")).toBeInTheDocument();
    // Nested rows sit between the parent row and the next parent row.
    const text = document.body.textContent ?? "";
    expect(text.indexOf("Parent issue")).toBeLessThan(text.indexOf("Child one"));
    expect(text.indexOf("Child one")).toBeLessThan(text.indexOf("Child two"));
    expect(text.indexOf("Child two")).toBeLessThan(text.indexOf("Second parent"));
    // Child's real state (Doing) on the state dot, priority bars for
    // the row that carries one (priority 2 = Medium), none for the
    // priority-less row.
    const one = nestedEl("i-c1")!;
    expect(one.querySelector('[title="Doing"]')).toBeInTheDocument();
    expect(within(one).getByLabelText("Priority: Medium")).toBeInTheDocument();
    expect(within(one).getByLabelText("1 assignee")).toBeInTheDocument();
    expect(within(nestedEl("i-c2")!).queryByLabelText(/Priority:/)).not.toBeInTheDocument();
  });

  it("grouped path: nested rows render under the parent inside its group", async () => {
    setSettings({ groupBy: "state", subGroupBy: "none" });
    mockState.issues = [parentIssue()];
    renderIssues();
    await screen.findByText("Parent issue");
    expect(screen.getByRole("heading", { level: 2, name: "Todo" })).toBeInTheDocument();
    expect(nestedEl("i-c1")).toBeInTheDocument();
    expect(nestedEl("i-c2")).toBeInTheDocument();
    const todo = screen.getByRole("region", { name: "Todo" });
    expect(within(todo).getByText("Child one")).toBeInTheDocument();
  });

  it("sub-grouped path: nested rows render under the parent inside its sub-section", async () => {
    setSettings({ groupBy: "state", subGroupBy: "priority" });
    mockState.issues = [parentIssue()];
    renderIssues();
    await screen.findByText("Parent issue");
    // showEmptyGroups renders an Urgent sub-section per primary group;
    // scope to the Todo region (the parent's group).
    const todo = within(screen.getByRole("region", { name: "Todo" }));
    expect(todo.getByRole("heading", { level: 3, name: "Urgent" })).toBeInTheDocument();
    expect(todo.getByText("Child one")).toBeInTheDocument();
    expect(nestedEl("i-c1")).toBeInTheDocument();
    expect(nestedEl("i-c2")).toBeInTheDocument();
  });

  it("toggle off renders nothing nested in any path", async () => {
    setSettings({ groupBy: "state", subGroupBy: "priority", showSubWorkItems: false });
    mockState.issues = [parentIssue()];
    renderIssues();
    await screen.findByText("Parent issue");
    expect(document.querySelector("[data-nested-subtask]")).not.toBeInTheDocument();
  });

  it("nested click opens peek for the child id", async () => {
    setSettings({ groupBy: "none", subGroupBy: "none" });
    mockState.issues = [parentIssue()];
    renderIssues();
    await screen.findByText("Parent issue");
    fireEvent.click(screen.getByText("Child one"));
    expect(window.location.search).toContain("peek=i-c1");
  });

  it("nested rows carry no bulk checkbox and are not j/k targets", async () => {
    installGlobalShortcuts();
    setSettings({ groupBy: "none", subGroupBy: "none" });
    mockState.issues = [
      parentIssue(),
      mkIssue("i-q", "ENG-9", "Second parent", "s-doing", 0),
    ];
    renderIssues();
    await screen.findByText("Parent issue");
    const nested = nestedEl("i-c1")!;
    // No checkbox, no row role, no selection ring on the nested row.
    expect(within(nested).queryByRole("checkbox")).not.toBeInTheDocument();
    expect(nested.getAttribute("role")).toBeNull();
    expect(nested.className).not.toContain("ring-1");
    // j moves the kbd ring from the first parent to the second parent
    // — never onto the nested row.
    fireEvent.keyDown(window, { key: "j" });
    const ringed = document.querySelectorAll(".ring-1");
    expect(ringed).toHaveLength(1);
    expect(ringed[0].textContent).toContain("Second parent");
    expect(nested.className).not.toContain("ring-1");
  });
});
