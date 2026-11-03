// @vitest-environment jsdom
// C123T1: list-view sub-group sections — sub-sections nest inside
// primary groups via the shared helper; same-as-primary and groupBy
// "none" stay flat; flattened row order (and j/k selection over it)
// is unchanged; showEmptyGroups hides empty sub-sections.
//
// The base useDisplaySettings does not carry subGroupBy yet (T0 adds
// it), so these tests drive the page through the mocked hook with the
// contract key present — the page itself only reads settings through
// effectiveSubGroupBy, which is exactly what T0's merge wires up.
import {
  afterEach,
  beforeEach,
  describe,
  expect,
  it,
  vi,
} from "vitest";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import Issues from "./Issues";
import { api } from "../lib/api";
import { installGlobalShortcuts } from "../lib/shortcuts";
import type {
  DisplaySettings,
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
  members: [] as { id: string; name: string | null; email: string }[],
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
// (which headers are droppable) stay observable — a created-by
// sub-header must render as the plain div, never a dropzone.
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

function setSettings(over: {
  groupBy: DisplaySettings["groupBy"];
  subGroupBy: SubGroupBy;
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
    showSubWorkItems: true,
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
    if (path.includes("/members"))
      return Promise.resolve({ members: mockState.members });
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
}

/** DOM order of issue names (names are unique in these fixtures). */
function domOrder(names: string[]): number[] {
  const text = document.body.textContent ?? "";
  return names.map((n) => text.indexOf(n));
}

function rowRoot(name: string): HTMLElement | null {
  return screen
    .getByText(name)
    .closest("div[class*='group/row']") as HTMLElement | null;
}

beforeEach(() => {
  stubMatchMedia(false);
  localStorage.clear();
  mockState.issues = [];
  mockState.members = [];
});

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

describe("list sub-group sections (C123T1)", () => {
  it("renders priority sub-sections inside state groups, in 4→0 order", async () => {
    setSettings({ groupBy: "state", subGroupBy: "priority" });
    mockState.issues = [
      mkIssue("i-1", "ENG-1", "Alpha issue", "s-todo", 4),
      mkIssue("i-2", "ENG-2", "Beta issue", "s-todo", 0),
      mkIssue("i-3", "ENG-3", "Gamma issue", "s-doing", 4),
    ];
    renderIssues();
    await screen.findAllByText("Alpha issue");
    // Primary group headers (h2) + sub-section headers (h3), scoped to
    // the Todo primary group (sub-section titles repeat per group).
    const todo = within(screen.getByRole("region", { name: "Todo" }));
    expect(todo.getByRole("heading", { level: 2, name: "Todo" })).toBeInTheDocument();
    const subTitles = todo
      .getAllByRole("heading", { level: 3 })
      .map((h) => h.textContent);
    expect(subTitles).toEqual(["Urgent", "High", "Medium", "Low", "None"]);
    // Flattened order = primary-group order, then sub-section order.
    const order = domOrder(["Alpha issue", "Beta issue", "Gamma issue"]);
    expect(order[0]).toBeGreaterThan(-1);
    expect(order[0]).toBeLessThan(order[1]);
    expect(order[1]).toBeLessThan(order[2]);
  });

  it("renders state sub-sections inside priority groups, in sequence order", async () => {
    setSettings({ groupBy: "priority", subGroupBy: "state" });
    mockState.issues = [
      mkIssue("i-1", "ENG-1", "Alpha issue", "s-done", 4),
      mkIssue("i-2", "ENG-2", "Beta issue", "s-todo", 4),
    ];
    renderIssues();
    await screen.findByText("Alpha issue");
    // Sub-sections follow workflow sequence (Todo before Done) even
    // though the fetch order was Done first.
    const order = domOrder(["Beta issue", "Alpha issue"]);
    expect(order[0]).toBeLessThan(order[1]);
  });

  it("renders epic sub-sections with multi-appearance, Ungrouped last", async () => {
    setSettings({ groupBy: "state", subGroupBy: "epic" });
    mockState.issues = [
      mkIssue("i-1", "ENG-1", "Alpha issue", "s-todo", 0, {
        epics: [
          { id: "e-1", name: "Auth" },
          { id: "e-2", name: "Checkout" },
        ],
      }),
      mkIssue("i-2", "ENG-2", "Beta issue", "s-todo", 0),
    ];
    renderIssues();
    await screen.findAllByText("Alpha issue");
    const todo = within(screen.getByRole("region", { name: "Todo" }));
    const subTitles = todo
      .getAllByRole("heading", { level: 3 })
      .map((h) => h.textContent);
    expect(subTitles).toEqual(["Auth", "Checkout", "Ungrouped"]);
    // Multi-appearance: Alpha renders under both epic sub-sections.
    expect(screen.getAllByText("Alpha issue")).toHaveLength(2);
    const text = todo.getByRole("heading", { level: 2, name: "Todo" }).parentElement?.parentElement?.textContent ?? "";
    expect(text.indexOf("Ungrouped")).toBeGreaterThan(text.lastIndexOf("Checkout"));
  });

  it("renders assignee sub-sections with multi-appearance, Ungrouped last (C136T1)", async () => {
    setSettings({ groupBy: "state", subGroupBy: "assignees" });
    mockState.issues = [
      mkIssue("i-1", "ENG-1", "Alpha issue", "s-todo", 0, {
        assignees: [
          { id: "u-alice", name: "Alice" },
          { id: "u-bob", name: "Bob" },
        ],
      }),
      mkIssue("i-2", "ENG-2", "Beta issue", "s-todo", 0),
      mkIssue("i-3", "ENG-3", "Gamma issue", "s-todo", 0, {
        assignees: [{ id: "u-alice", name: "Alice" }],
      }),
    ];
    renderIssues();
    await screen.findAllByText("Alpha issue");
    const todo = within(screen.getByRole("region", { name: "Todo" }));
    const subTitles = todo
      .getAllByRole("heading", { level: 3 })
      .map((h) => h.textContent);
    expect(subTitles).toEqual(["Alice", "Bob", "Ungrouped"]);
    // Multi-appearance: Alpha renders under both assignee sub-sections.
    expect(screen.getAllByText("Alpha issue")).toHaveLength(2);
  });

  it("renders label sub-sections with multi-appearance, Ungrouped last (C137T1)", async () => {
    setSettings({ groupBy: "state", subGroupBy: "labels" });
    mockState.issues = [
      mkIssue("i-1", "ENG-1", "Alpha issue", "s-todo", 0, {
        labels: [
          { id: "l-bug", name: "Bug", color: "#e22718" },
          { id: "l-feat", name: "Feature", color: "#0066b1" },
        ],
      }),
      mkIssue("i-2", "ENG-2", "Beta issue", "s-todo", 0),
      mkIssue("i-3", "ENG-3", "Gamma issue", "s-todo", 0, {
        labels: [{ id: "l-bug", name: "Bug", color: "#e22718" }],
      }),
    ];
    renderIssues();
    await screen.findAllByText("Alpha issue");
    const todo = within(screen.getByRole("region", { name: "Todo" }));
    const subTitles = todo
      .getAllByRole("heading", { level: 3 })
      .map((h) => h.textContent);
    expect(subTitles).toEqual(["Bug", "Feature", "Ungrouped"]);
    // Multi-appearance: Alpha renders under both label sub-sections.
    expect(screen.getAllByText("Alpha issue")).toHaveLength(2);
  });

  it("label sub-group hides Ungrouped when every issue is labeled (C137T1)", async () => {
    setSettings({ groupBy: "state", subGroupBy: "labels" });
    mockState.issues = [
      mkIssue("i-1", "ENG-1", "Alpha issue", "s-todo", 0, {
        labels: [{ id: "l-bug", name: "Bug", color: "#e22718" }],
      }),
    ];
    renderIssues();
    await screen.findByText("Alpha issue");
    expect(
      screen.queryByRole("heading", { level: 3, name: "Ungrouped" }),
    ).not.toBeInTheDocument();
    expect(
      screen.getByRole("heading", { level: 3, name: "Bug" }),
    ).toBeInTheDocument();
  });

  it("labels primary + labels sub-group renders flat (same-dimension rule, C137T1)", async () => {
    setSettings({ groupBy: "labels", subGroupBy: "labels" });
    mockState.issues = [
      mkIssue("i-1", "ENG-1", "Alpha issue", "s-todo", 0, {
        labels: [{ id: "l-bug", name: "Bug", color: "#e22718" }],
      }),
    ];
    renderIssues();
    await screen.findByText("Alpha issue");
    expect(screen.queryByRole("heading", { level: 3 })).not.toBeInTheDocument();
  });

  it("renders created-by sub-sections with single-appearance, Unknown last (C138T1)", async () => {
    setSettings({ groupBy: "state", subGroupBy: "createdBy" });
    mockState.members = [
      { id: "u-alice", name: "Alice", email: "alice@x.com" },
      { id: "u-bob", name: null, email: "bob@x.com" },
    ];
    mockState.issues = [
      mkIssue("i-1", "ENG-1", "Alpha issue", "s-todo", 0, {
        created_by: "u-alice",
      }),
      mkIssue("i-2", "ENG-2", "Beta issue", "s-todo", 0, {
        created_by: "u-gone",
      }),
      mkIssue("i-3", "ENG-3", "Gamma issue", "s-todo", 0, {
        created_by: "u-bob",
      }),
      mkIssue("i-4", "ENG-4", "Delta issue", "s-todo", 0, {
        created_by: "u-alice",
      }),
    ];
    renderIssues();
    await screen.findAllByText("Alpha issue");
    const todo = within(screen.getByRole("region", { name: "Todo" }));
    const subTitles = todo
      .getAllByRole("heading", { level: 3 })
      .map((h) => h.textContent);
    // Label = name ?? email; member-miss collapses to Unknown, last.
    expect(subTitles).toEqual(["Alice", "bob@x.com", "Unknown"]);
    // Single-appearance: no issue renders twice.
    expect(screen.getAllByText("Alpha issue")).toHaveLength(1);
    expect(screen.getAllByText("Beta issue")).toHaveLength(1);
    expect(screen.queryByText("u-gone")).not.toBeInTheDocument();
  });

  it("created-by sub-group hides Unknown when every creator matches a member (C138T1)", async () => {
    setSettings({ groupBy: "state", subGroupBy: "createdBy" });
    mockState.members = [
      { id: "u-alice", name: "Alice", email: "alice@x.com" },
    ];
    mockState.issues = [
      mkIssue("i-1", "ENG-1", "Alpha issue", "s-todo", 0, {
        created_by: "u-alice",
      }),
    ];
    renderIssues();
    await screen.findByText("Alpha issue");
    expect(
      screen.queryByRole("heading", { level: 3, name: "Unknown" }),
    ).not.toBeInTheDocument();
    expect(
      screen.getByRole("heading", { level: 3, name: "Alice" }),
    ).toBeInTheDocument();
  });

  it("created-by sub-header is NOT a drop target — no creator-move plan resolves (C138T1)", async () => {
    setSettings({ groupBy: "state", subGroupBy: "createdBy" });
    mockState.members = [
      { id: "u-alice", name: "Alice", email: "alice@x.com" },
    ];
    mockState.issues = [
      mkIssue("i-1", "ENG-1", "Alpha issue", "s-todo", 0, {
        created_by: "u-alice",
      }),
    ];
    renderIssues();
    await screen.findByText("Alpha issue");
    const header = screen.getByRole("heading", { level: 3, name: "Alice" });
    // The creator header renders as the plain div, never a dropzone:
    // created_by is immutable, so toHeaderTarget returns null.
    expect(
      header.closest('[data-testid="header-dropzone"]'),
    ).toBeNull();
  });

  it("created-by primary + created-by sub-group renders flat (same-dimension rule, C138T1)", async () => {
    setSettings({ groupBy: "createdBy", subGroupBy: "createdBy" });
    mockState.members = [
      { id: "u-alice", name: "Alice", email: "alice@x.com" },
    ];
    mockState.issues = [
      mkIssue("i-1", "ENG-1", "Alpha issue", "s-todo", 0, {
        created_by: "u-alice",
      }),
    ];
    renderIssues();
    await screen.findByText("Alpha issue");
    // Drop on a created-by sub-header under a created-by primary is
    // impossible: the same-dimension rule renders flat, no sub-headers.
    expect(screen.queryByRole("heading", { level: 3 })).not.toBeInTheDocument();
  });

  it("renders state-group sub-sections in canonical order, single-appearance (C139T1)", async () => {
    // One priority primary group holding states from three groups,
    // in scrambled order — sub-sections still emit triage → cancelled.
    setSettings({ groupBy: "priority", subGroupBy: "stateGroups" });
    mockState.issues = [
      mkIssue("i-1", "ENG-1", "Alpha issue", "s-done", 4),
      mkIssue("i-2", "ENG-2", "Beta issue", "s-todo", 4),
      mkIssue("i-3", "ENG-3", "Gamma issue", "s-doing", 4),
    ];
    renderIssues();
    await screen.findAllByText("Alpha issue");
    // The Urgent primary group holds all three rows; its
    // sub-sections (showEmptyGroups on) are all six canonical
    // groups, in backend order, regardless of issue order.
    const urgent = within(screen.getByRole("region", { name: "Urgent" }));
    const subTitles = urgent
      .getAllByRole("heading", { level: 3 })
      .map((h) => h.textContent);
    expect(subTitles).toEqual([
      "Triage",
      "Backlog",
      "Unstarted",
      "Started",
      "Completed",
      "Cancelled",
    ]);
    // Single-appearance: no issue renders twice.
    expect(screen.getAllByText("Alpha issue")).toHaveLength(1);
    expect(screen.getAllByText("Beta issue")).toHaveLength(1);
    expect(screen.getAllByText("Gamma issue")).toHaveLength(1);
  });

  it("state-group sub-group renders Unknown last, hidden when empty (C139T1)", async () => {
    // Priority primary: a missing-state row still renders (primary
    // state grouping would drop it — it matches no state group), so
    // the Unknown sub-section is observable here.
    setSettings({
      groupBy: "priority",
      subGroupBy: "stateGroups",
      showEmptyGroups: false,
    });
    mockState.issues = [
      mkIssue("i-1", "ENG-1", "Alpha issue", "s-todo", 4),
      mkIssue("i-2", "ENG-2", "Beta issue", "s-missing", 4),
    ];
    renderIssues();
    await screen.findAllByText("Alpha issue");
    const urgent = within(screen.getByRole("region", { name: "Urgent" }));
    // Empty canonical groups filtered; Unknown last, never a raw id.
    expect(
      urgent.getAllByRole("heading", { level: 3 }).map((h) => h.textContent),
    ).toEqual(["Unstarted", "Unknown"]);
    expect(screen.queryByText("s-missing")).not.toBeInTheDocument();
  });

  it("state-group sub-group hides Unknown when every state resolves (C139T1)", async () => {
    setSettings({
      groupBy: "priority",
      subGroupBy: "stateGroups",
      showEmptyGroups: false,
    });
    mockState.issues = [
      mkIssue("i-1", "ENG-1", "Alpha issue", "s-todo", 4),
    ];
    renderIssues();
    await screen.findByText("Alpha issue");
    const urgent = within(screen.getByRole("region", { name: "Urgent" }));
    expect(
      urgent.getAllByRole("heading", { level: 3 }).map((h) => h.textContent),
    ).toEqual(["Unstarted"]);
  });

  it("state-group sub-header is NOT a drop target — no state-group-move plan resolves (C139T1)", async () => {
    setSettings({ groupBy: "state", subGroupBy: "stateGroups" });
    mockState.issues = [
      mkIssue("i-1", "ENG-1", "Alpha issue", "s-todo", 0),
    ];
    renderIssues();
    await screen.findByText("Alpha issue");
    // Scoped to the Todo primary group: showEmptyGroups on renders
    // the six canonical sub-sections under every state primary, so
    // the document holds several "Unstarted" sub-headers.
    const todo = within(screen.getByRole("region", { name: "Todo" }));
    const header = todo.getByRole("heading", {
      level: 3,
      name: "Unstarted",
    });
    // The state-group header renders as the plain div, never a
    // dropzone: a group holds several states, so toHeaderTarget
    // returns null (no single target state).
    expect(
      header.closest('[data-testid="header-dropzone"]'),
    ).toBeNull();
  });

  it("stateGroups primary + stateGroups sub-group renders flat (same-dimension rule, C139T1)", async () => {
    setSettings({ groupBy: "stateGroups", subGroupBy: "stateGroups" });
    mockState.issues = [
      mkIssue("i-1", "ENG-1", "Alpha issue", "s-todo", 0),
    ];
    renderIssues();
    await screen.findByText("Alpha issue");
    // The same-dimension rule renders flat, no sub-headers — the
    // shared "stateGroups" literal is what makes this automatic.
    expect(screen.queryByRole("heading", { level: 3 })).not.toBeInTheDocument();
  });

  it("assignee sub-group hides Ungrouped when every issue is assigned (C136T1)", async () => {
    setSettings({ groupBy: "state", subGroupBy: "assignees" });
    mockState.issues = [
      mkIssue("i-1", "ENG-1", "Alpha issue", "s-todo", 0, {
        assignees: [{ id: "u-alice", name: "Alice" }],
      }),
    ];
    renderIssues();
    await screen.findByText("Alpha issue");
    expect(
      screen.queryByRole("heading", { level: 3, name: "Ungrouped" }),
    ).not.toBeInTheDocument();
    expect(
      screen.getByRole("heading", { level: 3, name: "Alice" }),
    ).toBeInTheDocument();
  });

  it("assignee primary + assignee sub-group renders flat (same-dimension rule, C136T1)", async () => {
    setSettings({ groupBy: "assignees", subGroupBy: "assignees" });
    mockState.issues = [
      mkIssue("i-1", "ENG-1", "Alpha issue", "s-todo", 0, {
        assignees: [{ id: "u-alice", name: "Alice" }],
      }),
    ];
    renderIssues();
    await screen.findByText("Alpha issue");
    expect(screen.queryByRole("heading", { level: 3 })).not.toBeInTheDocument();
  });

  it("same-as-primary renders flat groups (no sub-sections)", async () => {
    setSettings({ groupBy: "state", subGroupBy: "state" });
    mockState.issues = [
      mkIssue("i-1", "ENG-1", "Alpha issue", "s-todo", 4),
    ];
    renderIssues();
    await screen.findByText("Alpha issue");
    expect(screen.queryByRole("heading", { level: 3 })).not.toBeInTheDocument();
    expect(screen.getByRole("heading", { level: 2, name: "Todo" })).toBeInTheDocument();
  });

  it("groupBy none renders a flat list (no groups, no sub-sections)", async () => {
    setSettings({ groupBy: "none", subGroupBy: "state" });
    mockState.issues = [
      mkIssue("i-1", "ENG-1", "Alpha issue", "s-todo", 4),
      mkIssue("i-2", "ENG-2", "Beta issue", "s-doing", 0),
    ];
    renderIssues();
    await screen.findByText("Alpha issue");
    expect(screen.queryByRole("heading", { level: 2 })).not.toBeInTheDocument();
    expect(screen.queryByRole("heading", { level: 3 })).not.toBeInTheDocument();
    // Fetch order preserved in the flat list.
    const order = domOrder(["Alpha issue", "Beta issue"]);
    expect(order[0]).toBeLessThan(order[1]);
  });

  it("showEmptyGroups false hides empty sub-sections", async () => {
    setSettings({ groupBy: "state", subGroupBy: "priority", showEmptyGroups: false });
    mockState.issues = [
      mkIssue("i-1", "ENG-1", "Alpha issue", "s-todo", 4),
    ];
    renderIssues();
    await screen.findByText("Alpha issue");
    expect(screen.getByRole("heading", { level: 3, name: "Urgent" })).toBeInTheDocument();
    expect(screen.queryByRole("heading", { level: 3, name: "None" })).not.toBeInTheDocument();
    expect(screen.queryByRole("heading", { level: 3, name: "High" })).not.toBeInTheDocument();
  });

  it("showEmptyGroups true keeps empty sub-sections", async () => {
    setSettings({ groupBy: "state", subGroupBy: "priority", showEmptyGroups: true });
    mockState.issues = [
      mkIssue("i-1", "ENG-1", "Alpha issue", "s-todo", 4),
    ];
    renderIssues();
    await screen.findByText("Alpha issue");
    const todo = within(screen.getByRole("region", { name: "Todo" }));
    const subTitles = todo
      .getAllByRole("heading", { level: 3 })
      .map((h) => h.textContent);
    expect(subTitles).toContain("None");
    expect(subTitles).toEqual(["Urgent", "High", "Medium", "Low", "None"]);
  });

  it("row order and j/k selection follow the flattened sub-group order", async () => {
    setSettings({ groupBy: "state", subGroupBy: "priority" });
    mockState.issues = [
      mkIssue("i-1", "ENG-1", "Alpha issue", "s-todo", 4),
      mkIssue("i-2", "ENG-2", "Beta issue", "s-todo", 0),
      mkIssue("i-3", "ENG-3", "Gamma issue", "s-doing", 4),
    ];
    renderIssues();
    await screen.findByText("Alpha issue");
    const removeShortcuts = installGlobalShortcuts();
    try {
      // Initial cursor: first row of the flattened order.
      expect(rowRoot("Alpha issue")?.className).toContain("ring-1");
      fireEvent.keyDown(window, { key: "j" });
      await waitFor(() =>
        expect(rowRoot("Beta issue")?.className).toContain("ring-1"),
      );
      fireEvent.keyDown(window, { key: "j" });
      await waitFor(() =>
        expect(rowRoot("Gamma issue")?.className).toContain("ring-1"),
      );
      fireEvent.keyDown(window, { key: "k" });
      await waitFor(() =>
        expect(rowRoot("Beta issue")?.className).toContain("ring-1"),
      );
    } finally {
      removeShortcuts();
    }
  });
});
