// @vitest-environment jsdom
// C126T0: list-view drag enable rules at the page level — the grip
// handle appears ONLY under Manual rank (sort_order) + visual mode +
// flat/state effective grouping, in all three render paths (flat,
// grouped, sub-grouped), and never on nested subtask rows.
import {
  afterEach,
  beforeEach,
  describe,
  expect,
  it,
  vi,
} from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import { QueryClientProvider } from "@tanstack/react-query";
import { BrowserRouter, Route, Routes } from "react-router-dom";
import Issues from "./Issues";
import { api } from "../lib/api";
import type {
  DisplaySettings,
  GroupBy,
  OrderBy,
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
];

function mkIssue(
  id: string,
  displayId: string,
  name: string,
  stateId: string,
  sortOrder: number,
  extra: Partial<Issue> = {},
): Issue {
  return {
    id,
    project_id: "p",
    sequence_id: 1,
    display_id: displayId,
    name,
    priority: 0,
    state_id: stateId,
    sort_order: sortOrder,
    is_draft: false,
    created_by: "u-1",
    created_at: "",
    updated_at: "",
    assignees: [],
    labels: [],
    ...extra,
  };
}

const child: SubtaskRow = {
  id: "i-c1",
  display_id: "ENG-3",
  title: "Child one",
  state_id: "s-doing",
  priority: 2,
  assignee_ids: [],
  sort_order: 1,
};

function setSettings(over: {
  groupBy: GroupBy;
  subGroupBy: SubGroupBy;
  orderBy: OrderBy;
  showSubWorkItems?: boolean;
}) {
  mockState.settings = {
    fields: {
      state: true, priority: true, labels: true, assignees: true,
      dates: true, id: true, subitems: true, modules: true, cycle: true,
      release: true, attachments: true, links: true, dependencies: true,
    },
    groupBy: over.groupBy,
    orderBy: over.orderBy,
    showEmptyGroups: true,
    showSubWorkItems: over.showSubWorkItems ?? false,
    subGroupBy: over.subGroupBy,
  };
}

function renderIssues(path = "/w/acme/p/ENG") {
  vi.spyOn(api, "get").mockImplementation((p: string) => {
    if (p.endsWith("/states")) return Promise.resolve({ states });
    if (p.includes("/views")) return Promise.resolve({ views: [] });
    if (p.startsWith(`${ENG}/issues`))
      return Promise.resolve({ results: mockState.issues, next_cursor: null });
    if (p === ENG)
      return Promise.resolve({ id: "p", name: "ENG", identifier: "ENG", created_at: "", updated_at: "" });
    if (p.includes("/members")) return Promise.resolve({ members: [] });
    if (p.includes("/labels")) return Promise.resolve({ labels: [] });
    if (p.includes("/templates")) return Promise.resolve({ templates: [] });
    if (p.includes("/estimates")) return Promise.resolve({ estimates: [] });
    if (p.includes("/custom-fields")) return Promise.resolve({ custom_fields: [] });
    if (p.includes("/cycles")) return Promise.resolve({ cycles: [] });
    if (p.includes("/modules")) return Promise.resolve({ modules: [] });
    if (p.includes("/epics")) return Promise.resolve({ epics: [] });
    return Promise.reject(new Error(`unexpected GET ${p}`));
  });
  window.history.pushState({}, "", path);
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

function grips(): HTMLElement[] {
  return screen.queryAllByLabelText(/to reorder/);
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

describe("list drag enable rules (C126T0)", () => {
  it("flat path: grips appear under Manual rank, one per top-level row", async () => {
    setSettings({ groupBy: "none", subGroupBy: "none", orderBy: "sort_order" });
    mockState.issues = [
      mkIssue("i-a", "ENG-1", "First", "s-todo", 1024),
      mkIssue("i-b", "ENG-2", "Second", "s-doing", 2048),
    ];
    renderIssues();
    await screen.findByText("First");
    expect(grips()).toHaveLength(2);
    expect(screen.getByLabelText("Drag ENG-1 to reorder")).toBeInTheDocument();
  });

  it("flat path: no grips under any other order", async () => {
    setSettings({ groupBy: "none", subGroupBy: "none", orderBy: "-updated_at" });
    mockState.issues = [
      mkIssue("i-a", "ENG-1", "First", "s-todo", 1024),
    ];
    renderIssues();
    await screen.findByText("First");
    expect(grips()).toHaveLength(0);
  });

  it("flat path: no grips in PQL mode even under Manual rank", async () => {
    setSettings({ groupBy: "none", subGroupBy: "none", orderBy: "sort_order" });
    mockState.issues = [
      mkIssue("i-a", "ENG-1", "First", "s-todo", 1024),
    ];
    renderIssues("/w/acme/p/ENG?pql=state%20%3D%20Todo");
    await screen.findByText("First");
    expect(grips()).toHaveLength(0);
  });

  // C127T0 SUPERSEDES the C126 rule here: reassign drag is the ONLY
  // drag under priority/epic grouping, and the C126 grip handle is
  // its drag source — so grips now appear (row drops there stay
  // no-ops; only header drops reassign).
  it("grouped path: grips appear for priority grouping (C127 reassign source)", async () => {
    setSettings({ groupBy: "priority", subGroupBy: "none", orderBy: "sort_order" });
    mockState.issues = [
      mkIssue("i-a", "ENG-1", "First", "s-todo", 1024),
    ];
    renderIssues();
    await screen.findByText("First");
    expect(grips()).toHaveLength(1);
  });

  it("grouped path: grips appear for epic grouping (C127 reassign source)", async () => {
    setSettings({ groupBy: "epic", subGroupBy: "none", orderBy: "sort_order" });
    mockState.issues = [
      mkIssue("i-a", "ENG-1", "First", "s-todo", 1024),
    ];
    renderIssues();
    await screen.findByText("First");
    expect(grips()).toHaveLength(1);
  });

  it("grouped path: grips appear for state grouping", async () => {
    setSettings({ groupBy: "state", subGroupBy: "none", orderBy: "sort_order" });
    mockState.issues = [
      mkIssue("i-a", "ENG-1", "First", "s-todo", 1024),
      mkIssue("i-b", "ENG-2", "Second", "s-doing", 2048),
    ];
    renderIssues();
    await screen.findByText("First");
    expect(grips()).toHaveLength(2);
  });

  it("sub-grouped path: grips appear for state grouping with a sub-group", async () => {
    setSettings({ groupBy: "state", subGroupBy: "priority", orderBy: "sort_order" });
    mockState.issues = [
      mkIssue("i-a", "ENG-1", "First", "s-todo", 1024),
    ];
    renderIssues();
    await screen.findByText("First");
    expect(grips()).toHaveLength(1);
  });

  it("nested subtask rows never get a grip, even when drag is enabled", async () => {
    setSettings({ groupBy: "none", subGroupBy: "none", orderBy: "sort_order", showSubWorkItems: true });
    mockState.issues = [
      mkIssue("i-p", "ENG-1", "Parent issue", "s-todo", 1024, {
        subtasks: [child],
      }),
      mkIssue("i-q", "ENG-9", "Second parent", "s-doing", 2048),
    ];
    renderIssues();
    await screen.findByText("Parent issue");
    await screen.findByText("Child one");
    // Two top-level rows → two grips; the nested row adds none.
    expect(grips()).toHaveLength(2);
  });
});
