// @vitest-environment jsdom
// C148T1: spreadsheet cross-group drag core — grouped mode gains a
// leading handle column (top-level rows only), primary group headers
// as drop targets, and an onDragEnd that PATCHes state/priority
// through the existing patchMutation shape, with same-group and
// createdBy/stateGroups drops as deliberate no-ops and the C68
// transition guard applied to state moves.
//
// Live-drag caveat (BoardNestedDrag.test.tsx precedent): jsdom cannot
// prove a dnd-kit pointer drag landing (collision needs real layout),
// so @dnd-kit/core is stubbed at the DndContext boundary ONLY — the
// stub renders children and captures onDragEnd/onDragOver, which the
// tests invoke directly with real drag/over ids. Everything below
// that boundary is real: the page's handler, planSpreadsheetDrop,
// patchMutation (optimistic update + rollback + invalidation), and
// the re-grouped grid. The live pointer drag itself remains a
// main-agent live-browser check.
import {
  act,
  cleanup,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { ReactNode } from "react";
import Spreadsheet from "./Spreadsheet";
import { toast } from "../components/ui/toast";
import {
  planSpreadsheetDrop,
  spreadsheetDragGroupKey,
  spreadsheetDragId,
  spreadsheetDragIssueId,
  spreadsheetOverGroupKey,
} from "../components/issue/spreadsheetDrag";
import type { Issue, IssueState, SubtaskRow } from "../lib/types";
import type { Workflow } from "../lib/workflow";

const dndCapture = vi.hoisted(() => ({
  onDragEnd: null as ((e: {
    active: { id: string };
    over: { id: string } | null;
  }) => void) | null,
  onDragOver: null as ((e: {
    active: { id: string };
    over: { id: string } | null;
  }) => void) | null,
}));

vi.mock("@dnd-kit/core", async (importOriginal) => {
  const actual =
    await importOriginal<typeof import("@dnd-kit/core")>();
  return {
    ...actual,
    DndContext: (props: {
      children: ReactNode;
      onDragEnd?: (e: {
        active: { id: string };
        over: { id: string } | null;
      }) => void;
      onDragOver?: (e: {
        active: { id: string };
        over: { id: string } | null;
      }) => void;
    }) => {
      dndCapture.onDragEnd = props.onDragEnd ?? null;
      dndCapture.onDragOver = props.onDragOver ?? null;
      return <>{props.children}</>;
    },
    useDraggable: () => ({
      attributes: {},
      listeners: {},
      setNodeRef: vi.fn(),
      isDragging: false,
    }),
    useDroppable: () => ({ setNodeRef: vi.fn(), isOver: false }),
  };
});

vi.mock("../components/ui/toast", () => ({
  toast: { add: vi.fn() },
}));

function mkIssue(over: Partial<Issue> & { id: string }): Issue {
  return {
    project_id: "proj-1",
    sequence_id: 1,
    display_id: "ENG-1",
    name: "test",
    priority: 0,
    state_id: "s",
    sort_order: 0,
    is_draft: false,
    created_by: "u2",
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    assignees: [],
    labels: [],
    ...over,
  };
}

const states: IssueState[] = [
  {
    id: "s",
    project_id: "proj-1",
    name: "Backlog",
    group: "backlog",
    color: "#999999",
    sequence: 0,
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
  },
  {
    id: "s2",
    project_id: "proj-1",
    name: "In Progress",
    group: "started",
    color: "#3b82f6",
    sequence: 1,
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
  },
  {
    id: "s3",
    project_id: "proj-1",
    name: "Done",
    group: "completed",
    color: "#4ade80",
    sequence: 2,
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
  },
];

const members = [{ id: "u2", name: "Ada", email: "ada@example.com", role: 1 }];

const BASE = "/api/v1/workspaces/acme/projects/ENG";
const STORAGE_KEY = "glance:spreadsheet-grouping:acme:ENG";

function defaultIssues(): Issue[] {
  return [
    mkIssue({
      id: "i1",
      sequence_id: 1,
      display_id: "ENG-1",
      name: "Alpha",
      priority: 2,
      state_id: "s",
      created_by: "u2",
      assignees: [{ id: "u2", name: "Ada" }],
    }),
    mkIssue({
      id: "i2",
      sequence_id: 2,
      display_id: "ENG-2",
      name: "Beta",
      priority: 4,
      state_id: "s2",
      created_by: "u9",
    }),
    mkIssue({
      id: "i3",
      sequence_id: 3,
      display_id: "ENG-3",
      name: "Gamma",
      priority: 2,
      state_id: "s",
      created_by: "u2",
    }),
    mkIssue({
      id: "i4",
      sequence_id: 4,
      display_id: "ENG-4",
      name: "Delta",
      priority: 0,
      state_id: "sX",
      created_by: "u2",
    }),
  ];
}

const mockData = vi.hoisted(() => ({
  issues: [] as Issue[],
  role: 15,
  workflow: null as Workflow | null,
}));

vi.mock("../lib/api", () => ({
  api: {
    get: vi.fn((path: string) => {
      if (path.includes("/states")) {
        return Promise.resolve({ states });
      }
      if (path.includes("/members")) {
        return Promise.resolve({ members });
      }
      if (path.includes("/workflow")) {
        return Promise.resolve(
          mockData.workflow ?? { states, transitions: [] },
        );
      }
      if (path.includes("/labels")) {
        return Promise.resolve({ labels: [] });
      }
      if (path.includes("/estimates")) {
        return Promise.resolve({ estimates: [] });
      }
      if (path.includes("/custom-fields")) {
        return Promise.resolve({ custom_fields: [] });
      }
      if (path.includes("/views")) {
        return Promise.resolve({ views: [] });
      }
      if (path.includes("/epics")) {
        return Promise.resolve({ epics: [] });
      }
      if (path.includes("/issues")) {
        return Promise.resolve({
          results: mockData.issues,
          next_cursor: undefined,
        });
      }
      if (path === BASE) {
        return Promise.resolve({
          id: "proj-1",
          identifier: "ENG",
          name: "Eng",
        });
      }
      if (path === "/api/v1/workspaces/acme") {
        return Promise.resolve({
          id: "ws-1",
          slug: "acme",
          name: "Acme",
          role: mockData.role,
          slack_configured: false,
          github_webhook_configured: false,
          created_at: "2026-01-01T00:00:00Z",
          updated_at: "2026-01-01T00:00:00Z",
        });
      }
      return Promise.reject(new Error("unexpected path: " + path));
    }),
    post: vi.fn(),
    patch: vi.fn(),
    del: vi.fn(),
  },
  ApiError: class ApiError extends Error {
    code?: string;
    details?: unknown;
    constructor(status: number, body: string, code?: string, details?: unknown) {
      super(body || `Request failed with status ${status}`);
      this.name = "ApiError";
      this.code = code;
      this.details = details;
    }
  },
}));

vi.mock("../lib/auth", () => ({
  useAuth: () => ({
    user: { id: "u1", name: "U", email: "u@example.com" },
    loading: false,
  }),
}));

function renderSpreadsheet(
  initialEntry = "/w/acme/p/ENG/spreadsheet",
) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return render(
    <MemoryRouter initialEntries={[initialEntry]}>
      <QueryClientProvider client={client}>
        <Routes>
          <Route
            path="/w/:slug/p/:identifier/spreadsheet"
            element={<Spreadsheet />}
          />
        </Routes>
      </QueryClientProvider>
    </MemoryRouter>,
  );
}

