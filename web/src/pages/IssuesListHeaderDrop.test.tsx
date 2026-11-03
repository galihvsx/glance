// @vitest-environment jsdom
// C127T0: group-header drop targets at the page level — the reassign
// enable rule (grips as drag sources under any orderBy whenever
// grouping is active and not PQL mode) and the header-droppable wiring
// (which headers get a droppable, with which id; the state-Ungrouped
// sub-header never does).
//
// A real dnd-kit drop cannot be simulated reliably in jsdom (sensors
// need real pointer movement), so drop routing + call sequences are
// covered by the listHeaderDrop unit tests; this file covers what the
// page itself decides. The drop zone is mocked to a marker div so the
// wiring (presence + ids) is observable without dnd-kit internals.
import {
  afterEach,
  beforeEach,
  describe,
  expect,
  it,
  vi,
} from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
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
import type { Issue, IssueState } from "../lib/types";
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

// Marker stand-in for the real drop zone: the page's wiring decisions
// (which headers are droppable, with which id) stay observable.
vi.mock("../components/issue/ListHeaderDropZone", () => ({
  default: ({
    droppableId,
    children,
  }: {
    droppableId: string;
    children: React.ReactNode;
  }) => (
    <div data-testid="header-dropzone" data-drop-id={droppableId}>
      {children}
    </div>
  ),
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

function setSettings(over: {
  groupBy: GroupBy;
  subGroupBy: SubGroupBy;
  orderBy: OrderBy;
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
    showSubWorkItems: false,
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

function dropIds(): string[] {
  return screen
    .queryAllByTestId("header-dropzone")
    .map((el) => el.getAttribute("data-drop-id") ?? "");
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

describe("reassign enable rule (C127T0)", () => {
  it("priority grouping: grips appear even under a non-Manual order", async () => {
    setSettings({ groupBy: "priority", subGroupBy: "none", orderBy: "-updated_at" });
    mockState.issues = [mkIssue("i-a", "ENG-1", "First", "s-todo", 1024)];
    renderIssues();
    await screen.findByText("First");
    expect(grips()).toHaveLength(1);
  });

  it("epic grouping: grips appear under Manual rank", async () => {
    setSettings({ groupBy: "epic", subGroupBy: "none", orderBy: "sort_order" });
    mockState.issues = [
      mkIssue("i-a", "ENG-1", "First", "s-todo", 1024, {
        epics: [{ id: "e-1", name: "Epic One", archived_at: null }],
      }),
    ];
    renderIssues();
    await screen.findByText("First");
    expect(grips()).toHaveLength(1);
  });

  it("state grouping: grips appear under a non-Manual order (reassign-only)", async () => {
    setSettings({ groupBy: "state", subGroupBy: "none", orderBy: "-updated_at" });
    mockState.issues = [mkIssue("i-a", "ENG-1", "First", "s-todo", 1024)];
    renderIssues();
    await screen.findByText("First");
    expect(grips()).toHaveLength(1);
  });

  it("PQL mode: no grips under priority grouping even under Manual rank", async () => {
    setSettings({ groupBy: "priority", subGroupBy: "none", orderBy: "sort_order" });
    mockState.issues = [mkIssue("i-a", "ENG-1", "First", "s-todo", 1024)];
    renderIssues("/w/acme/p/ENG?pql=state%20%3D%20Todo");
    await screen.findByText("First");
    expect(grips()).toHaveLength(0);
  });

  it("flat layout: no grips under a non-Manual order (unchanged)", async () => {
    setSettings({ groupBy: "none", subGroupBy: "none", orderBy: "-updated_at" });
    mockState.issues = [mkIssue("i-a", "ENG-1", "First", "s-todo", 1024)];
    renderIssues();
    await screen.findByText("First");
    expect(grips()).toHaveLength(0);
  });
});

describe("header droppable wiring (C127T0)", () => {
  it("state grouping: every primary header is droppable, with its state id", async () => {
    setSettings({ groupBy: "state", subGroupBy: "none", orderBy: "sort_order" });
    mockState.issues = [mkIssue("i-a", "ENG-1", "First", "s-todo", 1024)];
    renderIssues();
    await screen.findByText("First");
    // showEmptyGroups renders both states, even the empty one.
    expect(dropIds().sort()).toEqual([
      "header:s-doing:state:s-doing",
      "header:s-todo:state:s-todo",
    ]);
  });

  it("epic grouping: epic + Ungrouped headers are droppable", async () => {
    setSettings({ groupBy: "epic", subGroupBy: "none", orderBy: "sort_order" });
    mockState.issues = [
      mkIssue("i-a", "ENG-1", "First", "s-todo", 1024, {
        epics: [{ id: "e-1", name: "Epic One", archived_at: null }],
      }),
      mkIssue("i-b", "ENG-2", "Second", "s-doing", 2048),
    ];
    renderIssues();
    await screen.findByText("First");
    expect(dropIds().sort()).toEqual([
      "header:e-1:epic:e-1",
      "header:epic:ungrouped:epic:ungrouped",
    ]);
  });

  it("sub-grouped priority+state: every header is droppable EXCEPT the state-Ungrouped sub-header", async () => {
    setSettings({ groupBy: "priority", subGroupBy: "state", orderBy: "sort_order" });
    mockState.issues = [
      mkIssue("i-a", "ENG-1", "First", "s-todo", 1024),
      // State missing from the loaded states → Ungrouped sub-section.
      mkIssue("i-b", "ENG-2", "Orphan", "s-unknown", 2048),
    ];
    renderIssues();
    await screen.findByText("First");
    // 5 primary priority headers + 2 state sub-headers per primary
    // group (Todo, Doing — the Ungrouped sub-header is not droppable).
    expect(dropIds()).toHaveLength(15);
    expect(dropIds()).not.toContain(expect.stringContaining("state:ungrouped"));
    // The Ungrouped sub-header renders, but outside any drop zone.
    const ungrouped = screen.getByRole("heading", { name: "Ungrouped" });
    expect(ungrouped.closest('[data-testid="header-dropzone"]')).toBeNull();
    // A normal sub-header renders inside a drop zone.
    const todo = screen.getAllByRole("heading", { name: "Todo" })[0];
    expect(todo.closest('[data-testid="header-dropzone"]')).not.toBeNull();
  });

  it("flat layout: no droppables", async () => {
    setSettings({ groupBy: "none", subGroupBy: "none", orderBy: "sort_order" });
    mockState.issues = [mkIssue("i-a", "ENG-1", "First", "s-todo", 1024)];
    renderIssues();
    await screen.findByText("First");
    expect(dropIds()).toHaveLength(0);
  });

  it("PQL mode: no droppables under state grouping", async () => {
    setSettings({ groupBy: "state", subGroupBy: "none", orderBy: "sort_order" });
    mockState.issues = [mkIssue("i-a", "ENG-1", "First", "s-todo", 1024)];
    renderIssues("/w/acme/p/ENG?pql=state%20%3D%20Todo");
    await screen.findByText("First");
    expect(dropIds()).toHaveLength(0);
  });
});

describe("assignee grouping (C129T0)", () => {
  const ALICE = { id: "u-alice", name: "Alice" };
  const BOB = { id: "u-bob", name: "Bob" };

  function assigneeIssues() {
    return [
      mkIssue("i-a", "ENG-1", "First", "s-todo", 1024, {
        assignees: [ALICE, BOB],
      }),
      mkIssue("i-b", "ENG-2", "Second", "s-doing", 2048, {
        assignees: [BOB],
      }),
      mkIssue("i-c", "ENG-3", "Third", "s-todo", 3072),
    ];
  }

  it("assignee grouping: multi-appearance rows grip under every group, even under a non-Manual order", async () => {
    setSettings({
      groupBy: "assignees",
      subGroupBy: "none",
      orderBy: "-updated_at",
    });
    mockState.issues = assigneeIssues();
    renderIssues();
    await screen.findByText("Third");
    // Alice: [First]; Bob: [First, Second]; Ungrouped: [Third].
    expect(grips()).toHaveLength(4);
    expect(screen.getByRole("heading", { name: "Alice" })).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "Bob" })).toBeInTheDocument();
    expect(
      screen.getByRole("heading", { name: "Ungrouped" }),
    ).toBeInTheDocument();
  });

  it("assignee grouping: member + Ungrouped headers are droppable", async () => {
    setSettings({
      groupBy: "assignees",
      subGroupBy: "none",
      orderBy: "sort_order",
    });
    mockState.issues = assigneeIssues();
    renderIssues();
    await screen.findByText("Third");
    expect(dropIds().sort()).toEqual([
      "header:assignee:ungrouped:assignee:ungrouped",
      "header:u-alice:assignee:u-alice",
      "header:u-bob:assignee:u-bob",
    ]);
  });

  it("assignee grouping: no Ungrouped header when every issue is assigned", async () => {
    setSettings({
      groupBy: "assignees",
      subGroupBy: "none",
      orderBy: "sort_order",
    });
    mockState.issues = [
      mkIssue("i-a", "ENG-1", "First", "s-todo", 1024, {
        assignees: [ALICE],
      }),
    ];
    renderIssues();
    await screen.findByText("First");
    expect(dropIds()).toEqual(["header:u-alice:assignee:u-alice"]);
  });

  it("assignee primary + state sub-group: sub-sections compose under assignee groups", async () => {
    setSettings({
      groupBy: "assignees",
      subGroupBy: "state",
      orderBy: "sort_order",
    });
    mockState.issues = [
      mkIssue("i-a", "ENG-1", "First", "s-todo", 1024, {
        assignees: [ALICE],
      }),
      mkIssue("i-b", "ENG-2", "Second", "s-doing", 2048, {
        assignees: [BOB],
      }),
    ];
    renderIssues();
    await screen.findByText("First");
    const ids = dropIds();
    // Primary assignee header stays droppable…
    expect(ids).toContain("header:u-alice:assignee:u-alice");
    // …and state sub-headers nest under it (section-scoped ids).
    expect(ids).toContain("header:u-alice::s-todo:state:s-todo");
    expect(ids).toContain("header:u-bob::s-doing:state:s-doing");
  });

  it("PQL mode: no grips under assignee grouping even under Manual rank", async () => {
    setSettings({
      groupBy: "assignees",
      subGroupBy: "none",
      orderBy: "sort_order",
    });
    mockState.issues = assigneeIssues();
    renderIssues("/w/acme/p/ENG?pql=state%20%3D%20Todo");
    await screen.findByText("Third");
    expect(grips()).toHaveLength(0);
    expect(dropIds()).toHaveLength(0);
  });

  it("assignee group quick-add: create, then attach to the group member", async () => {
    setSettings({
      groupBy: "assignees",
      subGroupBy: "none",
      orderBy: "sort_order",
    });
    mockState.issues = assigneeIssues();
    const postSpy = vi.spyOn(api, "post").mockImplementation((p: string) => {
      if (p === `${ENG}/issues`)
        return Promise.resolve(
          mkIssue("i-new", "ENG-9", "Brand new", "s-todo", 4096),
        );
      if (p.includes("/assignees/")) return Promise.resolve({});
      return Promise.reject(new Error(`unexpected POST ${p}`));
    });
    renderIssues();
    await screen.findByText("Third");
    // First group's (Alice's) quick-add.
    const buttons = screen.getAllByRole("button", {
      name: "Add new work item inline",
    });
    fireEvent.click(buttons[0]);
    const input = screen.getByLabelText("New work item title");
    fireEvent.change(input, { target: { value: "Brand new" } });
    fireEvent.submit(input.closest("form")!);
    await waitFor(() => expect(postSpy).toHaveBeenCalledTimes(2));
    expect(postSpy.mock.calls[0][0]).toBe(`${ENG}/issues`);
    expect(postSpy.mock.calls[0][1]).toEqual({
      name: "Brand new",
      state_id: "s-todo",
    });
    expect(postSpy.mock.calls[1][0]).toBe(
      `${ENG}/issues/i-new/assignees/u-alice`,
    );
  });

  it("assignee group quick-add: attach failure surfaces an error", async () => {
    setSettings({
      groupBy: "assignees",
      subGroupBy: "none",
      orderBy: "sort_order",
    });
    mockState.issues = assigneeIssues();
    vi.spyOn(api, "post").mockImplementation((p: string) => {
      if (p === `${ENG}/issues`)
        return Promise.resolve(
          mkIssue("i-new", "ENG-9", "Brand new", "s-todo", 4096),
        );
      if (p.includes("/assignees/"))
        return Promise.reject(new Error("attach boom"));
      return Promise.reject(new Error(`unexpected POST ${p}`));
    });
    renderIssues();
    await screen.findByText("Third");
    const buttons = screen.getAllByRole("button", {
      name: "Add new work item inline",
    });
    fireEvent.click(buttons[0]);
    const input = screen.getByLabelText("New work item title");
    fireEvent.change(input, { target: { value: "Brand new" } });
    fireEvent.submit(input.closest("form")!);
    // QuickAdd shows the thrown attach error inline; the created row
    // itself lands in Ungrouped after invalidation (server state).
    await screen.findByText("attach boom");
  });

  it("Ungrouped quick-add: plain create, no attach call", async () => {
    setSettings({
      groupBy: "assignees",
      subGroupBy: "none",
      orderBy: "sort_order",
    });
    mockState.issues = assigneeIssues();
    const postSpy = vi.spyOn(api, "post").mockImplementation((p: string) => {
      if (p === `${ENG}/issues`)
        return Promise.resolve(
          mkIssue("i-new", "ENG-9", "Brand new", "s-todo", 4096),
        );
      return Promise.reject(new Error(`unexpected POST ${p}`));
    });
    renderIssues();
    await screen.findByText("Third");
    // Last group's (Ungrouped's) quick-add.
    const buttons = screen.getAllByRole("button", {
      name: "Add new work item inline",
    });
    fireEvent.click(buttons[buttons.length - 1]);
    const input = screen.getByLabelText("New work item title");
    fireEvent.change(input, { target: { value: "Brand new" } });
    fireEvent.submit(input.closest("form")!);
    await waitFor(() => expect(postSpy).toHaveBeenCalledTimes(1));
    expect(postSpy.mock.calls[0][0]).toBe(`${ENG}/issues`);
  });
});

describe("cycle grouping (C132T1)", () => {
  const SPRINT_1 = { id: "c-1", name: "Sprint 1" };
  const SPRINT_2 = { id: "c-2", name: "Sprint 2" };

  function cycleIssues() {
    return [
      mkIssue("i-a", "ENG-1", "First", "s-todo", 1024, {
        project_structure: { cycle: SPRINT_1, modules: [] },
      }),
      mkIssue("i-b", "ENG-2", "Second", "s-doing", 2048, {
        project_structure: { cycle: SPRINT_2, modules: [] },
      }),
      mkIssue("i-c", "ENG-3", "Third", "s-todo", 3072, {
        project_structure: { cycle: null, modules: [] },
      }),
    ];
  }

  it("cycle grouping: single-appearance rows, one grip each, headers in first-appearance order", async () => {
    setSettings({
      groupBy: "cycles",
      subGroupBy: "none",
      orderBy: "sort_order",
    });
    mockState.issues = cycleIssues();
    renderIssues();
    await screen.findByText("Third");
    // Single-appearance: each issue grips exactly once (3, not 4).
    expect(grips()).toHaveLength(3);
    expect(screen.getByRole("heading", { name: "Sprint 1" })).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "Sprint 2" })).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "Ungrouped" })).toBeInTheDocument();
  });

  it("cycle grouping: cycle + Ungrouped headers are droppable, with no color dot", async () => {
    setSettings({
      groupBy: "cycles",
      subGroupBy: "none",
      orderBy: "sort_order",
    });
    mockState.issues = cycleIssues();
    renderIssues();
    await screen.findByText("Third");
    expect(dropIds().sort()).toEqual([
      "header:c-1:cycle:c-1",
      "header:c-2:cycle:c-2",
      "header:cycle:ungrouped:cycle:ungrouped",
    ]);
  });

  it("cycle grouping: no Ungrouped header when every issue has a cycle", async () => {
    setSettings({
      groupBy: "cycles",
      subGroupBy: "none",
      orderBy: "sort_order",
    });
    mockState.issues = [
      mkIssue("i-a", "ENG-1", "First", "s-todo", 1024, {
        project_structure: { cycle: SPRINT_1, modules: [] },
      }),
    ];
    renderIssues();
    await screen.findByText("First");
    expect(dropIds()).toEqual(["header:c-1:cycle:c-1"]);
    expect(
      screen.queryByRole("heading", { name: "Ungrouped" }),
    ).not.toBeInTheDocument();
  });

  it("cycle grouping: grips appear even under a non-Manual order (reassign-only)", async () => {
    setSettings({
      groupBy: "cycles",
      subGroupBy: "none",
      orderBy: "-updated_at",
    });
    mockState.issues = cycleIssues();
    renderIssues();
    await screen.findByText("Third");
    expect(grips()).toHaveLength(3);
  });

  it("cycle grouping: PQL mode disables grips and droppables (unchanged rule)", async () => {
    setSettings({
      groupBy: "cycles",
      subGroupBy: "none",
      orderBy: "sort_order",
    });
    mockState.issues = cycleIssues();
    renderIssues("/w/acme/p/ENG?pql=state%20%3D%20Todo");
    await screen.findByText("Third");
    expect(grips()).toHaveLength(0);
    expect(dropIds()).toHaveLength(0);
  });

  it("sub-grouped cycles+state: primary cycle headers and state sub-headers are droppable", async () => {
    setSettings({
      groupBy: "cycles",
      subGroupBy: "state",
      orderBy: "sort_order",
    });
    mockState.issues = cycleIssues();
    renderIssues();
    await screen.findByText("Third");
    // 3 primary cycle headers (incl. Ungrouped) + 2 state sub-headers
    // per primary group (Todo, Doing — showEmptyGroups renders empties).
    expect(dropIds()).toHaveLength(9);
    expect(dropIds()).toContain("header:c-1:cycle:c-1");
    expect(dropIds()).toContain("header:cycle:ungrouped:cycle:ungrouped");
    expect(dropIds()).toContain("header:c-1::s-todo:state:s-todo");
    // A state sub-header renders inside a drop zone.
    const todo = screen.getAllByRole("heading", { name: "Todo" })[0];
    expect(todo.closest('[data-testid="header-dropzone"]')).not.toBeNull();
  });

  it("cycle group quick-add: create, then POST to the group cycle; attach failure surfaces an error", async () => {
    setSettings({
      groupBy: "cycles",
      subGroupBy: "none",
      orderBy: "sort_order",
    });
    mockState.issues = cycleIssues();
    const postSpy = vi.spyOn(api, "post").mockImplementation((p: string) => {
      if (p === `${ENG}/issues`)
        return Promise.resolve(
          mkIssue("i-new", "ENG-9", "Brand new", "s-todo", 4096),
        );
      if (p === `${ENG}/cycles/c-1/issues`)
        return Promise.reject(new Error("attach boom"));
      return Promise.reject(new Error(`unexpected POST ${p}`));
    });
    renderIssues();
    await screen.findByText("Third");
    // First group's (Sprint 1's) quick-add.
    const buttons = screen.getAllByRole("button", {
      name: "Add new work item inline",
    });
    fireEvent.click(buttons[0]);
    const input = screen.getByLabelText("New work item title");
    fireEvent.change(input, { target: { value: "Brand new" } });
    fireEvent.submit(input.closest("form")!);
    await screen.findByText("attach boom");
    expect(postSpy).toHaveBeenCalledWith(
      `${ENG}/cycles/c-1/issues`,
      expect.objectContaining({ issue_ids: ["i-new"] }),
    );
  });

  it("cycle group quick-add: success attaches to the group cycle", async () => {
    setSettings({
      groupBy: "cycles",
      subGroupBy: "none",
      orderBy: "sort_order",
    });
    mockState.issues = cycleIssues();
    const postSpy = vi.spyOn(api, "post").mockImplementation((p: string) => {
      if (p === `${ENG}/issues`)
        return Promise.resolve(
          mkIssue("i-new", "ENG-9", "Brand new", "s-todo", 4096),
        );
      if (p === `${ENG}/cycles/c-1/issues`)
        return Promise.resolve({ added: 1 });
      return Promise.reject(new Error(`unexpected POST ${p}`));
    });
    renderIssues();
    await screen.findByText("Third");
    const buttons = screen.getAllByRole("button", {
      name: "Add new work item inline",
    });
    fireEvent.click(buttons[0]);
    const input = screen.getByLabelText("New work item title");
    fireEvent.change(input, { target: { value: "Brand new" } });
    fireEvent.submit(input.closest("form")!);
    await waitFor(() =>
      expect(postSpy).toHaveBeenCalledWith(
        `${ENG}/cycles/c-1/issues`,
        { issue_ids: ["i-new"] },
      ),
    );
  });

  it("cycle Ungrouped quick-add: plain create, no attach call", async () => {
    setSettings({
      groupBy: "cycles",
      subGroupBy: "none",
      orderBy: "sort_order",
    });
    mockState.issues = cycleIssues();
    const postSpy = vi.spyOn(api, "post").mockImplementation((p: string) => {
      if (p === `${ENG}/issues`)
        return Promise.resolve(
          mkIssue("i-new", "ENG-9", "Brand new", "s-todo", 4096),
        );
      return Promise.reject(new Error(`unexpected POST ${p}`));
    });
    renderIssues();
    await screen.findByText("Third");
    // Last group's (Ungrouped's) quick-add.
    const buttons = screen.getAllByRole("button", {
      name: "Add new work item inline",
    });
    fireEvent.click(buttons[buttons.length - 1]);
    const input = screen.getByLabelText("New work item title");
    fireEvent.change(input, { target: { value: "Brand new" } });
    fireEvent.submit(input.closest("form")!);
    await waitFor(() => expect(postSpy).toHaveBeenCalledTimes(1));
    expect(postSpy.mock.calls[0][0]).toBe(`${ENG}/issues`);
  });
});

