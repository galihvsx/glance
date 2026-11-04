// @vitest-environment jsdom
// C141T1: nested subtask drag at the page level — the grip appears
// on nested rows ONLY when the nested gate holds (showSubWorkItems
// on + >= 2 rows), and the gate is order-independent by design:
// nested grips appear under a non-sort_order orderBy, in PQL mode,
// and under epic grouping (where one parent multi-appears, so the
// same nested grip renders under EACH appearance). Disabled states
// render no nested grip at all. The live drag itself is NOT proven
// here (mocked tests cannot prove a dnd-kit drop landing); the
// drop pipeline is covered by listNestedReorder.test.ts and dispatch
// routing by listHeaderDrop.test.ts, and the live browser drag
// remains the final verifier.
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

function mkChild(id: string, displayId: string, title: string): SubtaskRow {
  return {
    id,
    display_id: displayId,
    title,
    state_id: "s-doing",
    priority: null,
    assignee_ids: [],
    sort_order: 1024,
  };
}

const childOne = mkChild("i-c1", "ENG-2", "Child one");
const childTwo = mkChild("i-c2", "ENG-3", "Child two");

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

function nestedGrips(): HTMLElement[] {
  return screen.queryAllByLabelText(/to reorder subtask/);
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

describe("nested drag gate at the page level (C141T1)", () => {
  it("flat path: nested grips appear under a NON-sort_order order, with no parent grips", async () => {
    // The C126 gate would forbid any drag here — the nested gate
    // does not: nestedByParent always sorts by sort_order ASC.
    setSettings({ groupBy: "none", subGroupBy: "none", orderBy: "-updated_at", showSubWorkItems: true });
    mockState.issues = [
      mkIssue("i-p", "ENG-1", "Parent issue", "s-todo", 1024, {
        subtasks: [childOne, childTwo],
      }),
    ];
    renderIssues();
    await screen.findByText("Parent issue");
    await screen.findByText("Child one");
    expect(nestedGrips()).toHaveLength(2);
    expect(screen.getByLabelText("Drag ENG-2 to reorder subtask")).toBeInTheDocument();
    expect(screen.getByLabelText("Drag ENG-3 to reorder subtask")).toBeInTheDocument();
    // Parent drag is off under this order with no grouping.
    expect(screen.queryByLabelText("Drag ENG-1 to reorder")).not.toBeInTheDocument();
  });

  it("flat path: nested grips appear in PQL mode, with no parent grips", async () => {
    setSettings({ groupBy: "none", subGroupBy: "none", orderBy: "sort_order", showSubWorkItems: true });
    mockState.issues = [
      mkIssue("i-p", "ENG-1", "Parent issue", "s-todo", 1024, {
        subtasks: [childOne, childTwo],
      }),
    ];
    renderIssues("/w/acme/p/ENG?pql=state%20%3D%20Todo");
    await screen.findByText("Parent issue");
    await screen.findByText("Child one");
    expect(nestedGrips()).toHaveLength(2);
    expect(screen.queryByLabelText("Drag ENG-1 to reorder")).not.toBeInTheDocument();
  });

  it("grouped path: nested grips appear under state grouping", async () => {
    setSettings({ groupBy: "state", subGroupBy: "none", orderBy: "-updated_at", showSubWorkItems: true });
    mockState.issues = [
      mkIssue("i-p", "ENG-1", "Parent issue", "s-todo", 1024, {
        subtasks: [childOne, childTwo],
      }),
    ];
    renderIssues();
    await screen.findByText("Parent issue");
    await screen.findByText("Child one");
    expect(nestedGrips()).toHaveLength(2);
  });

  it("epic grouping: the same nested grip renders under EACH parent appearance", async () => {
    setSettings({ groupBy: "epic", subGroupBy: "none", orderBy: "-updated_at", showSubWorkItems: true });
    mockState.issues = [
      mkIssue("i-p", "ENG-1", "Parent issue", "s-todo", 1024, {
        subtasks: [childOne, childTwo],
        epics: [
          { id: "e-1", name: "Epic one" },
          { id: "e-2", name: "Epic two" },
        ],
      }),
    ];
    renderIssues();
    // Multi-appearance: the parent (and each child) renders twice.
    expect(await screen.findAllByText("Parent issue")).toHaveLength(2);
    expect(await screen.findAllByText("Child one")).toHaveLength(2);
    // Two appearances → two grips per child, each stack-scoped.
    expect(screen.queryAllByLabelText("Drag ENG-2 to reorder subtask")).toHaveLength(2);
    expect(screen.queryAllByLabelText("Drag ENG-3 to reorder subtask")).toHaveLength(2);
  });

  it("toggle off: no nested rows render, so no nested grips", async () => {
    setSettings({ groupBy: "none", subGroupBy: "none", orderBy: "sort_order", showSubWorkItems: false });
    mockState.issues = [
      mkIssue("i-p", "ENG-1", "Parent issue", "s-todo", 1024, {
        subtasks: [childOne, childTwo],
      }),
    ];
    renderIssues();
    await screen.findByText("Parent issue");
    expect(screen.queryByText("Child one")).not.toBeInTheDocument();
    expect(nestedGrips()).toHaveLength(0);
  });

  it("1-row stack: the lone nested row gets no grip, but the parent keeps its own", async () => {
    setSettings({ groupBy: "none", subGroupBy: "none", orderBy: "sort_order", showSubWorkItems: true });
    mockState.issues = [
      mkIssue("i-p", "ENG-1", "Parent issue", "s-todo", 1024, {
        subtasks: [childOne],
      }),
    ];
    renderIssues();
    await screen.findByText("Parent issue");
    await screen.findByText("Child one");
    expect(nestedGrips()).toHaveLength(0);
    expect(screen.getByLabelText("Drag ENG-1 to reorder")).toBeInTheDocument();
  });
});