function groupHeaders(): string[] {
  const table = screen.queryByRole("table");
  if (!table) return [];
  return Array.from(
    table.querySelectorAll("tr[data-spreadsheet-group-header]"),
  ).map((tr) => (tr.textContent ?? "").replace(/\s+/g, " ").trim());
}

function rowLabels(): string[] {
  const table = screen.getByRole("table");
  return within(table)
    .getAllByRole("link")
    .map((row) => row.getAttribute("aria-label") ?? "");
}

function handles(): HTMLElement[] {
  return screen.queryAllByRole("button", {
    name: "Drag to move to another group",
  });
}

function setGrouping(value: unknown) {
  localStorage.setItem(STORAGE_KEY, JSON.stringify(value));
}

async function renderGrouped(
  groupBy: string,
  subGroupBy = "none",
  expectHandles = true,
) {
  setGrouping({ groupBy, subGroupBy });
  renderSpreadsheet();
  await screen.findByRole("button", { name: "Sort by Title" });
  if (expectHandles) {
    // Handles render only once the workspace query resolves canEdit —
    // waiting for them also settles the canEdit gate before any drop.
    await waitFor(() => expect(handles().length).toBeGreaterThan(0));
  }
}

/** Invoke the page's captured onDragEnd (dnd-kit stub boundary). */
function drop(activeId: string, overId: string | null) {
  expect(dndCapture.onDragEnd).not.toBeNull();
  act(() => {
    dndCapture.onDragEnd!({
      active: { id: activeId },
      over: overId === null ? null : { id: overId },
    });
  });
}