describe("cycle expand-to-full-form (C132T1)", () => {
  it("expand keeps title + group cycle via the pendingCycleRef attach-after-create path", async () => {
    setSettings({
      groupBy: "cycles",
      subGroupBy: "none",
      orderBy: "sort_order",
    });
    mockState.issues = [
      mkIssue("i-a", "ENG-1", "First", "s-todo", 1024, {
        project_structure: {
          cycle: { id: "c-1", name: "Sprint 1" },
          modules: [],
        },
      }),
    ];
    const postSpy = vi.spyOn(api, "post").mockImplementation((p: string) => {
      if (p === `${ENG}/issues`)
        return Promise.resolve(
          mkIssue("i-new", "ENG-9", "Brand new", "s-todo", 4096),
        );
      if (p === `${ENG}/cycles/c-1/issues`)
        return Promise.resolve({ added: 1 });
      return Promise.reject(new Error(`unexpected POST ${p}`));
    });
    renderIssues();
    await screen.findByText("First");
    const buttons = screen.getAllByRole("button", {
      name: "Add new work item inline",
    });
    fireEvent.click(buttons[0]);
    const input = screen.getByLabelText("New work item title");
    fireEvent.change(input, { target: { value: "Brand new" } });
    fireEvent.click(screen.getByRole("button", { name: "Open full create form" }));
    // The full form opens with the typed title kept.
    const title = screen.getByLabelText("Title") as HTMLInputElement;
    expect(title.value).toBe("Brand new");
    fireEvent.click(screen.getByRole("button", { name: "Create issue" }));
    await waitFor(() =>
      expect(postSpy).toHaveBeenCalledWith(
        `${ENG}/cycles/c-1/issues`,
        { issue_ids: ["i-new"] },
      ),
    );
  });
});
