// @vitest-environment jsdom
// C135T1: list-view Group-by-State-groups at the page level —
// sections in backend-fixed canonical order (triage, backlog,
// unstarted, started, completed, cancelled) with "Show empty
// groups" synthesizing all six, unresolved states collapsed into
// ONE Unknown group last/hidden-when-empty, no raw state id ever
// rendered as a label, NO header drop zones under state-group
// grouping (no move pipeline — a group holds several states, so
// a cross-group drop has no single target state), per-group
// quick-add ENABLED with the first-by-sequence state in the group
// as the default (so the new row stays in the group) and DISABLED
// for groups with no states at all (stateless synthesized groups
// and Unknown, whose states are not loaded).
//
// The drop zone is mocked to a marker div (IssuesListHeaderDrop
// precedent) so the wiring (presence/absence) is observable without
// dnd-kit internals; a real dnd-kit drop cannot be simulated reliably
// in jsdom, and within-group reorder routing is covered by the
// listReorder/listHeaderDrop unit tests (dispatch keeps row drops
// stack-local, cross-group row drops are silent no-ops).
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
import type { Issue, IssueState, Member } from "../lib/types";
import {
  makeQueryClient,
  stubMatchMedia,
} from "../components/shell/test-utils";

const mockState = vi.hoisted(() => ({
  settings: null as unknown as DisplaySettings & { subGroupBy: SubGroupBy },
  issues: [] as Issue[],
  membersResult: null as unknown as Promise<{ members: Member[] }>,
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
// (which headers are droppable) stay observable.
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

// Sequence order is deliberate: two unstarted states (s-todo seq 2
// before s-todo2 seq 3) so the quick-add default test can prove the
// FIRST-by-sequence state wins; NO triage and NO cancelled state,
// so those synthesized groups have no state to default to.
const states: IssueState[] = [
  { id: "s-backlog", project_id: "p", name: "Backlog", group: "backlog", color: "#888", sequence: 1, created_at: "", updated_at: "" },
  { id: "s-todo", project_id: "p", name: "Todo", group: "unstarted", color: "#888", sequence: 2, created_at: "", updated_at: "" },
  { id: "s-todo2", project_id: "p", name: "Todo 2", group: "unstarted", color: "#888", sequence: 3, created_at: "", updated_at: "" },
  { id: "s-doing", project_id: "p", name: "Doing", group: "started", color: "#888", sequence: 4, created_at: "", updated_at: "" },
  { id: "s-done", project_id: "p", name: "Done", group: "completed", color: "#888", sequence: 5, created_at: "", updated_at: "" },
];

const MEMBERS: Member[] = [
  { id: "u-alice", name: "Alice", email: "alice@x.com", role: 1 },
];

function mkIssue(
  id: string,
  displayId: string,
  name: string,
  stateId: string,
  sortOrder: number,
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
    created_by: "u-alice",
    created_at: "",
    updated_at: "",
    assignees: [],
    labels: [],
  };
}

function setSettings(over: {
  groupBy: GroupBy;
  subGroupBy: SubGroupBy;
  orderBy: OrderBy;
  showEmptyGroups?: boolean;
}) {
  mockState.settings = {
    fields: {
      state: true, priority: true, labels: true, assignees: true,
      dates: true, id: true, subitems: true, modules: true, cycle: true,
      release: true, attachments: true, links: true, dependencies: true,
    },
    groupBy: over.groupBy,
    orderBy: over.orderBy,
    showEmptyGroups: over.showEmptyGroups ?? true,
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
    if (p.includes("/members")) return mockState.membersResult;
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

function sectionLabels(): string[] {
  return Array.from(document.querySelectorAll("section[aria-label]")).map(
    (el) => el.getAttribute("aria-label") ?? "",
  );
}

function sectionEl(label: string): HTMLElement {
  const el = Array.from(document.querySelectorAll("section[aria-label]")).find(
    (s) => s.getAttribute("aria-label") === label,
  );
  if (!el) throw new Error(`no section labelled ${label}`);
  return el as HTMLElement;
}

function sectionQuickAddButton(label: string): HTMLButtonElement {
  const btn = sectionEl(label).querySelector(
    'button[aria-label="Add new work item inline"]',
  );
  if (!btn) throw new Error(`no quick-add button in section ${label}`);
  return btn as HTMLButtonElement;
}

function dropIds(): string[] {
  return screen
    .queryAllByTestId("header-dropzone")
    .map((el) => el.getAttribute("data-drop-id") ?? "");
}

function grips(): HTMLElement[] {
  return screen.queryAllByLabelText(/to reorder/);
}

beforeEach(() => {
  stubMatchMedia(false);
  localStorage.clear();
  mockState.issues = [];
  mockState.membersResult = Promise.resolve({ members: MEMBERS });
});

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

describe("state-group grouping (C135T1)", () => {
  function stateGroupIssues() {
    return [
      mkIssue("i-a", "ENG-1", "First", "s-todo", 1024),
      mkIssue("i-b", "ENG-2", "Second", "s-doing", 2048),
      mkIssue("i-c", "ENG-3", "Third", "s-done", 3072),
      // Unresolved state: no loaded state with this id.
      mkIssue("i-d", "ENG-4", "Fourth", "s-gone", 4096),
    ];
  }

  it("renders the six canonical sections in backend-fixed order, Unknown last", async () => {
    setSettings({ groupBy: "stateGroups", subGroupBy: "none", orderBy: "-updated_at" });
    mockState.issues = stateGroupIssues();
    renderIssues();
    await screen.findByText("First");
    expect(sectionLabels()).toEqual([
      "Triage",
      "Backlog",
      "Unstarted",
      "Started",
      "Completed",
      "Cancelled",
      "Unknown",
    ]);
    expect(
      screen.getByRole("heading", { name: "Unstarted" }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("heading", { name: "Unknown" }),
    ).toBeInTheDocument();
  });

  it("never renders a raw state id as a group label", async () => {
    setSettings({ groupBy: "stateGroups", subGroupBy: "none", orderBy: "-updated_at" });
    mockState.issues = stateGroupIssues();
    renderIssues();
    await screen.findByText("First");
    expect(document.body.textContent).not.toContain("s-gone");
    expect(document.body.textContent).not.toContain("s-todo");
  });

  it("with show-empty off, only groups with visible rows render (Unknown only when it has rows)", async () => {
    setSettings({ groupBy: "stateGroups", subGroupBy: "none", orderBy: "-updated_at", showEmptyGroups: false });
    mockState.issues = stateGroupIssues();
    renderIssues();
    await screen.findByText("First");
    expect(sectionLabels()).toEqual([
      "Unstarted",
      "Started",
      "Completed",
      "Unknown",
    ]);
  });

  it("with show-empty off, Unknown hides when every state resolves", async () => {
    setSettings({ groupBy: "stateGroups", subGroupBy: "none", orderBy: "-updated_at", showEmptyGroups: false });
    mockState.issues = [
      mkIssue("i-a", "ENG-1", "First", "s-todo", 1024),
      mkIssue("i-c", "ENG-3", "Third", "s-todo", 3072),
    ];
    renderIssues();
    await screen.findByText("First");
    expect(sectionLabels()).toEqual(["Unstarted"]);
  });

  it("renders NO header drop zones under state-group grouping", async () => {
    setSettings({ groupBy: "stateGroups", subGroupBy: "none", orderBy: "sort_order" });
    mockState.issues = stateGroupIssues();
    renderIssues();
    await screen.findByText("First");
    expect(dropIds()).toHaveLength(0);
  });

  it("per-group quick-add is enabled for groups with states, disabled for stateless groups and Unknown", async () => {
    setSettings({ groupBy: "stateGroups", subGroupBy: "none", orderBy: "-updated_at" });
    mockState.issues = stateGroupIssues();
    renderIssues();
    await screen.findByText("First");
    // Seven sections, each with its quick-add row.
    expect(
      screen.getAllByRole("button", { name: "Add new work item inline" }),
    ).toHaveLength(7);
    // Groups with loaded states: enabled.
    expect(sectionQuickAddButton("Backlog").disabled).toBe(false);
    expect(sectionQuickAddButton("Unstarted").disabled).toBe(false);
    expect(sectionQuickAddButton("Started").disabled).toBe(false);
    expect(sectionQuickAddButton("Completed").disabled).toBe(false);
    // No triage state and no cancelled state exist: disabled.
    expect(sectionQuickAddButton("Triage").disabled).toBe(true);
    expect(sectionQuickAddButton("Cancelled").disabled).toBe(true);
    // Unknown's states are not loaded: disabled.
    expect(sectionQuickAddButton("Unknown").disabled).toBe(true);
  });

  it("group quick-add creates with the first-by-sequence state in the group", async () => {
    setSettings({ groupBy: "stateGroups", subGroupBy: "none", orderBy: "sort_order" });
    mockState.issues = stateGroupIssues();
    const postSpy = vi.spyOn(api, "post").mockImplementation((p: string) => {
      if (p === `${ENG}/issues`)
        return Promise.resolve(
          mkIssue("i-new", "ENG-9", "Brand new", "s-todo", 4096),
        );
      return Promise.reject(new Error(`unexpected POST ${p}`));
    });
    renderIssues();
    await screen.findByText("First");
    // The Unstarted group's quick-add — not the first button on the
    // page (Triage's disabled row comes first in canonical order).
    fireEvent.click(sectionQuickAddButton("Unstarted"));
    const input = screen.getByLabelText("New work item title");
    fireEvent.change(input, { target: { value: "Brand new" } });
    fireEvent.submit(input.closest("form")!);
    await waitFor(() => expect(postSpy).toHaveBeenCalledTimes(1));
    expect(postSpy.mock.calls[0][0]).toBe(`${ENG}/issues`);
    // First-by-sequence unstarted state is s-todo (seq 2), not
    // s-todo2 (seq 3) — and no attach calls follow (plain create).
    expect(postSpy.mock.calls[0][1]).toEqual({
      name: "Brand new",
      state_id: "s-todo",
    });
  });

  it("within-group reorder grips appear under Manual rank", async () => {
    setSettings({ groupBy: "stateGroups", subGroupBy: "none", orderBy: "sort_order" });
    mockState.issues = stateGroupIssues();
    renderIssues();
    await screen.findByText("First");
    expect(grips()).toHaveLength(4);
  });
});