function dragOver(overId: string | null) {
  expect(dndCapture.onDragOver).not.toBeNull();
  act(() => {
    dndCapture.onDragOver!({
      active: { id: "spreadsheet-drag:s:i1" },
      over: overId === null ? null : { id: overId },
    });
  });
}

async function patchMock() {
  const { api } = await import("../lib/api");
  return api.patch as ReturnType<typeof vi.fn>;
}

/** Default PATCH behavior: apply the body server-side, like the real
 *  endpoint, so the post-move refetch keeps the row in its new group. */
function applyPatchToFixture(body: Record<string, unknown>, id: string) {
  const issue = mockData.issues.find((i) => i.id === id);
  if (!issue) return null;
  if (body.state_id !== undefined) issue.state_id = body.state_id as string;
  if (body.priority !== undefined) issue.priority = body.priority as number;
  return { ...issue };
}

beforeEach(async () => {
  localStorage.clear();
  vi.clearAllMocks();
  dndCapture.onDragEnd = null;
  dndCapture.onDragOver = null;
  mockData.issues = defaultIssues();
  mockData.role = 15;
  mockData.workflow = null;
  const patch = await patchMock();
  patch.mockReset();
  patch.mockImplementation((path: string, body: Record<string, unknown>) => {
    const m = /\/issues\/([^/?]+)/.exec(path);
    const updated = m ? applyPatchToFixture(body, m[1]) : null;
    return Promise.resolve(updated ?? {});
  });
});

describe("spreadsheetDrag id helpers", () => {
  it("round-trips compound ids, including colon-bearing group keys", () => {
    expect(spreadsheetDragId("s2", "i2")).toBe("spreadsheet-drag:s2:i2");
    expect(spreadsheetDragIssueId("spreadsheet-drag:s2:i2")).toBe("i2");
    expect(spreadsheetDragGroupKey("spreadsheet-drag:s2:i2")).toBe("s2");
    // priority + createdBy-unknown keys contain colons; the LAST
    // colon still separates the issue id.
    expect(
      spreadsheetDragGroupKey("spreadsheet-drag:priority:4:i1"),
    ).toBe("priority:4");
    expect(
      spreadsheetDragGroupKey("spreadsheet-drag:createdby:unknown:i2"),
    ).toBe("createdby:unknown");
    expect(spreadsheetDragIssueId("spreadsheet-drag:priority:4:i1")).toBe(
      "i1",
    );
  });

  it("unwrappers are identity/null for non-spreadsheet ids", () => {
    expect(spreadsheetDragIssueId("i2")).toBe("i2");
    expect(spreadsheetDragGroupKey("i2")).toBeNull();
    expect(spreadsheetDragGroupKey("spreadsheet-drag:nocolon")).toBeNull();
  });

  it("over ids resolve to the primary group, or null", () => {
    expect(spreadsheetOverGroupKey("spreadsheet-group:s2")).toBe("s2");
    expect(spreadsheetOverGroupKey("spreadsheet-group:priority:4")).toBe(
      "priority:4",
    );
    expect(spreadsheetOverGroupKey("spreadsheet-row-drop:s2:i2")).toBe("s2");
    expect(
      spreadsheetOverGroupKey("spreadsheet-row-drop:stategroup:started:i2"),
    ).toBe("stategroup:started");
    expect(spreadsheetOverGroupKey("some-other-id")).toBeNull();
    expect(spreadsheetOverGroupKey("spreadsheet-group:")).toBe("");
  });
});

describe("planSpreadsheetDrop", () => {
  const issue = mkIssue({ id: "i1", state_id: "s", priority: 2 });
  const stateNames = new Map([
    ["s", "Backlog"],
    ["s2", "In Progress"],
    ["s3", "Done"],
  ]);

  it("state drop PATCHes state_id", () => {
    expect(
      planSpreadsheetDrop({
        groupBy: "state",
        sourceGroupKey: "s",
        targetGroupKey: "s2",
        issue,
        workflow: null,
        stateNames,
      }),
    ).toEqual({ kind: "patch", body: { state_id: "s2" } });
  });

  it("priority drop PATCHes priority", () => {
    expect(
      planSpreadsheetDrop({
        groupBy: "priority",
        sourceGroupKey: "priority:2",
        targetGroupKey: "priority:4",
        issue,
        workflow: null,
        stateNames,
      }),
    ).toEqual({ kind: "patch", body: { priority: 4 } });
  });

  it("same-group drop is a no-op in every dimension", () => {
    for (const groupBy of [
      "state",
      "priority",
      "createdBy",
      "stateGroups",
      "epic",
    ]) {
      expect(
        planSpreadsheetDrop({
          groupBy,
          sourceGroupKey: "s",
          targetGroupKey: "s",
          issue,
          workflow: null,
          stateNames,
        }),
      ).toEqual({ kind: "noop", reason: "same-group" });
    }
  });

  it("state Ungrouped target is a no-op (no state to PATCH to)", () => {
    expect(
      planSpreadsheetDrop({
        groupBy: "state",
        sourceGroupKey: "s",
        targetGroupKey: "state:ungrouped",
        issue,
        workflow: null,
        stateNames,
      }),
    ).toEqual({ kind: "noop", reason: "ungrouped-state" });
  });

  it("blocked state transition PATCHes nothing and names permitted states", () => {
    const workflow: Workflow = {
      states,
      transitions: [
        { from_state_id: "s2", to_state_id: "s" },
        { from_state_id: "s", to_state_id: "s3" },
      ],
    };
    expect(
      planSpreadsheetDrop({
        groupBy: "state",
        sourceGroupKey: "s",
        targetGroupKey: "s2",
        issue,
        workflow,
        stateNames,
      }),
    ).toEqual({ kind: "blocked", permittedNames: ["Done"] });
    // The permitted move still PATCHes.
    expect(
      planSpreadsheetDrop({
        groupBy: "state",
        sourceGroupKey: "s",
        targetGroupKey: "s3",
        issue,
        workflow,
        stateNames,
      }),
    ).toEqual({ kind: "patch", body: { state_id: "s3" } });
  });

  it("unrestricted workflow (null or zero transitions) allows state moves", () => {
    const empty: Workflow = { states, transitions: [] };
    for (const workflow of [null, empty]) {
      expect(
        planSpreadsheetDrop({
          groupBy: "state",
          sourceGroupKey: "s",
          targetGroupKey: "s2",
          issue,
          workflow,
          stateNames,
        }),
      ).toEqual({ kind: "patch", body: { state_id: "s2" } });
    }
  });

  it("createdBy/stateGroups cross-group drops are deliberate no-ops", () => {
    expect(
      planSpreadsheetDrop({
        groupBy: "createdBy",
        sourceGroupKey: "createdby:unknown",
        targetGroupKey: "u2",
        issue,
        workflow: null,
        stateNames,
      }),
    ).toEqual({ kind: "noop", reason: "created-by" });
    expect(
      planSpreadsheetDrop({
        groupBy: "stateGroups",
        sourceGroupKey: "stategroup:started",
        targetGroupKey: "stategroup:backlog",
        issue,
        workflow: null,
        stateNames,
      }),
    ).toEqual({ kind: "noop", reason: "state-group" });
  });

  it("membership dimensions are pending-dimension no-ops in T1", () => {
    for (const groupBy of [
      "epic",
      "assignees",
      "labels",
      "modules",
      "cycles",
      "releases",
    ]) {
      expect(
        planSpreadsheetDrop({
          groupBy,
          sourceGroupKey: "a",
          targetGroupKey: "b",
          issue,
          workflow: null,
          stateNames,
        }),
      ).toEqual({ kind: "noop", reason: "pending-dimension" });
    }
  });

  it("malformed priority target is a no-op, never a PATCH", () => {
    expect(
      planSpreadsheetDrop({
        groupBy: "priority",
        sourceGroupKey: "priority:2",
        targetGroupKey: "bogus",
        issue,
        workflow: null,
        stateNames,
      }),
    ).toEqual({ kind: "noop", reason: "unknown-target" });
  });
});

describe("Spreadsheet drag rendering", () => {
  it("flat mode renders no handles, no DndContext, no drag attrs", async () => {
    renderSpreadsheet();
    await screen.findByRole("button", { name: "Sort by Title" });
    expect(handles()).toHaveLength(0);
    expect(dndCapture.onDragEnd).toBeNull();
    expect(dndCapture.onDragOver).toBeNull();
    expect(groupHeaders()).toEqual([]);
    const table = screen.getByRole("table");
    expect(
      table.querySelectorAll("[data-spreadsheet-drag-group]"),
    ).toHaveLength(0);
    expect(
      screen.queryByRole("columnheader", { name: "Drag to move" }),
    ).toBeNull();
  });

  it("grouped mode renders a handle per top-level row and a narrow header", async () => {
    await renderGrouped("state");
    expect(groupHeaders()).toEqual([
      "Backlog (2)",
      "In Progress (1)",
      "Ungrouped (1)",
    ]);
    expect(handles()).toHaveLength(4);
    expect(
      screen.getByRole("columnheader", { name: "Drag to move" }),
    ).toBeInTheDocument();
    // Header colspan grows by 1 for the handle column: 13 columns +
    // 0 custom fields + 1 handle.
    const table = screen.getByRole("table");
    const headerTd = table.querySelector(
      "tr[data-spreadsheet-group-header] td",
    ) as HTMLElement;
    expect(headerTd.getAttribute("colspan")).toBe("14");
  });

  it("nested rows carry no handle, only an empty alignment cell", async () => {
    const child: SubtaskRow = {
      id: "c1",
      display_id: "ENG-9",
      title: "Child Task",
      state_id: "s2",
      priority: 1,
      assignee_ids: [],
      sort_order: 1,
    };
    const parent = mkIssue({
      id: "p1",
      sequence_id: 1,
      display_id: "ENG-1",
      name: "Parent",
      state_id: "s",
      subtasks: [child],
    });
    const childIssue = mkIssue({
      id: "c1",
      sequence_id: 2,
      display_id: "ENG-9",
      name: "Child Task",
      state_id: "s2",
    });
    const other = mkIssue({
      id: "o1",
      sequence_id: 3,
      display_id: "ENG-5",
      name: "Other Started",
      state_id: "s2",
    });
    mockData.issues = [parent, childIssue, other];
    await renderGrouped("state");
    // Two top-level rows → two handles; the nested child has none.
    expect(handles()).toHaveLength(2);
    expect(rowLabels()).toEqual([
      "Open issue ENG-1: Parent",
      "Open issue ENG-9: Child Task",
      "Open issue ENG-5: Other Started",
    ]);
    const table = screen.getByRole("table");
    const nestedRow = table.querySelector(
      "tr[data-nested-subtask]",
    ) as HTMLElement;
    expect(
      within(nestedRow).queryByRole("button", {
        name: "Drag to move to another group",
      }),
    ).toBeNull();
  });

  it("guest (canEdit false) gets no handles in grouped mode", async () => {
    mockData.role = 0;
    await renderGrouped("state", "none", false);
    expect(groupHeaders()).toEqual([
      "Backlog (2)",
      "In Progress (1)",
      "Ungrouped (1)",
    ]);
    expect(handles()).toHaveLength(0);
  });

  it("drop highlight follows the group under the pointer, header or row", async () => {
    await renderGrouped("state");
    const table = screen.getByRole("table");
    const header = (key: string) =>
      table.querySelector(
        `tr[data-spreadsheet-group-header="${key}"]`,
      ) as HTMLElement;
    expect(header("s2").className).toContain("bg-wash");
    expect(header("s2").className).not.toContain("bg-accent-blue/10");
    // Over the target header itself.
    dragOver("spreadsheet-group:s2");
    expect(header("s2").className).toContain("bg-accent-blue/10");
    expect(header("s").className).not.toContain("bg-accent-blue/10");
    // Over a row of that group: the group's header highlights instead.
    dragOver("spreadsheet-row-drop:s2:i2");
    expect(header("s2").className).toContain("bg-accent-blue/10");
    // Leaving all targets clears the highlight.
    dragOver(null);
    expect(header("s2").className).not.toContain("bg-accent-blue/10");
  });
});

describe("Spreadsheet drag moves (page wiring)", () => {
  it("state drop PATCHes state_id and re-groups the row", async () => {
    await renderGrouped("state");
    const patch = await patchMock();
    drop("spreadsheet-drag:s2:i2", "spreadsheet-group:s");
    await waitFor(() => {
      expect(patch).toHaveBeenCalledWith(
        expect.stringContaining("/issues/i2"),
        { state_id: "s" },
      );
    });
    await waitFor(() => {
      expect(groupHeaders()).toEqual(["Backlog (3)", "Ungrouped (1)"]);
    });
    // Groups filter the underlying row order: the moved row slots
    // into its position in that order (i2 between i1 and i3), not
    // appended at the end of the group.
    expect(rowLabels()).toEqual([
      "Open issue ENG-1: Alpha",
      "Open issue ENG-2: Beta",
      "Open issue ENG-3: Gamma",
      "Open issue ENG-4: Delta",
    ]);
  });

  it("state drop onto a foreign row resolves to that row's group", async () => {
    await renderGrouped("state");
    const patch = await patchMock();
    drop("spreadsheet-drag:s2:i2", "spreadsheet-row-drop:s:i1");
    await waitFor(() => {
      expect(patch).toHaveBeenCalledWith(
        expect.stringContaining("/issues/i2"),
        { state_id: "s" },
      );
    });
    await waitFor(() => {
      expect(groupHeaders()).toEqual(["Backlog (3)", "Ungrouped (1)"]);
    });
  });

  it("priority drop PATCHes priority and re-groups the row", async () => {
    await renderGrouped("priority");
    const patch = await patchMock();
    drop("spreadsheet-drag:priority:2:i1", "spreadsheet-group:priority:4");
    await waitFor(() => {
      expect(patch).toHaveBeenCalledWith(
        expect.stringContaining("/issues/i1"),
        { priority: 4 },
      );
    });
    await waitFor(() => {
      expect(groupHeaders()).toEqual([
        "Urgent (2)",
        "Medium (1)",
        "None (1)",
      ]);
    });
  });

  it("same-group drop PATCHes nothing (header or foreign row)", async () => {
    await renderGrouped("state");
    const patch = await patchMock();
    drop("spreadsheet-drag:s:i1", "spreadsheet-group:s");
    drop("spreadsheet-drag:s:i1", "spreadsheet-row-drop:s:i3");
    expect(patch).not.toHaveBeenCalled();
    expect(groupHeaders()).toEqual([
      "Backlog (2)",
      "In Progress (1)",
      "Ungrouped (1)",
    ]);
  });

  it("createdBy/stateGroups cross-group drops PATCH nothing and show no error", async () => {
    await renderGrouped("createdBy");
    const patch = await patchMock();
    drop("spreadsheet-drag:createdby:unknown:i2", "spreadsheet-group:u2");
    expect(patch).not.toHaveBeenCalled();
    expect(toast.add).not.toHaveBeenCalled();
    expect(screen.queryByRole("alert")).toBeNull();

    // Fresh page for the stateGroups dimension.
    cleanup();
    localStorage.clear();
    setGrouping({ groupBy: "stateGroups", subGroupBy: "none" });
    renderSpreadsheet();
    await screen.findByRole("button", { name: "Sort by Title" });
    await waitFor(() => expect(handles().length).toBeGreaterThan(0));
    await waitFor(() => {
      expect(groupHeaders()).toEqual([
        "Backlog (2)",
        "Started (1)",
        "Unknown (1)",
      ]);
    });
    drop(
      "spreadsheet-drag:stategroup:started:i2",
      "spreadsheet-group:stategroup:backlog",
    );
    expect(patch).not.toHaveBeenCalled();
    expect(toast.add).not.toHaveBeenCalled();
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("guest drop is refused: no PATCH", async () => {
    mockData.role = 0;
    await renderGrouped("state", "none", false);
    const patch = await patchMock();
    drop("spreadsheet-drag:s2:i2", "spreadsheet-group:s");
    expect(patch).not.toHaveBeenCalled();
  });

  it("drop with no target is a no-op", async () => {
    await renderGrouped("state");
    const patch = await patchMock();
    drop("spreadsheet-drag:s2:i2", null);
    // An over id that resolves to no group (e.g. a sub-group header,
    // which carries no droppable) is the same no-op.
    drop("spreadsheet-drag:s2:i2", "bogus-id");
    expect(patch).not.toHaveBeenCalled();
  });

  it("blocked state transition PATCHes nothing and surfaces Move blocked", async () => {
    mockData.workflow = {
      states,
      transitions: [
        { from_state_id: "s2", to_state_id: "s" },
        { from_state_id: "s", to_state_id: "s3" },
      ],
    };
    await renderGrouped("state");
    const patch = await patchMock();
    drop("spreadsheet-drag:s:i1", "spreadsheet-group:s2");
    expect(patch).not.toHaveBeenCalled();
    expect(toast.add).toHaveBeenCalledWith({
      title: "Move blocked",
      description:
        "State transition not allowed by the project's workflow. Permitted next states: Done",
      type: "error",
    });
    expect(groupHeaders()).toEqual([
      "Backlog (2)",
      "In Progress (1)",
      "Ungrouped (1)",
    ]);
  });

  it("server 409 backstop: no PATCH sticks, row rolls back, Move blocked toast", async () => {
    await renderGrouped("state");
    const patch = await patchMock();
    const { ApiError } = await import("../lib/api");
    patch.mockRejectedValueOnce(
      new ApiError(409, "denied", "state_transition_not_allowed", {
        allowed: ["s2"],
      }),
    );
    drop("spreadsheet-drag:s:i1", "spreadsheet-group:s2");
    await waitFor(() => {
      expect(patch).toHaveBeenCalledWith(
        expect.stringContaining("/issues/i1"),
        { state_id: "s2" },
      );
    });
    // Rollback: the row is back under Backlog, counts restored.
    await waitFor(() => {
      expect(groupHeaders()).toEqual([
        "Backlog (2)",
        "In Progress (1)",
        "Ungrouped (1)",
      ]);
    });
    expect(toast.add).toHaveBeenCalledWith({
      title: "Move blocked",
      description:
        "State transition not allowed by the project's workflow. Permitted next states: In Progress",
      type: "error",
    });
  });

  it("failed PATCH rolls the row back to its source group", async () => {
    await renderGrouped("state");
    const patch = await patchMock();
    const { ApiError } = await import("../lib/api");
    patch.mockRejectedValueOnce(new ApiError(500, "boom"));
    drop("spreadsheet-drag:s2:i2", "spreadsheet-group:s");
    await waitFor(() => {
      expect(patch).toHaveBeenCalled();
    });
    await waitFor(() => {
      expect(groupHeaders()).toEqual([
        "Backlog (2)",
        "In Progress (1)",
        "Ungrouped (1)",
      ]);
    });
    expect(rowLabels()).toEqual([
      "Open issue ENG-1: Alpha",
      "Open issue ENG-3: Gamma",
      "Open issue ENG-2: Beta",
      "Open issue ENG-4: Delta",
    ]);
    expect(await screen.findByText("boom")).toBeInTheDocument();
  });

  it("sub-group headers are not drop targets: drop over nothing is a no-op", async () => {
    await renderGrouped("priority", "state");
    const patch = await patchMock();
    const table = screen.getByRole("table");
    const subHeaders = table.querySelectorAll(
      "tr[data-spreadsheet-sub-group-header]",
    );
    expect(subHeaders.length).toBeGreaterThan(0);
    for (const h of Array.from(subHeaders)) {
      expect(h.querySelector("button")).toBeNull();
    }
    // Real dnd-kit reports over === null above a non-droppable header.
    drop("spreadsheet-drag:priority:2:i1", null);
    expect(patch).not.toHaveBeenCalled();
  });
});
