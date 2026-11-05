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
  fireEvent,
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
  planSpreadsheetReorder,
  planSpreadsheetReorderToEnd,
  planSpreadsheetSubSectionDrop,
  planSpreadsheetSubSectionHeaderDrop,
  spreadsheetDragGroupKey,
  spreadsheetDragId,
  spreadsheetDragIssueId,
  spreadsheetOverGroupKey,
  spreadsheetReorderIndicatorSide,
  spreadsheetReorderRun,
  spreadsheetSubGroupDroppableId,
  spreadsheetSubGroupDropTarget,
  SPREADSHEET_GROUP_DROPPABLE_PREFIX,
  SPREADSHEET_ROW_DROPPABLE_PREFIX,
  SPREADSHEET_SUB_GROUP_DROPPABLE_PREFIX,
} from "../components/issue/spreadsheetDrag";
import type { Issue, IssueState, SubtaskRow } from "../lib/types";
import type { Workflow } from "../lib/workflow";

const dndCapture = vi.hoisted(() => ({
  onDragStart: null as ((e: {
    active: { id: string };
  }) => void) | null,
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
      onDragStart?: (e: {
        active: { id: string };
      }) => void;
      onDragEnd?: (e: {
        active: { id: string };
        over: { id: string } | null;
      }) => void;
      onDragOver?: (e: {
        active: { id: string };
        over: { id: string } | null;
      }) => void;
    }) => {
      dndCapture.onDragStart = props.onDragStart ?? null;
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

/** Invoke the page's captured onDragStart (dnd-kit stub boundary). */
function dragStart(activeId: string) {
  expect(dndCapture.onDragStart).not.toBeNull();
  act(() => {
    dndCapture.onDragStart!({ active: { id: activeId } });
  });
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

async function postMock() {
  const { api } = await import("../lib/api");
  return api.post as ReturnType<typeof vi.fn>;
}

async function delMock() {
  const { api } = await import("../lib/api");
  return api.del as ReturnType<typeof vi.fn>;
}

/** Default PATCH behavior: apply the body server-side, like the real
 *  endpoint, so the post-move refetch keeps the row in its new group. */
function applyPatchToFixture(body: Record<string, unknown>, id: string) {
  const issue = mockData.issues.find((i) => i.id === id);
  if (!issue) return null;
  if (body.state_id !== undefined) issue.state_id = body.state_id as string;
  if (body.priority !== undefined) issue.priority = body.priority as number;
  if (body.sort_order !== undefined)
    issue.sort_order = body.sort_order as number;
  return { ...issue };
}

beforeEach(async () => {
  localStorage.clear();
  vi.clearAllMocks();
  dndCapture.onDragStart = null;
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
  // Membership mocks default to a resolving no-op; T2 tests install
  // server-side fixture mutations per test (mirroring the PATCH
  // pattern above) so the post-move refetch re-groups the row.
  const post = await postMock();
  post.mockReset();
  post.mockImplementation(() => Promise.resolve({}));
  const del = await delMock();
  del.mockReset();
  del.mockImplementation(() => Promise.resolve({}));
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

  it("sub-group header ids round-trip, including colon-bearing keys", () => {
    // Both keys can contain colons, so the `|` split — not a colon
    // split — is what separates them.
    expect(spreadsheetSubGroupDroppableId("s", "priority:4")).toBe(
      "spreadsheet-sub-group:s|priority:4",
    );
    expect(
      spreadsheetSubGroupDropTarget(
        spreadsheetSubGroupDroppableId("priority:4", "state:ungrouped"),
      ),
    ).toEqual({ groupKey: "priority:4", sectionKey: "state:ungrouped" });
    expect(
      spreadsheetSubGroupDropTarget(
        spreadsheetSubGroupDroppableId("s", "cycle:ungrouped"),
      ),
    ).toEqual({ groupKey: "s", sectionKey: "cycle:ungrouped" });
    expect(
      spreadsheetSubGroupDropTarget(
        spreadsheetSubGroupDroppableId("stategroup:started", "lb1"),
      ),
    ).toEqual({ groupKey: "stategroup:started", sectionKey: "lb1" });
  });

  it("sub-group header parse returns null for non-matching / malformed ids", () => {
    expect(spreadsheetSubGroupDropTarget("spreadsheet-group:s")).toBeNull();
    expect(
      spreadsheetSubGroupDropTarget("spreadsheet-row-drop:s:i2"),
    ).toBeNull();
    expect(spreadsheetSubGroupDropTarget("spreadsheet-drag:s:i1")).toBeNull();
    expect(spreadsheetSubGroupDropTarget("spreadsheet-sub-group:s")).toBeNull();
    expect(
      spreadsheetSubGroupDropTarget("spreadsheet-sub-group:|priority:2"),
    ).toBeNull();
    expect(
      spreadsheetSubGroupDropTarget("spreadsheet-sub-group:s|"),
    ).toBeNull();
    expect(
      spreadsheetSubGroupDropTarget("spreadsheet-sub-group:s|a|b"),
    ).toBeNull();
    expect(spreadsheetSubGroupDropTarget("other")).toBeNull();
    expect(spreadsheetSubGroupDropTarget("")).toBeNull();
  });

  it("over ids resolve to the primary group for sub-group header ids", () => {
    expect(
      spreadsheetOverGroupKey("spreadsheet-sub-group:s|priority:4"),
    ).toBe("s");
    expect(
      spreadsheetOverGroupKey(
        "spreadsheet-sub-group:priority:4|state:ungrouped",
      ),
    ).toBe("priority:4");
    expect(spreadsheetOverGroupKey("spreadsheet-sub-group:s")).toBeNull();
    expect(
      spreadsheetOverGroupKey("spreadsheet-sub-group:|priority:2"),
    ).toBeNull();
  });

  it("the sub-group prefix collides with neither the group nor the row prefix", () => {
    const subId = spreadsheetSubGroupDroppableId("s", "priority:2");
    expect(subId.startsWith(SPREADSHEET_GROUP_DROPPABLE_PREFIX)).toBe(false);
    expect(subId.startsWith(SPREADSHEET_ROW_DROPPABLE_PREFIX)).toBe(false);
    expect(
      "spreadsheet-group:s".startsWith(SPREADSHEET_SUB_GROUP_DROPPABLE_PREFIX),
    ).toBe(false);
    expect(
      "spreadsheet-row-drop:s:i2".startsWith(
        SPREADSHEET_SUB_GROUP_DROPPABLE_PREFIX,
      ),
    ).toBe(false);
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

  it("epic drop attaches; Ungrouped and already-attached are no-ops", () => {
    const withEpic = mkIssue({
      id: "i1",
      state_id: "s",
      priority: 2,
      epics: [{ id: "ep1", name: "Epic One", archived_at: null }],
    });
    const base = {
      groupBy: "epic",
      workflow: null,
      stateNames,
    } as const;
    expect(
      planSpreadsheetDrop({
        ...base,
        sourceGroupKey: "ep1",
        targetGroupKey: "ep2",
        issue: withEpic,
      }),
    ).toEqual({ kind: "attach", dimension: "epic", memberId: "ep2" });
    // Board planEpicDrop: Ungrouped is a deliberate no-op.
    expect(
      planSpreadsheetDrop({
        ...base,
        sourceGroupKey: "ep1",
        targetGroupKey: "epic:ungrouped",
        issue: withEpic,
      }),
    ).toEqual({ kind: "noop", reason: "ungrouped" });
    // Already-attached (live membership) is a no-op.
    expect(
      planSpreadsheetDrop({
        ...base,
        sourceGroupKey: "ep2",
        targetGroupKey: "ep1",
        issue: withEpic,
      }),
    ).toEqual({ kind: "noop", reason: "already-member" });
    // An archived-only epic membership does NOT count as attached
    // (Board checks !archived_at) — the drop attaches.
    const archivedOnly = mkIssue({
      id: "i1",
      state_id: "s",
      priority: 2,
      epics: [{ id: "ep1", name: "Epic One", archived_at: "2026-01-01" }],
    });
    expect(
      planSpreadsheetDrop({
        ...base,
        sourceGroupKey: "epic:ungrouped",
        targetGroupKey: "ep1",
        issue: archivedOnly,
      }),
    ).toEqual({ kind: "attach", dimension: "epic", memberId: "ep1" });
  });

  it("assignee drop attaches; Ungrouped and already-assigned are no-ops", () => {
    const withAssignee = mkIssue({
      id: "i1",
      state_id: "s",
      priority: 2,
      assignees: [{ id: "u2", name: "Ada" }],
    });
    const base = {
      groupBy: "assignees",
      workflow: null,
      stateNames,
    } as const;
    expect(
      planSpreadsheetDrop({
        ...base,
        sourceGroupKey: "u2",
        targetGroupKey: "u3",
        issue: withAssignee,
      }),
    ).toEqual({ kind: "attach", dimension: "assignees", memberId: "u3" });
    expect(
      planSpreadsheetDrop({
        ...base,
        sourceGroupKey: "u2",
        targetGroupKey: "assignee:ungrouped",
        issue: withAssignee,
      }),
    ).toEqual({ kind: "noop", reason: "ungrouped" });
    expect(
      planSpreadsheetDrop({
        ...base,
        sourceGroupKey: "u3",
        targetGroupKey: "u2",
        issue: withAssignee,
      }),
    ).toEqual({ kind: "noop", reason: "already-member" });
  });

  it("label drop attaches; Ungrouped and already-labeled are no-ops", () => {
    const withLabel = mkIssue({
      id: "i1",
      state_id: "s",
      priority: 2,
      labels: [{ id: "lb1", name: "Red", color: "#f00" }],
    });
    const base = {
      groupBy: "labels",
      workflow: null,
      stateNames,
    } as const;
    expect(
      planSpreadsheetDrop({
        ...base,
        sourceGroupKey: "lb1",
        targetGroupKey: "lb2",
        issue: withLabel,
      }),
    ).toEqual({ kind: "attach", dimension: "labels", memberId: "lb2" });
    expect(
      planSpreadsheetDrop({
        ...base,
        sourceGroupKey: "lb1",
        targetGroupKey: "label:ungrouped",
        issue: withLabel,
      }),
    ).toEqual({ kind: "noop", reason: "ungrouped" });
    expect(
      planSpreadsheetDrop({
        ...base,
        sourceGroupKey: "lb2",
        targetGroupKey: "lb1",
        issue: withLabel,
      }),
    ).toEqual({ kind: "noop", reason: "already-member" });
  });

  it("module drop attaches; Ungrouped and already-member are no-ops", () => {
    const withModule = mkIssue({
      id: "i1",
      state_id: "s",
      priority: 2,
      project_structure: {
        modules: [{ id: "md1", name: "Core" }],
      },
    });
    const base = {
      groupBy: "modules",
      workflow: null,
      stateNames,
    } as const;
    expect(
      planSpreadsheetDrop({
        ...base,
        sourceGroupKey: "md1",
        targetGroupKey: "md2",
        issue: withModule,
      }),
    ).toEqual({ kind: "attach", dimension: "modules", memberId: "md2" });
    expect(
      planSpreadsheetDrop({
        ...base,
        sourceGroupKey: "md1",
        targetGroupKey: "module:ungrouped",
        issue: withModule,
      }),
    ).toEqual({ kind: "noop", reason: "ungrouped" });
    expect(
      planSpreadsheetDrop({
        ...base,
        sourceGroupKey: "md2",
        targetGroupKey: "md1",
        issue: withModule,
      }),
    ).toEqual({ kind: "noop", reason: "already-member" });
  });

  it("cycle drop moves add-then-remove; Ungrouped and already-member are no-ops", () => {
    const withCycle = mkIssue({
      id: "i1",
      state_id: "s",
      priority: 2,
      project_structure: {
        modules: [],
        cycle: { id: "cy1", name: "Sprint 1" },
      },
    });
    const base = {
      groupBy: "cycles",
      workflow: null,
      stateNames,
    } as const;
    expect(
      planSpreadsheetDrop({
        ...base,
        sourceGroupKey: "cy1",
        targetGroupKey: "cy2",
        issue: withCycle,
      }),
    ).toEqual({
      kind: "move-cycle",
      cycleId: "cy2",
      sourceCycleId: "cy1",
    });
    expect(
      planSpreadsheetDrop({
        ...base,
        sourceGroupKey: "cy1",
        targetGroupKey: "cycle:ungrouped",
        issue: withCycle,
      }),
    ).toEqual({ kind: "noop", reason: "ungrouped" });
    expect(
      planSpreadsheetDrop({
        ...base,
        sourceGroupKey: "cy2",
        targetGroupKey: "cy1",
        issue: withCycle,
      }),
    ).toEqual({ kind: "noop", reason: "already-member" });
    // Ungrouped source = add-only move (sourceCycleId null, Board
    // moveToCycle precedent).
    const ungrouped = mkIssue({
      id: "i1",
      state_id: "s",
      priority: 2,
      project_structure: { modules: [] },
    });
    expect(
      planSpreadsheetDrop({
        ...base,
        sourceGroupKey: "cycle:ungrouped",
        targetGroupKey: "cy2",
        issue: ungrouped,
      }),
    ).toEqual({
      kind: "move-cycle",
      cycleId: "cy2",
      sourceCycleId: null,
    });
  });

  it("release drop moves via single assign; Ungrouped and already-member are no-ops", () => {
    const withRelease = mkIssue({
      id: "i1",
      state_id: "s",
      priority: 2,
      project_structure: {
        modules: [],
        release: { id: "r1", name: "v1" },
      },
    });
    const base = {
      groupBy: "releases",
      workflow: null,
      stateNames,
    } as const;
    expect(
      planSpreadsheetDrop({
        ...base,
        sourceGroupKey: "r1",
        targetGroupKey: "r2",
        issue: withRelease,
      }),
    ).toEqual({ kind: "move-release", releaseId: "r2" });
    expect(
      planSpreadsheetDrop({
        ...base,
        sourceGroupKey: "r1",
        targetGroupKey: "release:ungrouped",
        issue: withRelease,
      }),
    ).toEqual({ kind: "noop", reason: "ungrouped" });
    expect(
      planSpreadsheetDrop({
        ...base,
        sourceGroupKey: "r2",
        targetGroupKey: "r1",
        issue: withRelease,
      }),
    ).toEqual({ kind: "noop", reason: "already-member" });
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

  it("same-group header drop moves the row to the end (C152T2: the C148 no-op is retired)", async () => {
    await renderGrouped("state");
    const patch = await patchMock();
    drop("spreadsheet-drag:s:i1", "spreadsheet-group:s");
    // Backlog is i1 (0), i3 (0): move-to-end PATCHes prev + 1024.
    await waitFor(() => {
      expect(patch).toHaveBeenCalledWith(
        expect.stringContaining("/issues/i1"),
        { sort_order: 1024 },
      );
    });
    await waitFor(() => {
      expect(rowLabels()).toEqual([
        "Open issue ENG-3: Gamma",
        "Open issue ENG-1: Alpha",
        "Open issue ENG-2: Beta",
        "Open issue ENG-4: Delta",
      ]);
    });
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

  it("drop over nothing is a no-op (C155T1: headers are droppable now, but no droppable is under the pointer)", async () => {
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
    // C155T1: sub-group headers ARE droppable now, so a real
    // dnd-kit drop over one carries the header id (see the C155T1
    // describe); over === null here means the pointer is above no
    // droppable at all, and the handler still returns.
    drop("spreadsheet-drag:priority:2:i1", null);
    expect(patch).not.toHaveBeenCalled();
  });
});

describe("Spreadsheet drag membership moves (T2)", () => {
  /** i1 in every source group, i2 in every target group. */
  function membershipIssues(): Issue[] {
    return [
      mkIssue({
        id: "i1",
        sequence_id: 1,
        display_id: "ENG-1",
        name: "Alpha",
        state_id: "s",
        epics: [{ id: "ep1", name: "Epic One", archived_at: null }],
        assignees: [{ id: "u2", name: "Ada" }],
        labels: [{ id: "lb1", name: "Red", color: "#f00" }],
        project_structure: {
          modules: [{ id: "md1", name: "Core" }],
          cycle: { id: "cy1", name: "Sprint 1" },
          release: { id: "r1", name: "v1" },
        },
      }),
      mkIssue({
        id: "i2",
        sequence_id: 2,
        display_id: "ENG-2",
        name: "Beta",
        state_id: "s2",
        epics: [{ id: "ep2", name: "Epic Two", archived_at: null }],
        assignees: [{ id: "u3", name: "Bob" }],
        labels: [{ id: "lb2", name: "Blue", color: "#00f" }],
        project_structure: {
          modules: [{ id: "md2", name: "UI" }],
          cycle: { id: "cy2", name: "Sprint 2" },
          release: { id: "r2", name: "v2" },
        },
      }),
    ];
  }

  const TARGET_NAMES: Record<string, string> = {
    ep2: "Epic Two",
    u3: "Bob",
    lb2: "Blue",
    lb3: "Green",
    md2: "UI",
    cy2: "Sprint 2",
    r2: "v2",
  };
  const TARGET_COLORS: Record<string, string> = {
    lb2: "#00f",
    lb3: "#0f0",
  };

  /** Server-side fixture update for membership endpoints, so the
   *  post-move refetch re-groups the row (the PATCH pattern above).
   *  Rows are REPLACED, never mutated in place: React Query's
   *  structural sharing treats a refetch carrying the same object
   *  references as unchanged and skips the re-render, so an in-place
   *  mutation would hide the re-group in PQL mode (where no
   *  optimistic visual-cache patch is displayed). Replacement mirrors
   *  production, where every fetch returns fresh JSON. */
  function replaceIssue(id: string, f: (issue: Issue) => Issue) {
    const idx = mockData.issues.findIndex((i) => i.id === id);
    if (idx !== -1) mockData.issues[idx] = f(mockData.issues[idx]);
  }

  async function installMembershipServer() {
    const post = await postMock();
    post.mockImplementation((path: string) => {
      const epic = /\/epics\/([^/]+)\/issues/.exec(path);
      if (epic) {
        replaceIssue("i1", (t) => ({
          ...t,
          epics: [
            ...(t.epics ?? []),
            {
              id: epic[1],
              name: TARGET_NAMES[epic[1]] ?? epic[1],
              archived_at: null,
            },
          ],
        }));
        return Promise.resolve({});
      }
      const assignee = /\/issues\/([^/]+)\/assignees\/([^/]+)/.exec(path);
      if (assignee) {
        replaceIssue(assignee[1], (t) => ({
          ...t,
          assignees: [
            ...t.assignees,
            {
              id: assignee[2],
              name: TARGET_NAMES[assignee[2]] ?? assignee[2],
            },
          ],
        }));
        return Promise.resolve({});
      }
      const label = /\/issues\/([^/]+)\/labels\/([^/]+)/.exec(path);
      if (label) {
        replaceIssue(label[1], (t) => ({
          ...t,
          labels: [
            ...t.labels,
            {
              id: label[2],
              name: TARGET_NAMES[label[2]] ?? label[2],
              color: TARGET_COLORS[label[2]] ?? "",
            },
          ],
        }));
        return Promise.resolve({});
      }
      const module = /\/modules\/([^/]+)\/issues/.exec(path);
      if (module) {
        replaceIssue("i1", (t) => {
          const ps = t.project_structure ?? { modules: [] };
          return {
            ...t,
            project_structure: {
              ...ps,
              modules: [
                ...ps.modules,
                { id: module[1], name: TARGET_NAMES[module[1]] ?? module[1] },
              ],
            },
          };
        });
        return Promise.resolve({});
      }
      const cycle = /\/cycles\/([^/]+)\/issues/.exec(path);
      if (cycle) {
        replaceIssue("i1", (t) => {
          const ps = t.project_structure ?? { modules: [] };
          return {
            ...t,
            project_structure: {
              ...ps,
              cycle: {
                id: cycle[1],
                name: TARGET_NAMES[cycle[1]] ?? cycle[1],
              },
            },
          };
        });
        return Promise.resolve({});
      }
      const release = /\/releases\/([^/]+)\/issues/.exec(path);
      if (release) {
        replaceIssue("i1", (t) => {
          const ps = t.project_structure ?? { modules: [] };
          return {
            ...t,
            project_structure: {
              ...ps,
              release: {
                id: release[1],
                name: TARGET_NAMES[release[1]] ?? release[1],
              },
            },
          };
        });
        return Promise.resolve({});
      }
      return Promise.resolve({});
    });
    const del = await delMock();
    del.mockImplementation((path: string) => {
      const cycle = /\/cycles\/([^/]+)\/issues/.exec(path);
      if (cycle) {
        replaceIssue("i1", (t) =>
          t.project_structure?.cycle?.id === cycle[1]
            ? {
                ...t,
                project_structure: { ...t.project_structure, cycle: null },
              }
            : t,
        );
      }
      return Promise.resolve({});
    });
  }

  it("epic drop attaches via Board's epic endpoint and re-groups", async () => {
    mockData.issues = membershipIssues();
    await installMembershipServer();
    await renderGrouped("epic");
    expect(groupHeaders()).toEqual(["Epic One (1)", "Epic Two (1)"]);
    const post = await postMock();
    drop("spreadsheet-drag:ep1:i1", "spreadsheet-group:ep2");
    await waitFor(() => {
      expect(post).toHaveBeenCalledWith(
        expect.stringContaining("/epics/ep2/issues"),
        { issue_ids: ["i1"] },
      );
    });
    await waitFor(() => {
      expect(groupHeaders()).toEqual(["Epic One (1)", "Epic Two (2)"]);
    });
  });

  it("assignee drop attaches via Board's assignee endpoint and re-groups", async () => {
    mockData.issues = membershipIssues();
    await installMembershipServer();
    await renderGrouped("assignees");
    expect(groupHeaders()).toEqual(["Ada (1)", "Bob (1)"]);
    const post = await postMock();
    drop("spreadsheet-drag:u2:i1", "spreadsheet-group:u3");
    await waitFor(() => {
      expect(
        post.mock.calls.some(
          ([p]) =>
            typeof p === "string" && p.includes("/issues/i1/assignees/u3"),
        ),
      ).toBe(true);
    });
    await waitFor(() => {
      expect(groupHeaders()).toEqual(["Ada (1)", "Bob (2)"]);
    });
  });

  it("label drop attaches via Board's label endpoint and re-groups", async () => {
    mockData.issues = membershipIssues();
    await installMembershipServer();
    await renderGrouped("labels");
    expect(groupHeaders()).toEqual(["Red (1)", "Blue (1)"]);
    const post = await postMock();
    drop("spreadsheet-drag:lb1:i1", "spreadsheet-group:lb2");
    await waitFor(() => {
      expect(
        post.mock.calls.some(
          ([p]) =>
            typeof p === "string" && p.includes("/issues/i1/labels/lb2"),
        ),
      ).toBe(true);
    });
    await waitFor(() => {
      expect(groupHeaders()).toEqual(["Red (1)", "Blue (2)"]);
    });
  });

  it("module drop attaches via Board's module endpoint and re-groups", async () => {
    mockData.issues = membershipIssues();
    await installMembershipServer();
    await renderGrouped("modules");
    expect(groupHeaders()).toEqual(["Core (1)", "UI (1)"]);
    const post = await postMock();
    drop("spreadsheet-drag:md1:i1", "spreadsheet-group:md2");
    await waitFor(() => {
      expect(post).toHaveBeenCalledWith(
        expect.stringContaining("/modules/md2/issues"),
        { issue_ids: ["i1"] },
      );
    });
    await waitFor(() => {
      expect(groupHeaders()).toEqual(["Core (1)", "UI (2)"]);
    });
  });

  it("cycle drop moves add-then-remove (Board pipeline) and re-groups", async () => {
    mockData.issues = membershipIssues();
    await installMembershipServer();
    await renderGrouped("cycles");
    expect(groupHeaders()).toEqual(["Sprint 1 (1)", "Sprint 2 (1)"]);
    const post = await postMock();
    const del = await delMock();
    drop("spreadsheet-drag:cy1:i1", "spreadsheet-group:cy2");
    await waitFor(() => {
      expect(post).toHaveBeenCalledWith(
        expect.stringContaining("/cycles/cy2/issues"),
        { issue_ids: ["i1"] },
      );
    });
    await waitFor(() => {
      expect(del).toHaveBeenCalledWith(
        expect.stringContaining("/cycles/cy1/issues"),
        { issue_ids: ["i1"] },
      );
    });
    // The emptied source group hides; the issue lands in Sprint 2.
    await waitFor(() => {
      expect(groupHeaders()).toEqual(["Sprint 2 (2)"]);
    });
  });

  it("release drop moves via a single assign, never a DELETE", async () => {
    mockData.issues = membershipIssues();
    await installMembershipServer();
    await renderGrouped("releases");
    expect(groupHeaders()).toEqual(["v1 (1)", "v2 (1)"]);
    const post = await postMock();
    const del = await delMock();
    drop("spreadsheet-drag:r1:i1", "spreadsheet-group:r2");
    await waitFor(() => {
      expect(post).toHaveBeenCalledWith(
        expect.stringContaining("/releases/r2/issues"),
        { issue_ids: ["i1"] },
      );
    });
    await waitFor(() => {
      expect(groupHeaders()).toEqual(["v2 (2)"]);
    });
    expect(del).not.toHaveBeenCalled();
  });

  it("epic Ungrouped drop is a deliberate no-op (Board planEpicDrop)", async () => {
    mockData.issues = [
      membershipIssues()[0],
      mkIssue({
        id: "i2",
        sequence_id: 2,
        display_id: "ENG-2",
        name: "Beta",
        state_id: "s2",
      }),
    ];
    await renderGrouped("epic");
    expect(groupHeaders()).toEqual(["Epic One (1)", "Ungrouped (1)"]);
    const post = await postMock();
    drop("spreadsheet-drag:ep1:i1", "spreadsheet-group:epic:ungrouped");
    expect(post).not.toHaveBeenCalled();
    expect(toast.add).not.toHaveBeenCalled();
    expect(groupHeaders()).toEqual(["Epic One (1)", "Ungrouped (1)"]);
  });

  it("already-member drop issues no membership call, in any dimension", async () => {
    // i1 carries lb1 AND lb2: dragging its Red appearance onto the
    // Blue group is an already-labeled drop, not a move.
    mockData.issues = [
      mkIssue({
        id: "i1",
        sequence_id: 1,
        display_id: "ENG-1",
        name: "Alpha",
        state_id: "s",
        labels: [
          { id: "lb1", name: "Red", color: "#f00" },
          { id: "lb2", name: "Blue", color: "#00f" },
        ],
      }),
      mkIssue({
        id: "i2",
        sequence_id: 2,
        display_id: "ENG-2",
        name: "Beta",
        state_id: "s2",
        labels: [{ id: "lb3", name: "Green", color: "#0f0" }],
      }),
    ];
    await renderGrouped("labels");
    expect(groupHeaders()).toEqual(["Red (1)", "Blue (1)", "Green (1)"]);
    const post = await postMock();
    drop("spreadsheet-drag:lb1:i1", "spreadsheet-group:lb2");
    expect(post).not.toHaveBeenCalled();
    expect(groupHeaders()).toEqual(["Red (1)", "Blue (1)", "Green (1)"]);
  });

  it("multi-appearance drag moves the issue once: single POST, all appearances re-render", async () => {
    mockData.issues = [
      mkIssue({
        id: "i1",
        sequence_id: 1,
        display_id: "ENG-1",
        name: "Alpha",
        state_id: "s",
        labels: [
          { id: "lb1", name: "Red", color: "#f00" },
          { id: "lb2", name: "Blue", color: "#00f" },
        ],
      }),
      mkIssue({
        id: "i2",
        sequence_id: 2,
        display_id: "ENG-2",
        name: "Beta",
        state_id: "s2",
        labels: [{ id: "lb3", name: "Green", color: "#0f0" }],
      }),
    ];
    await installMembershipServer();
    await renderGrouped("labels");
    expect(groupHeaders()).toEqual(["Red (1)", "Blue (1)", "Green (1)"]);
    const post = await postMock();
    // Drag ONE appearance (the Red one) onto Green.
    drop("spreadsheet-drag:lb1:i1", "spreadsheet-group:lb3");
    await waitFor(() => {
      expect(
        post.mock.calls.some(
          ([p]) =>
            typeof p === "string" && p.includes("/issues/i1/labels/lb3"),
        ),
      ).toBe(true);
    });
    expect(post).toHaveBeenCalledTimes(1);
    await waitFor(() => {
      expect(groupHeaders()).toEqual(["Red (1)", "Blue (1)", "Green (2)"]);
    });
    // The issue now renders in all three groups.
    const alphaRows = rowLabels().filter(
      (l) => l === "Open issue ENG-1: Alpha",
    );
    expect(alphaRows).toHaveLength(3);
  });

  it("dragged appearance dims in place; sibling appearances do not", async () => {
    mockData.issues = [
      mkIssue({
        id: "i1",
        sequence_id: 1,
        display_id: "ENG-1",
        name: "Alpha",
        state_id: "s",
        labels: [
          { id: "lb1", name: "Red", color: "#f00" },
          { id: "lb2", name: "Blue", color: "#00f" },
        ],
      }),
      mkIssue({
        id: "i2",
        sequence_id: 2,
        display_id: "ENG-2",
        name: "Beta",
        state_id: "s2",
        labels: [{ id: "lb3", name: "Green", color: "#0f0" }],
      }),
    ];
    await renderGrouped("labels");
    const table = screen.getByRole("table");
    const alphaRows = within(table).getAllByRole("link", {
      name: "Open issue ENG-1: Alpha",
    });
    expect(alphaRows).toHaveLength(2);
    expect(alphaRows[0].className).not.toContain("opacity-40");
    dragStart("spreadsheet-drag:lb1:i1");
    // Only the dragged (Red) appearance dims.
    expect(alphaRows[0].className).toContain("opacity-40");
    expect(alphaRows[1].className).not.toContain("opacity-40");
    // Ending the drag clears the dim.
    drop("spreadsheet-drag:lb1:i1", null);
    expect(alphaRows[0].className).not.toContain("opacity-40");
  });

  it("drop over nothing under a membership dimension issues no membership call", async () => {
    mockData.issues = membershipIssues();
    await renderGrouped("labels", "state");
    const post = await postMock();
    const table = screen.getByRole("table");
    const subHeaders = table.querySelectorAll(
      "tr[data-spreadsheet-sub-group-header]",
    );
    expect(subHeaders.length).toBeGreaterThan(0);
    for (const h of Array.from(subHeaders)) {
      expect(h.querySelector("button")).toBeNull();
    }
    // C155T1: sub-group headers ARE droppable now, so a real
    // dnd-kit drop over one carries the header id (see the C155T1
    // describe); over === null here means the pointer is above no
    // droppable at all.
    drop("spreadsheet-drag:lb1:i1", null);
    expect(post).not.toHaveBeenCalled();
  });

  it("PQL-mode membership move re-groups the row and invalidates the PQL query", async () => {
    mockData.issues = membershipIssues();
    await installMembershipServer();
    setGrouping({ groupBy: "labels", subGroupBy: "none" });
    renderSpreadsheet("/w/acme/p/ENG/spreadsheet?pql=priority+%3E%3D+0");
    await screen.findByRole("button", { name: "Sort by Title" });
    await waitFor(() => expect(handles().length).toBeGreaterThan(0));
    const { api } = await import("../lib/api");
    const get = api.get as ReturnType<typeof vi.fn>;
    const pqlFetches = () =>
      get.mock.calls.filter(
        ([p]) => typeof p === "string" && p.includes("pql="),
      ).length;
    expect(pqlFetches()).toBeGreaterThanOrEqual(1);
    const before = pqlFetches();
    const post = await postMock();
    drop("spreadsheet-drag:lb1:i1", "spreadsheet-group:lb2");
    await waitFor(() => {
      expect(
        post.mock.calls.some(
          ([p]) =>
            typeof p === "string" && p.includes("/issues/i1/labels/lb2"),
        ),
      ).toBe(true);
    });
    // Settle-time invalidation re-runs the committed PQL query, and
    // the refetched rows re-group the moved issue.
    await waitFor(() => {
      expect(pqlFetches()).toBeGreaterThan(before);
    });
    await waitFor(() => {
      expect(groupHeaders()).toEqual(["Red (1)", "Blue (2)"]);
    });
  });

  it("failed membership POST rolls the row back to its source group", async () => {
    mockData.issues = membershipIssues();
    await renderGrouped("labels");
    expect(groupHeaders()).toEqual(["Red (1)", "Blue (1)"]);
    const post = await postMock();
    const { ApiError } = await import("../lib/api");
    post.mockRejectedValueOnce(new ApiError(500, "boom"));
    drop("spreadsheet-drag:lb1:i1", "spreadsheet-group:lb2");
    await waitFor(() => {
      expect(post).toHaveBeenCalled();
    });
    await waitFor(() => {
      expect(groupHeaders()).toEqual(["Red (1)", "Blue (1)"]);
    });
    expect(await screen.findByText("boom")).toBeInTheDocument();
  });

  it("failed cycle add rolls back and issues no remove", async () => {
    mockData.issues = membershipIssues();
    await renderGrouped("cycles");
    expect(groupHeaders()).toEqual(["Sprint 1 (1)", "Sprint 2 (1)"]);
    const post = await postMock();
    const del = await delMock();
    const { ApiError } = await import("../lib/api");
    post.mockRejectedValueOnce(new ApiError(500, "cycle boom"));
    drop("spreadsheet-drag:cy1:i1", "spreadsheet-group:cy2");
    await waitFor(() => {
      expect(post).toHaveBeenCalled();
    });
    await waitFor(() => {
      expect(groupHeaders()).toEqual(["Sprint 1 (1)", "Sprint 2 (1)"]);
    });
    expect(del).not.toHaveBeenCalled();
    expect(await screen.findByText("cycle boom")).toBeInTheDocument();
  });
});

describe("planSpreadsheetReorder", () => {
  const rows = [
    { id: "a1", state_id: "s", sort_order: 1024 },
    { id: "a2", state_id: "s", sort_order: 2048 },
    { id: "a3", state_id: "s", sort_order: 3072 },
  ];

  it("drag down onto a lower row takes the midpoint after removing the source", () => {
    // a1 onto a2: without=[a2,a3], insert at 1 → [a2,a1,a3].
    expect(
      planSpreadsheetReorder({
        rows,
        sourceId: "a1",
        targetRowId: "a2",
        stateId: "s",
      }),
    ).toEqual({ kind: "reorder", sort_order: 2560 });
  });

  it("drag up onto a higher row takes the midpoint before it", () => {
    // a3 onto a2: without=[a1,a2], insert at 1 → [a1,a3,a2].
    expect(
      planSpreadsheetReorder({
        rows,
        sourceId: "a3",
        targetRowId: "a2",
        stateId: "s",
      }),
    ).toEqual({ kind: "reorder", sort_order: 1536 });
  });

  it("drag onto the last row takes prev + step", () => {
    // a1 onto a3: without=[a2,a3], insert at 2 → [a2,a3,a1].
    expect(
      planSpreadsheetReorder({
        rows,
        sourceId: "a1",
        targetRowId: "a3",
        stateId: "s",
      }),
    ).toEqual({ kind: "reorder", sort_order: 4096 });
  });

  it("drag up onto the first row takes next - step", () => {
    // a3 onto a1: without=[a1,a2], insert at 0 → [a3,a1,a2].
    expect(
      planSpreadsheetReorder({
        rows,
        sourceId: "a3",
        targetRowId: "a1",
        stateId: "s",
      }),
    ).toEqual({ kind: "reorder", sort_order: 0 });
  });

  it("collapsed gap returns a null midpoint for the rebalance path", () => {
    const tight = [
      { id: "a1", state_id: "s", sort_order: 1024 },
      { id: "a2", state_id: "s", sort_order: 1024 + 5e-10 },
      { id: "a3", state_id: "s", sort_order: 2048 },
    ];
    // a3 onto a2: without=[a1,a2], insert at 1 → [a1,a3,a2];
    // the a1/a2 gap (5e-10) is below REBALANCE_GAP.
    expect(
      planSpreadsheetReorder({
        rows: tight,
        sourceId: "a3",
        targetRowId: "a2",
        stateId: "s",
      }),
    ).toEqual({ kind: "reorder", sort_order: null });
  });

  it("source-row drop is a same-position no-op", () => {
    expect(
      planSpreadsheetReorder({
        rows,
        sourceId: "a2",
        targetRowId: "a2",
        stateId: "s",
      }),
    ).toEqual({ kind: "noop", reason: "same-position" });
  });

  it("rows missing from the group are an unknown-target no-op", () => {
    expect(
      planSpreadsheetReorder({
        rows,
        sourceId: "a1",
        targetRowId: "zz",
        stateId: "s",
      }),
    ).toEqual({ kind: "noop", reason: "unknown-target" });
    expect(
      planSpreadsheetReorder({
        rows,
        sourceId: "zz",
        targetRowId: "a2",
        stateId: "s",
      }),
    ).toEqual({ kind: "noop", reason: "unknown-target" });
  });
});

describe("spreadsheetReorderRun", () => {
  const a1 = { id: "a1" };
  const a2 = { id: "a2" };
  const a3 = { id: "a3" };
  const sections = [
    { key: "p0", issues: [a1, a2] },
    { key: "p2", issues: [a3] },
  ];

  it("single-appearance same-section rows resolve to that section's rows", () => {
    expect(spreadsheetReorderRun(sections, "a1", "a2")).toEqual([a1, a2]);
  });

  it("cross-section rows resolve to null", () => {
    expect(spreadsheetReorderRun(sections, "a1", "a3")).toBeNull();
  });

  it("multi-appearance source resolves to null", () => {
    const dup = [
      { key: "u2", issues: [a1, a2] },
      { key: "u3", issues: [a1] },
    ];
    expect(spreadsheetReorderRun(dup, "a1", "a2")).toBeNull();
  });

  it("multi-appearance target resolves to null", () => {
    const dup = [
      { key: "u2", issues: [a1, a2] },
      { key: "u3", issues: [a2] },
    ];
    expect(spreadsheetReorderRun(dup, "a1", "a2")).toBeNull();
  });

  it("missing source or target row resolves to null", () => {
    expect(spreadsheetReorderRun(sections, "zz", "a2")).toBeNull();
    expect(spreadsheetReorderRun(sections, "a1", "zz")).toBeNull();
    expect(spreadsheetReorderRun([], "a1", "a2")).toBeNull();
  });
});

describe("planSpreadsheetSubSectionDrop", () => {
  const stateNames = new Map([
    ["s", "Backlog"],
    ["s2", "In Progress"],
  ]);

  it("unique target section delegates to planSpreadsheetDrop with the section keys", () => {
    const src = mkIssue({ id: "src", state_id: "s", priority: 0 });
    const tgt = mkIssue({ id: "tgt", state_id: "s", priority: 2 });
    const sections = [
      { key: "priority:0", title: "None", issues: [src] },
      { key: "priority:2", title: "Medium", issues: [tgt] },
    ];
    expect(
      planSpreadsheetSubSectionDrop({
        subGroupBy: "priority",
        sections,
        sourceId: "src",
        targetRowId: "tgt",
        issue: src,
        workflow: null,
        stateNames,
      }),
    ).toEqual({
      plan: { kind: "patch", body: { priority: 2 } },
      targetSection: { key: "priority:2", title: "Medium", issues: [tgt] },
    });
  });

  it("multi-appearance target resolves to null, never to a guessed section", () => {
    const src = mkIssue({ id: "src", state_id: "s" });
    const tgt = mkIssue({ id: "tgt", state_id: "s" });
    const sections = [
      { key: "lb1", title: "Red", issues: [src, tgt] },
      { key: "lb2", title: "Blue", issues: [tgt] },
    ];
    expect(
      planSpreadsheetSubSectionDrop({
        subGroupBy: "labels",
        sections,
        sourceId: "src",
        targetRowId: "tgt",
        issue: src,
        workflow: null,
        stateNames,
      }),
    ).toBeNull();
  });

  it("source already in the target section resolves to null", () => {
    const src = mkIssue({ id: "src", state_id: "s", priority: 0 });
    const other = mkIssue({ id: "other", state_id: "s", priority: 0 });
    const sections = [
      { key: "priority:0", title: "None", issues: [src, other] },
      { key: "priority:2", title: "Medium", issues: [] },
    ];
    expect(
      planSpreadsheetSubSectionDrop({
        subGroupBy: "priority",
        sections,
        sourceId: "src",
        targetRowId: "other",
        issue: src,
        workflow: null,
        stateNames,
      }),
    ).toBeNull();
  });

  it("missing target row resolves to null", () => {
    const src = mkIssue({ id: "src", state_id: "s", priority: 0 });
    const sections = [
      { key: "priority:0", title: "None", issues: [src] },
    ];
    expect(
      planSpreadsheetSubSectionDrop({
        subGroupBy: "priority",
        sections,
        sourceId: "src",
        targetRowId: "zz",
        issue: src,
        workflow: null,
        stateNames,
      }),
    ).toBeNull();
  });

  it("ungrouped target section delegates to the plan's Ungrouped no-op", () => {
    const src = mkIssue({
      id: "src",
      state_id: "s",
      project_structure: {
        modules: [],
        cycle: { id: "cy1", name: "Sprint 1" },
      },
    });
    const tgt = mkIssue({
      id: "tgt",
      state_id: "s",
      project_structure: { modules: [] },
    });
    const sections = [
      { key: "cy1", title: "Sprint 1", issues: [src] },
      { key: "cycle:ungrouped", title: "Ungrouped", issues: [tgt] },
    ];
    expect(
      planSpreadsheetSubSectionDrop({
        subGroupBy: "cycles",
        sections,
        sourceId: "src",
        targetRowId: "tgt",
        issue: src,
        workflow: null,
        stateNames,
      }),
    ).toEqual({
      plan: { kind: "noop", reason: "ungrouped" },
      targetSection: {
        key: "cycle:ungrouped",
        title: "Ungrouped",
        issues: [tgt],
      },
    });
  });

  it("multi-appearance source with a unique target delegates via the sentinel source key", () => {
    const src = mkIssue({
      id: "src",
      state_id: "s",
      labels: [
        { id: "lb1", name: "Red", color: "#f00" },
        { id: "lb3", name: "Green", color: "#0f0" },
      ],
    });
    const tgt = mkIssue({ id: "tgt", state_id: "s" });
    const sections = [
      { key: "lb1", title: "Red", issues: [src] },
      { key: "lb3", title: "Green", issues: [src] },
      { key: "lb2", title: "Blue", issues: [tgt] },
    ];
    expect(
      planSpreadsheetSubSectionDrop({
        subGroupBy: "labels",
        sections,
        sourceId: "src",
        targetRowId: "tgt",
        issue: src,
        workflow: null,
        stateNames,
      }),
    ).toEqual({
      plan: { kind: "attach", dimension: "labels", memberId: "lb2" },
      targetSection: { key: "lb2", title: "Blue", issues: [tgt] },
    });
  });
});

describe("planSpreadsheetSubSectionHeaderDrop", () => {
  const stateNames = new Map([
    ["s", "Backlog"],
    ["s2", "In Progress"],
    ["s3", "Done"],
  ]);

  it("header drop PATCHes the target priority", () => {
    const src = mkIssue({ id: "src", state_id: "s", priority: 0 });
    const tgt = mkIssue({ id: "tgt", state_id: "s", priority: 2 });
    const sections = [
      { key: "priority:0", title: "None", issues: [src] },
      { key: "priority:2", title: "Medium", issues: [tgt] },
    ];
    expect(
      planSpreadsheetSubSectionHeaderDrop({
        subGroupBy: "priority",
        sections,
        sourceId: "src",
        targetSectionKey: "priority:2",
        issue: src,
        workflow: null,
        stateNames,
      }),
    ).toEqual({
      plan: { kind: "patch", body: { priority: 2 } },
      targetSection: { key: "priority:2", title: "Medium", issues: [tgt] },
    });
  });

  it("header drop PATCHes the target state", () => {
    const src = mkIssue({ id: "src", state_id: "s", priority: 0 });
    const tgt = mkIssue({ id: "tgt", state_id: "s2", priority: 0 });
    const sections = [
      { key: "s", title: "Backlog", issues: [src] },
      { key: "s2", title: "In Progress", issues: [tgt] },
    ];
    expect(
      planSpreadsheetSubSectionHeaderDrop({
        subGroupBy: "state",
        sections,
        sourceId: "src",
        targetSectionKey: "s2",
        issue: src,
        workflow: null,
        stateNames,
      }),
    ).toEqual({
      plan: { kind: "patch", body: { state_id: "s2" } },
      targetSection: { key: "s2", title: "In Progress", issues: [tgt] },
    });
  });

  it("header drop attaches epic / assignee / label / module", () => {
    const src = mkIssue({
      id: "src",
      state_id: "s",
      labels: [{ id: "lb1", name: "Red", color: "#f00" }],
      project_structure: {
        modules: [],
        cycle: { id: "cy1", name: "Sprint 1" },
        release: { id: "r1", name: "v1" },
      },
    });
    const tgt = mkIssue({ id: "tgt", state_id: "s" });
    const labelSections = [
      { key: "lb1", title: "Red", issues: [src] },
      { key: "lb2", title: "Blue", issues: [tgt] },
    ];
    expect(
      planSpreadsheetSubSectionHeaderDrop({
        subGroupBy: "labels",
        sections: labelSections,
        sourceId: "src",
        targetSectionKey: "lb2",
        issue: src,
        workflow: null,
        stateNames,
      })?.plan,
    ).toEqual({ kind: "attach", dimension: "labels", memberId: "lb2" });
    expect(
      planSpreadsheetSubSectionHeaderDrop({
        subGroupBy: "epic",
        sections: [
          { key: "ep1", title: "Epic One", issues: [src] },
          { key: "ep2", title: "Epic Two", issues: [tgt] },
        ],
        sourceId: "src",
        targetSectionKey: "ep2",
        issue: src,
        workflow: null,
        stateNames,
      })?.plan,
    ).toEqual({ kind: "attach", dimension: "epic", memberId: "ep2" });
    expect(
      planSpreadsheetSubSectionHeaderDrop({
        subGroupBy: "assignees",
        sections: [
          { key: "u2", title: "Ada", issues: [src] },
          { key: "u3", title: "Bob", issues: [tgt] },
        ],
        sourceId: "src",
        targetSectionKey: "u3",
        issue: src,
        workflow: null,
        stateNames,
      })?.plan,
    ).toEqual({ kind: "attach", dimension: "assignees", memberId: "u3" });
    expect(
      planSpreadsheetSubSectionHeaderDrop({
        subGroupBy: "modules",
        sections: [
          { key: "m1", title: "Core", issues: [src] },
          { key: "m2", title: "Shell", issues: [tgt] },
        ],
        sourceId: "src",
        targetSectionKey: "m2",
        issue: src,
        workflow: null,
        stateNames,
      })?.plan,
    ).toEqual({ kind: "attach", dimension: "modules", memberId: "m2" });
  });

  it("header drop moves cycle add-then-remove and release via single assign", () => {
    const src = mkIssue({
      id: "src",
      state_id: "s",
      project_structure: {
        modules: [],
        cycle: { id: "cy1", name: "Sprint 1" },
        release: { id: "r1", name: "v1" },
      },
    });
    const tgt = mkIssue({ id: "tgt", state_id: "s" });
    expect(
      planSpreadsheetSubSectionHeaderDrop({
        subGroupBy: "cycles",
        sections: [
          { key: "cy1", title: "Sprint 1", issues: [src] },
          { key: "cy2", title: "Sprint 2", issues: [tgt] },
        ],
        sourceId: "src",
        targetSectionKey: "cy2",
        issue: src,
        workflow: null,
        stateNames,
      })?.plan,
    ).toEqual({
      kind: "move-cycle",
      cycleId: "cy2",
      sourceCycleId: "cy1",
    });
    expect(
      planSpreadsheetSubSectionHeaderDrop({
        subGroupBy: "releases",
        sections: [
          { key: "r1", title: "v1", issues: [src] },
          { key: "r2", title: "v2", issues: [tgt] },
        ],
        sourceId: "src",
        targetSectionKey: "r2",
        issue: src,
        workflow: null,
        stateNames,
      })?.plan,
    ).toEqual({ kind: "move-release", releaseId: "r2" });
  });

  it("Ungrouped target section delegates to the plan's Ungrouped no-op", () => {
    const src = mkIssue({
      id: "src",
      state_id: "s",
      project_structure: {
        modules: [],
        cycle: { id: "cy1", name: "Sprint 1" },
      },
    });
    const tgt = mkIssue({
      id: "tgt",
      state_id: "s",
      project_structure: { modules: [] },
    });
    const sections = [
      { key: "cy1", title: "Sprint 1", issues: [src] },
      { key: "cycle:ungrouped", title: "Ungrouped", issues: [tgt] },
    ];
    expect(
      planSpreadsheetSubSectionHeaderDrop({
        subGroupBy: "cycles",
        sections,
        sourceId: "src",
        targetSectionKey: "cycle:ungrouped",
        issue: src,
        workflow: null,
        stateNames,
      }),
    ).toEqual({
      plan: { kind: "noop", reason: "ungrouped" },
      targetSection: {
        key: "cycle:ungrouped",
        title: "Ungrouped",
        issues: [tgt],
      },
    });
  });

  it("already-member header drop is a no-op", () => {
    const src = mkIssue({
      id: "src",
      state_id: "s",
      labels: [
        { id: "lb1", name: "Red", color: "#f00" },
        { id: "lb2", name: "Blue", color: "#00f" },
      ],
    });
    const tgt = mkIssue({ id: "tgt", state_id: "s" });
    const sections = [
      { key: "lb1", title: "Red", issues: [src] },
      { key: "lb2", title: "Blue", issues: [tgt] },
    ];
    expect(
      planSpreadsheetSubSectionHeaderDrop({
        subGroupBy: "labels",
        sections,
        sourceId: "src",
        targetSectionKey: "lb2",
        issue: src,
        workflow: null,
        stateNames,
      })?.plan,
    ).toEqual({ kind: "noop", reason: "already-member" });
  });

  it("createdBy / stateGroups sub-group header drops are no-ops", () => {
    const src = mkIssue({ id: "src", state_id: "s", priority: 2 });
    const tgt = mkIssue({ id: "tgt", state_id: "s2", priority: 2 });
    const createdBySections = [
      { key: "u2", title: "Ada", issues: [src] },
      { key: "createdby:unknown", title: "Unknown", issues: [tgt] },
    ];
    expect(
      planSpreadsheetSubSectionHeaderDrop({
        subGroupBy: "createdBy",
        sections: createdBySections,
        sourceId: "src",
        targetSectionKey: "createdby:unknown",
        issue: src,
        workflow: null,
        stateNames,
      })?.plan,
    ).toEqual({ kind: "noop", reason: "created-by" });
    expect(
      planSpreadsheetSubSectionHeaderDrop({
        subGroupBy: "stateGroups",
        sections: [
          { key: "stategroup:backlog", title: "Backlog", issues: [src] },
          { key: "stategroup:started", title: "Started", issues: [tgt] },
        ],
        sourceId: "src",
        targetSectionKey: "stategroup:started",
        issue: src,
        workflow: null,
        stateNames,
      })?.plan,
    ).toEqual({ kind: "noop", reason: "state-group" });
  });

  it("blocked state transition returns a blocked plan", () => {
    const workflow: Workflow = {
      states,
      transitions: [
        { from_state_id: "s2", to_state_id: "s" },
        { from_state_id: "s", to_state_id: "s3" },
      ],
    };
    const src = mkIssue({ id: "src", state_id: "s", priority: 0 });
    const tgt = mkIssue({ id: "tgt", state_id: "s2", priority: 0 });
    const sections = [
      { key: "s", title: "Backlog", issues: [src] },
      { key: "s2", title: "In Progress", issues: [tgt] },
    ];
    expect(
      planSpreadsheetSubSectionHeaderDrop({
        subGroupBy: "state",
        sections,
        sourceId: "src",
        targetSectionKey: "s2",
        issue: src,
        workflow,
        stateNames,
      })?.plan,
    ).toEqual({ kind: "blocked", permittedNames: ["Done"] });
  });

  it("multi-appearance source delegates via the sentinel source key", () => {
    const src = mkIssue({
      id: "src",
      state_id: "s",
      labels: [
        { id: "lb1", name: "Red", color: "#f00" },
        { id: "lb3", name: "Green", color: "#0f0" },
      ],
    });
    const tgt = mkIssue({ id: "tgt", state_id: "s" });
    const sections = [
      { key: "lb1", title: "Red", issues: [src] },
      { key: "lb3", title: "Green", issues: [src] },
      { key: "lb2", title: "Blue", issues: [tgt] },
    ];
    expect(
      planSpreadsheetSubSectionHeaderDrop({
        subGroupBy: "labels",
        sections,
        sourceId: "src",
        targetSectionKey: "lb2",
        issue: src,
        workflow: null,
        stateNames,
      }),
    ).toEqual({
      plan: { kind: "attach", dimension: "labels", memberId: "lb2" },
      targetSection: { key: "lb2", title: "Blue", issues: [tgt] },
    });
  });

  it("missing section key resolves to null", () => {
    const src = mkIssue({ id: "src", state_id: "s", priority: 0 });
    const sections = [
      { key: "priority:0", title: "None", issues: [src] },
    ];
    expect(
      planSpreadsheetSubSectionHeaderDrop({
        subGroupBy: "priority",
        sections,
        sourceId: "src",
        targetSectionKey: "priority:9",
        issue: src,
        workflow: null,
        stateNames,
      }),
    ).toBeNull();
  });

  it("source already in the target section resolves to null", () => {
    const src = mkIssue({ id: "src", state_id: "s", priority: 0 });
    const other = mkIssue({ id: "other", state_id: "s", priority: 0 });
    const sections = [
      { key: "priority:0", title: "None", issues: [src, other] },
      { key: "priority:2", title: "Medium", issues: [] },
    ];
    expect(
      planSpreadsheetSubSectionHeaderDrop({
        subGroupBy: "priority",
        sections,
        sourceId: "src",
        targetSectionKey: "priority:0",
        issue: src,
        workflow: null,
        stateNames,
      }),
    ).toBeNull();
  });
});

describe("Spreadsheet state-group reorder (C152T1)", () => {
  /** Three rows in Backlog (s) at even 1024-steps, one in In Progress. */
  function reorderIssues(): Issue[] {
    return [
      mkIssue({
        id: "a1",
        sequence_id: 1,
        display_id: "ENG-1",
        name: "Alpha",
        state_id: "s",
        sort_order: 1024,
        created_by: "u2",
      }),
      mkIssue({
        id: "a2",
        sequence_id: 2,
        display_id: "ENG-2",
        name: "Beta",
        state_id: "s",
        sort_order: 2048,
        created_by: "u2",
      }),
      mkIssue({
        id: "a3",
        sequence_id: 3,
        display_id: "ENG-3",
        name: "Gamma",
        state_id: "s",
        sort_order: 3072,
        created_by: "u2",
      }),
      mkIssue({
        id: "b1",
        sequence_id: 4,
        display_id: "ENG-4",
        name: "Delta",
        state_id: "s2",
        sort_order: 1024,
        created_by: "u2",
      }),
    ];
  }

  const ALL = [
    "Open issue ENG-1: Alpha",
    "Open issue ENG-2: Beta",
    "Open issue ENG-3: Gamma",
    "Open issue ENG-4: Delta",
  ];

  it("drag onto a lower row PATCHes the midpoint and re-renders in the new order", async () => {
    mockData.issues = reorderIssues();
    await renderGrouped("state");
    expect(rowLabels()).toEqual(ALL);
    const patch = await patchMock();
    drop("spreadsheet-drag:s:a1", "spreadsheet-row-drop:s:a2");
    await waitFor(() => {
      expect(patch).toHaveBeenCalledWith(
        expect.stringContaining("/issues/a1"),
        { sort_order: 2560 },
      );
    });
    await waitFor(() => {
      expect(rowLabels()).toEqual([
        "Open issue ENG-2: Beta",
        "Open issue ENG-1: Alpha",
        "Open issue ENG-3: Gamma",
        "Open issue ENG-4: Delta",
      ]);
    });
  });

  it("drag onto a higher row PATCHes the midpoint and re-renders in the new order", async () => {
    mockData.issues = reorderIssues();
    await renderGrouped("state");
    const patch = await patchMock();
    drop("spreadsheet-drag:s:a3", "spreadsheet-row-drop:s:a2");
    await waitFor(() => {
      expect(patch).toHaveBeenCalledWith(
        expect.stringContaining("/issues/a3"),
        { sort_order: 1536 },
      );
    });
    await waitFor(() => {
      expect(rowLabels()).toEqual([
        "Open issue ENG-1: Alpha",
        "Open issue ENG-3: Gamma",
        "Open issue ENG-2: Beta",
        "Open issue ENG-4: Delta",
      ]);
    });
  });

  it("drag onto the last row PATCHes prev + step", async () => {
    mockData.issues = reorderIssues();
    await renderGrouped("state");
    const patch = await patchMock();
    drop("spreadsheet-drag:s:a1", "spreadsheet-row-drop:s:a3");
    await waitFor(() => {
      expect(patch).toHaveBeenCalledWith(
        expect.stringContaining("/issues/a1"),
        { sort_order: 4096 },
      );
    });
    await waitFor(() => {
      expect(rowLabels()).toEqual([
        "Open issue ENG-2: Beta",
        "Open issue ENG-3: Gamma",
        "Open issue ENG-1: Alpha",
        "Open issue ENG-4: Delta",
      ]);
    });
  });

  it("drag onto the source row itself PATCHes nothing", async () => {
    mockData.issues = reorderIssues();
    await renderGrouped("state");
    const patch = await patchMock();
    drop("spreadsheet-drag:s:a1", "spreadsheet-row-drop:s:a1");
    expect(patch).not.toHaveBeenCalled();
    expect(rowLabels()).toEqual(ALL);
  });

  it("drag within a priority group PATCHes nothing (C148 noop preserved)", async () => {
    await renderGrouped("priority");
    expect(groupHeaders()).toEqual([
      "Urgent (1)",
      "Medium (2)",
      "None (1)",
    ]);
    const patch = await patchMock();
    drop("spreadsheet-drag:priority:2:i1", "spreadsheet-row-drop:priority:2:i3");
    expect(patch).not.toHaveBeenCalled();
    expect(groupHeaders()).toEqual([
      "Urgent (1)",
      "Medium (2)",
      "None (1)",
    ]);
  });

  it("drag within a state group while a column sort is active PATCHes nothing", async () => {
    mockData.issues = reorderIssues();
    await renderGrouped("state");
    const sortButton = screen.getByRole("button", {
      name: "Sort by Title",
    });
    fireEvent.click(sortButton);
    await waitFor(() => {
      expect(
        screen
          .getByRole("button", { name: "Sort by Title" })
          .getAttribute("aria-sort"),
      ).toBe("ascending");
    });
    const patch = await patchMock();
    drop("spreadsheet-drag:s:a1", "spreadsheet-row-drop:s:a2");
    expect(patch).not.toHaveBeenCalled();
  });

  it("drag within a state group with a sub-group active PATCHes the run midpoint (C153T1: retires the C152 no-op)", async () => {
    mockData.issues = reorderIssues();
    await renderGrouped("state", "priority");
    const table = screen.getByRole("table");
    // One priority sub-section per primary group (all rows are
    // priority 0) — the sub-group is genuinely active, not "none",
    // and every row is single-appearance in its own section, so the
    // run is that section's rows and the drop reorders within it.
    expect(
      table.querySelectorAll("tr[data-spreadsheet-sub-group-header]"),
    ).toHaveLength(2);
    const patch = await patchMock();
    drop("spreadsheet-drag:s:a1", "spreadsheet-row-drop:s:a2");
    await waitFor(() => {
      expect(patch).toHaveBeenCalledWith(
        expect.stringContaining("/issues/a1"),
        { sort_order: 2560 },
      );
    });
    await waitFor(() => {
      expect(rowLabels()).toEqual([
        "Open issue ENG-2: Beta",
        "Open issue ENG-1: Alpha",
        "Open issue ENG-3: Gamma",
        "Open issue ENG-4: Delta",
      ]);
    });
  });

  it("PQL-mode same-group row drop PATCHes nothing", async () => {
    mockData.issues = reorderIssues();
    setGrouping({ groupBy: "state", subGroupBy: "none" });
    renderSpreadsheet("/w/acme/p/ENG/spreadsheet?pql=priority+%3E%3D+0");
    await screen.findByRole("button", { name: "Sort by Title" });
    await waitFor(() => expect(handles().length).toBeGreaterThan(0));
    const patch = await patchMock();
    drop("spreadsheet-drag:s:a1", "spreadsheet-row-drop:s:a2");
    expect(patch).not.toHaveBeenCalled();
  });

  it("collapsed-gap drop rebalances, refetches, and retries once with the post-rebalance midpoint", async () => {
    mockData.issues = [
      mkIssue({
        id: "a1",
        sequence_id: 1,
        display_id: "ENG-1",
        name: "Alpha",
        state_id: "s",
        sort_order: 1024,
        created_by: "u2",
      }),
      mkIssue({
        id: "a2",
        sequence_id: 2,
        display_id: "ENG-2",
        name: "Beta",
        state_id: "s",
        sort_order: 1024 + 5e-10,
        created_by: "u2",
      }),
      mkIssue({
        id: "a3",
        sequence_id: 3,
        display_id: "ENG-3",
        name: "Gamma",
        state_id: "s",
        sort_order: 2048,
        created_by: "u2",
      }),
    ];
    await renderGrouped("state");
    const post = await postMock();
    const patch = await patchMock();
    post.mockImplementation((path: string) => {
      if (path.includes("/issues/rebalance")) {
        // Server-side re-space: even 1024-steps in current order.
        const rows = mockData.issues
          .filter((i) => i.state_id === "s")
          .sort((a, b) => a.sort_order - b.sort_order);
        rows.forEach((r, idx) => {
          r.sort_order = (idx + 1) * 1024;
        });
        return Promise.resolve({ rebalanced: rows.length });
      }
      return Promise.resolve({});
    });
    drop("spreadsheet-drag:s:a3", "spreadsheet-row-drop:s:a2");
    await waitFor(() => {
      expect(post).toHaveBeenCalledWith(
        expect.stringContaining("/issues/rebalance"),
        { state_id: "s" },
      );
    });
    // Retry once against the re-spaced column: midpoint(1024, 2048).
    await waitFor(() => {
      expect(patch).toHaveBeenCalledWith(
        expect.stringContaining("/issues/a3"),
        { sort_order: 1536 },
      );
    });
    expect(patch).toHaveBeenCalledTimes(1);
    expect(
      post.mock.calls.filter(
        ([p]) =>
          typeof p === "string" && p.includes("/issues/rebalance"),
      ),
    ).toHaveLength(1);
    await waitFor(() => {
      expect(rowLabels()).toEqual([
        "Open issue ENG-1: Alpha",
        "Open issue ENG-3: Gamma",
        "Open issue ENG-2: Beta",
      ]);
    });
  });

  it("guest row drop is refused: no PATCH", async () => {
    mockData.role = 0;
    mockData.issues = reorderIssues();
    await renderGrouped("state", "none", false);
    const patch = await patchMock();
    drop("spreadsheet-drag:s:a1", "spreadsheet-row-drop:s:a2");
    expect(patch).not.toHaveBeenCalled();
  });
});

describe("Spreadsheet within-sub-group reorder (C153T1)", () => {
  /** Backlog (s) rows split across priority sub-sections, one in In Progress. */
  function subgroupIssues(): Issue[] {
    return [
      mkIssue({
        id: "a1",
        sequence_id: 1,
        display_id: "ENG-1",
        name: "Alpha",
        priority: 0,
        state_id: "s",
        sort_order: 1024,
        created_by: "u2",
      }),
      mkIssue({
        id: "a2",
        sequence_id: 2,
        display_id: "ENG-2",
        name: "Beta",
        priority: 2,
        state_id: "s",
        sort_order: 2048,
        created_by: "u2",
      }),
      mkIssue({
        id: "a3",
        sequence_id: 3,
        display_id: "ENG-3",
        name: "Gamma",
        priority: 0,
        state_id: "s",
        sort_order: 3072,
        created_by: "u2",
      }),
      mkIssue({
        id: "b1",
        sequence_id: 4,
        display_id: "ENG-4",
        name: "Delta",
        priority: 0,
        state_id: "s2",
        sort_order: 1024,
        created_by: "u2",
      }),
    ];
  }

  it("cross-section row drop PATCHes the target priority (C154T1: retires the C153 no-op)", async () => {
    mockData.issues = subgroupIssues();
    await renderGrouped("state", "priority");
    const table = screen.getByRole("table");
    // Backlog splits into priority 2 + priority 0 sections, In
    // Progress has one — a1 (priority 0) and a2 (priority 2) are in
    // different sections of the same primary group, so the drop is
    // now a sub-dimension MOVE, not a reorder and not a no-op.
    expect(
      table.querySelectorAll("tr[data-spreadsheet-sub-group-header]"),
    ).toHaveLength(3);
    const patch = await patchMock();
    drop("spreadsheet-drag:s:a1", "spreadsheet-row-drop:s:a2");
    await waitFor(() => {
      expect(patch).toHaveBeenCalledWith(
        expect.stringContaining("/issues/a1"),
        { priority: 2 },
      );
    });
  });

  it("multi-appearance row drop PATCHes nothing", async () => {
    mockData.issues = [
      mkIssue({
        id: "a1",
        sequence_id: 1,
        display_id: "ENG-1",
        name: "Alpha",
        state_id: "s",
        sort_order: 1024,
        created_by: "u2",
        assignees: [
          { id: "u2", name: "Ada" },
          { id: "u3", name: "Bob" },
        ],
      }),
      mkIssue({
        id: "a2",
        sequence_id: 2,
        display_id: "ENG-2",
        name: "Beta",
        state_id: "s",
        sort_order: 2048,
        created_by: "u2",
        assignees: [{ id: "u2", name: "Ada" }],
      }),
      mkIssue({
        id: "a3",
        sequence_id: 3,
        display_id: "ENG-3",
        name: "Gamma",
        state_id: "s",
        sort_order: 3072,
        created_by: "u2",
        assignees: [{ id: "u2", name: "Ada" }],
      }),
    ];
    await renderGrouped("state", "assignees");
    const table = screen.getByRole("table");
    // a1 appears under both Ada and Bob sections — the drag id
    // cannot say which appearance was grabbed, so no run resolves.
    expect(
      table.querySelectorAll("tr[data-spreadsheet-sub-group-header]"),
    ).toHaveLength(2);
    const patch = await patchMock();
    drop("spreadsheet-drag:s:a1", "spreadsheet-row-drop:s:a2");
    expect(patch).not.toHaveBeenCalled();
  });
});

describe("Spreadsheet cross-sub-section move (C154T1)", () => {
  /** Both rows in Backlog (s), split across every sub-dimension. */
  function subsectionIssues(): Issue[] {
    return [
      mkIssue({
        id: "i1",
        sequence_id: 1,
        display_id: "ENG-1",
        name: "Alpha",
        priority: 0,
        state_id: "s",
        created_by: "u2",
        labels: [{ id: "lb1", name: "Red", color: "#f00" }],
        project_structure: {
          modules: [],
          cycle: { id: "cy1", name: "Sprint 1" },
          release: { id: "r1", name: "v1" },
        },
      }),
      mkIssue({
        id: "i2",
        sequence_id: 2,
        display_id: "ENG-2",
        name: "Beta",
        priority: 2,
        state_id: "s",
        created_by: "u9",
        labels: [{ id: "lb2", name: "Blue", color: "#00f" }],
        project_structure: {
          modules: [],
          cycle: { id: "cy2", name: "Sprint 2" },
          release: { id: "r2", name: "v2" },
        },
      }),
    ];
  }

  it("cross-section cycle drop runs the move pipeline (add then remove)", async () => {
    mockData.issues = subsectionIssues();
    await renderGrouped("state", "cycles");
    const post = await postMock();
    const del = await delMock();
    drop("spreadsheet-drag:s:i1", "spreadsheet-row-drop:s:i2");
    await waitFor(() => {
      expect(post).toHaveBeenCalledWith(
        expect.stringContaining("/cycles/cy2/issues"),
        { issue_ids: ["i1"] },
      );
    });
    await waitFor(() => {
      expect(del).toHaveBeenCalledWith(
        expect.stringContaining("/cycles/cy1/issues"),
        { issue_ids: ["i1"] },
      );
    });
  });

  it("cross-section release drop assigns, never DELETEs", async () => {
    mockData.issues = subsectionIssues();
    await renderGrouped("state", "releases");
    const post = await postMock();
    const del = await delMock();
    drop("spreadsheet-drag:s:i1", "spreadsheet-row-drop:s:i2");
    await waitFor(() => {
      expect(post).toHaveBeenCalledWith(
        expect.stringContaining("/releases/r2/issues"),
        { issue_ids: ["i1"] },
      );
    });
    expect(del).not.toHaveBeenCalled();
  });

  it("cross-section label drop attaches to the target section's label", async () => {
    mockData.issues = subsectionIssues();
    await renderGrouped("state", "labels");
    const post = await postMock();
    drop("spreadsheet-drag:s:i1", "spreadsheet-row-drop:s:i2");
    await waitFor(() => {
      expect(
        post.mock.calls.some(
          ([p]) =>
            typeof p === "string" && p.includes("/issues/i1/labels/lb2"),
        ),
      ).toBe(true);
    });
  });

  it("same-section drop does NOT move (source already in the target section)", async () => {
    mockData.issues = [
      mkIssue({
        id: "i1",
        sequence_id: 1,
        display_id: "ENG-1",
        name: "Alpha",
        priority: 2,
        state_id: "s",
        project_structure: {
          modules: [],
          cycle: { id: "cy1", name: "Sprint 1" },
        },
      }),
      mkIssue({
        id: "i2",
        sequence_id: 2,
        display_id: "ENG-2",
        name: "Beta",
        priority: 2,
        state_id: "s2",
        project_structure: {
          modules: [],
          cycle: { id: "cy1", name: "Sprint 1" },
        },
      }),
    ];
    await renderGrouped("priority", "cycles");
    const patch = await patchMock();
    const post = await postMock();
    const del = await delMock();
    drop("spreadsheet-drag:priority:2:i1", "spreadsheet-row-drop:priority:2:i2");
    expect(patch).not.toHaveBeenCalled();
    expect(post).not.toHaveBeenCalled();
    expect(del).not.toHaveBeenCalled();
  });

  it("multi-appearance target drop PATCHes nothing", async () => {
    mockData.issues = [
      mkIssue({
        id: "i1",
        sequence_id: 1,
        display_id: "ENG-1",
        name: "Alpha",
        state_id: "s",
        assignees: [{ id: "u2", name: "Ada" }],
      }),
      mkIssue({
        id: "i2",
        sequence_id: 2,
        display_id: "ENG-2",
        name: "Beta",
        state_id: "s",
        assignees: [
          { id: "u2", name: "Ada" },
          { id: "u3", name: "Bob" },
        ],
      }),
    ];
    await renderGrouped("state", "assignees");
    const patch = await patchMock();
    const post = await postMock();
    const del = await delMock();
    drop("spreadsheet-drag:s:i1", "spreadsheet-row-drop:s:i2");
    expect(patch).not.toHaveBeenCalled();
    expect(post).not.toHaveBeenCalled();
    expect(del).not.toHaveBeenCalled();
  });

  it("ungrouped-section drop PATCHes nothing (detach-from-all stays out of scope)", async () => {
    mockData.issues = [
      mkIssue({
        id: "i1",
        sequence_id: 1,
        display_id: "ENG-1",
        name: "Alpha",
        state_id: "s",
        project_structure: {
          modules: [],
          cycle: { id: "cy1", name: "Sprint 1" },
        },
      }),
      mkIssue({
        id: "i2",
        sequence_id: 2,
        display_id: "ENG-2",
        name: "Beta",
        state_id: "s",
        project_structure: { modules: [] },
      }),
    ];
    await renderGrouped("state", "cycles");
    const patch = await patchMock();
    const post = await postMock();
    const del = await delMock();
    drop("spreadsheet-drag:s:i1", "spreadsheet-row-drop:s:i2");
    expect(patch).not.toHaveBeenCalled();
    expect(post).not.toHaveBeenCalled();
    expect(del).not.toHaveBeenCalled();
    expect(toast.add).not.toHaveBeenCalled();
  });

  it("createdBy sub-group drop PATCHes nothing", async () => {
    // Both rows under one priority primary group, split across
    // createdBy sub-sections (u2 vs unknown u9).
    mockData.issues = [
      mkIssue({
        id: "i1",
        sequence_id: 1,
        display_id: "ENG-1",
        name: "Alpha",
        priority: 2,
        state_id: "s",
        created_by: "u2",
      }),
      mkIssue({
        id: "i2",
        sequence_id: 2,
        display_id: "ENG-2",
        name: "Beta",
        priority: 2,
        state_id: "s2",
        created_by: "u9",
      }),
    ];
    await renderGrouped("priority", "createdBy");
    const patch = await patchMock();
    const post = await postMock();
    const del = await delMock();
    drop("spreadsheet-drag:priority:2:i1", "spreadsheet-row-drop:priority:2:i2");
    expect(patch).not.toHaveBeenCalled();
    expect(post).not.toHaveBeenCalled();
    expect(del).not.toHaveBeenCalled();
    expect(toast.add).not.toHaveBeenCalled();
  });

  it("stateGroups sub-group drop PATCHes nothing", async () => {
    mockData.issues = [
      mkIssue({
        id: "i1",
        sequence_id: 1,
        display_id: "ENG-1",
        name: "Alpha",
        priority: 2,
        state_id: "s",
      }),
      mkIssue({
        id: "i2",
        sequence_id: 2,
        display_id: "ENG-2",
        name: "Beta",
        priority: 2,
        state_id: "s2",
      }),
    ];
    await renderGrouped("priority", "stateGroups");
    const patch = await patchMock();
    const post = await postMock();
    const del = await delMock();
    drop("spreadsheet-drag:priority:2:i1", "spreadsheet-row-drop:priority:2:i2");
    expect(patch).not.toHaveBeenCalled();
    expect(post).not.toHaveBeenCalled();
    expect(del).not.toHaveBeenCalled();
    expect(toast.add).not.toHaveBeenCalled();
  });

  it("cross-primary-group drop keeps its C148 meaning with a sub-group active", async () => {
    mockData.issues = [
      ...subsectionIssues(),
      mkIssue({
        id: "i3",
        sequence_id: 3,
        display_id: "ENG-3",
        name: "Gamma",
        priority: 0,
        state_id: "s2",
      }),
    ];
    await renderGrouped("state", "priority");
    const patch = await patchMock();
    drop("spreadsheet-drag:s:i1", "spreadsheet-row-drop:s2:i3");
    await waitFor(() => {
      expect(patch).toHaveBeenCalledWith(
        expect.stringContaining("/issues/i1"),
        { state_id: "s2" },
      );
    });
  });
});

describe("Spreadsheet sub-group header drop (C155T1)", () => {
  /** Backlog (s) rows split across priority sub-sections, one in In Progress. */
  function headerDropIssues(): Issue[] {
    return [
      mkIssue({
        id: "a1",
        sequence_id: 1,
        display_id: "ENG-1",
        name: "Alpha",
        priority: 0,
        state_id: "s",
        sort_order: 1024,
        created_by: "u2",
      }),
      mkIssue({
        id: "a2",
        sequence_id: 2,
        display_id: "ENG-2",
        name: "Beta",
        priority: 2,
        state_id: "s",
        sort_order: 2048,
        created_by: "u2",
      }),
      mkIssue({
        id: "b1",
        sequence_id: 3,
        display_id: "ENG-3",
        name: "Gamma",
        priority: 0,
        state_id: "s2",
        sort_order: 1024,
        created_by: "u2",
      }),
    ];
  }

  it("header drop PATCHes the target priority and surfaces nothing extra", async () => {
    mockData.issues = headerDropIssues();
    await renderGrouped("state", "priority");
    const table = screen.getByRole("table");
    expect(
      table.querySelectorAll("tr[data-spreadsheet-sub-group-header]"),
    ).toHaveLength(3);
    const patch = await patchMock();
    drop("spreadsheet-drag:s:a1", "spreadsheet-sub-group:s|priority:2");
    await waitFor(() => {
      expect(patch).toHaveBeenCalledWith(
        expect.stringContaining("/issues/a1"),
        { priority: 2 },
      );
    });
    expect(toast.add).not.toHaveBeenCalled();
  });

  it("same-section header drop PATCHes nothing", async () => {
    mockData.issues = headerDropIssues();
    await renderGrouped("state", "priority");
    const patch = await patchMock();
    const post = await postMock();
    const del = await delMock();
    // a1 already sits in the priority:0 section of Backlog.
    drop("spreadsheet-drag:s:a1", "spreadsheet-sub-group:s|priority:0");
    expect(patch).not.toHaveBeenCalled();
    expect(post).not.toHaveBeenCalled();
    expect(del).not.toHaveBeenCalled();
    expect(toast.add).not.toHaveBeenCalled();
  });

  it("cross-group sub-header drop keeps the C148 primary-group meaning", async () => {
    mockData.issues = headerDropIssues();
    await renderGrouped("state", "priority");
    const patch = await patchMock();
    // The over id names In Progress's priority:0 header — the
    // primary-group meaning wins (state move), not a priority move.
    drop("spreadsheet-drag:s:a1", "spreadsheet-sub-group:s2|priority:0");
    await waitFor(() => {
      expect(patch).toHaveBeenCalledWith(
        expect.stringContaining("/issues/a1"),
        { state_id: "s2" },
      );
    });
    expect(patch).not.toHaveBeenCalledWith(
      expect.stringContaining("/issues/a1"),
      { priority: 0 },
    );
  });
});

describe("spreadsheetReorderIndicatorSide", () => {
  const rows = [
    { id: "a1", state_id: "s", sort_order: 1024 },
    { id: "a2", state_id: "s", sort_order: 2048 },
    { id: "a3", state_id: "s", sort_order: 3072 },
  ];

  it("source above the hovered row puts the line below it", () => {
    expect(
      spreadsheetReorderIndicatorSide({
        rows,
        sourceId: "a1",
        hoveredId: "a2",
      }),
    ).toBe("below");
  });

  it("source below the hovered row puts the line above it", () => {
    expect(
      spreadsheetReorderIndicatorSide({
        rows,
        sourceId: "a3",
        hoveredId: "a2",
      }),
    ).toBe("above");
  });

  it("hovering the source row itself shows no line (no-op drop)", () => {
    expect(
      spreadsheetReorderIndicatorSide({
        rows,
        sourceId: "a2",
        hoveredId: "a2",
      }),
    ).toBeNull();
  });

  it("rows missing from the sequence show no line", () => {
    expect(
      spreadsheetReorderIndicatorSide({
        rows,
        sourceId: "a1",
        hoveredId: "zz",
      }),
    ).toBeNull();
    expect(
      spreadsheetReorderIndicatorSide({
        rows,
        sourceId: "zz",
        hoveredId: "a2",
      }),
    ).toBeNull();
  });
});

describe("planSpreadsheetReorderToEnd", () => {
  const rows = [
    { id: "a1", state_id: "s", sort_order: 1024 },
    { id: "a2", state_id: "s", sort_order: 2048 },
    { id: "a3", state_id: "s", sort_order: 3072 },
  ];

  it("mid-group source takes prev + step (last row's sort_order + 1024)", () => {
    expect(
      planSpreadsheetReorderToEnd({
        rows,
        sourceId: "a1",
        stateId: "s",
      }),
    ).toEqual({ kind: "reorder", sort_order: 4096 });
  });

  it("source already last is a same-position no-op", () => {
    expect(
      planSpreadsheetReorderToEnd({
        rows,
        sourceId: "a3",
        stateId: "s",
      }),
    ).toEqual({ kind: "noop", reason: "same-position" });
  });

  it("source missing from the group is an unknown-target no-op", () => {
    expect(
      planSpreadsheetReorderToEnd({
        rows,
        sourceId: "zz",
        stateId: "s",
      }),
    ).toEqual({ kind: "noop", reason: "unknown-target" });
  });

  it("single-row group: the only row is already last", () => {
    expect(
      planSpreadsheetReorderToEnd({
        rows: [{ id: "only", state_id: "s", sort_order: 1024 }],
        sourceId: "only",
        stateId: "s",
      }),
    ).toEqual({ kind: "noop", reason: "same-position" });
  });
});

describe("Spreadsheet reorder indicator (C152T2)", () => {
  /** Three rows in Backlog (s) at even 1024-steps, one in In Progress. */
  function reorderIssues(): Issue[] {
    return [
      mkIssue({
        id: "a1",
        sequence_id: 1,
        display_id: "ENG-1",
        name: "Alpha",
        state_id: "s",
        sort_order: 1024,
        created_by: "u2",
      }),
      mkIssue({
        id: "a2",
        sequence_id: 2,
        display_id: "ENG-2",
        name: "Beta",
        state_id: "s",
        sort_order: 2048,
        created_by: "u2",
      }),
      mkIssue({
        id: "a3",
        sequence_id: 3,
        display_id: "ENG-3",
        name: "Gamma",
        state_id: "s",
        sort_order: 3072,
        created_by: "u2",
      }),
      mkIssue({
        id: "b1",
        sequence_id: 4,
        display_id: "ENG-4",
        name: "Delta",
        state_id: "s2",
        sort_order: 1024,
        created_by: "u2",
      }),
    ];
  }

  /** The indicator <tr>, or null when no line renders. */
  function indicatorRow(): HTMLElement | null {
    const table = screen.getByRole("table");
    return table.querySelector(
      "tr[data-spreadsheet-reorder-indicator]",
    ) as HTMLElement | null;
  }

  function headerRow(key: string): HTMLElement {
    const table = screen.getByRole("table");
    return table.querySelector(
      `tr[data-spreadsheet-group-header="${key}"]`,
    ) as HTMLElement;
  }

  /** Start a drag, then hover a row — the dragOver helper's active
   *  id is ignored by the handler; the dragStart id is what the
   *  indicator reads as the source. */
  function hoverRow(sourceDragId: string, overRowId: string) {
    dragStart(sourceDragId);
    dragOver(overRowId);
  }

  it("hovering a lower row shows the line below it and replaces the header highlight", async () => {
    mockData.issues = reorderIssues();
    await renderGrouped("state");
    hoverRow("spreadsheet-drag:s:a1", "spreadsheet-row-drop:s:a2");
    const line = indicatorRow();
    expect(line).not.toBeNull();
    expect(line!.getAttribute("data-spreadsheet-reorder-side")).toBe(
      "below",
    );
    expect(line!.getAttribute("data-spreadsheet-reorder-indicator")).toBe(
      "a2",
    );
    // The line sits immediately after the hovered row.
    expect(
      line!.previousElementSibling?.getAttribute("aria-label"),
    ).toBe("Open issue ENG-2: Beta");
    // C148 header highlight is replaced, not stacked, for
    // reorder-eligible hovers.
    expect(headerRow("s").className).not.toContain("bg-accent-blue/10");
  });

  it("hovering a higher row shows the line above it", async () => {
    mockData.issues = reorderIssues();
    await renderGrouped("state");
    hoverRow("spreadsheet-drag:s:a3", "spreadsheet-row-drop:s:a2");
    const line = indicatorRow();
    expect(line).not.toBeNull();
    expect(line!.getAttribute("data-spreadsheet-reorder-side")).toBe(
      "above",
    );
    // The line sits immediately before the hovered row.
    expect(line!.nextElementSibling?.getAttribute("aria-label")).toBe(
      "Open issue ENG-2: Beta",
    );
    expect(headerRow("s").className).not.toContain("bg-accent-blue/10");
  });

  it("hovering the source row itself shows no line", async () => {
    mockData.issues = reorderIssues();
    await renderGrouped("state");
    hoverRow("spreadsheet-drag:s:a1", "spreadsheet-row-drop:s:a1");
    expect(indicatorRow()).toBeNull();
    // No reorder hover → the C148 header highlight still applies.
    expect(headerRow("s").className).toContain("bg-accent-blue/10");
  });

  it("hovering a row in a priority group shows no line (highlight only)", async () => {
    mockData.issues = defaultIssues();
    await renderGrouped("priority");
    hoverRow(
      "spreadsheet-drag:priority:2:i1",
      "spreadsheet-row-drop:priority:2:i3",
    );
    expect(indicatorRow()).toBeNull();
    expect(headerRow("priority:2").className).toContain(
      "bg-accent-blue/10",
    );
  });

  it("hovering with a column sort active shows no line", async () => {
    mockData.issues = reorderIssues();
    await renderGrouped("state");
    fireEvent.click(screen.getByRole("button", { name: "Sort by Title" }));
    await waitFor(() => {
      expect(
        screen
          .getByRole("button", { name: "Sort by Title" })
          .getAttribute("aria-sort"),
      ).toBe("ascending");
    });
    hoverRow("spreadsheet-drag:s:a1", "spreadsheet-row-drop:s:a2");
    expect(indicatorRow()).toBeNull();
  });

  it("hovering within a sub-group run shows the line (C153T2: retires the C152 no-line rule)", async () => {
    mockData.issues = reorderIssues();
    await renderGrouped("state", "priority");
    // All fixture rows share priority 0, so they are one run of
    // the Backlog group: a1 above a2 → the line sits below a2.
    hoverRow("spreadsheet-drag:s:a1", "spreadsheet-row-drop:s:a2");
    const line = indicatorRow();
    expect(line).not.toBeNull();
    expect(line!.getAttribute("data-spreadsheet-reorder-side")).toBe(
      "below",
    );
    expect(line!.getAttribute("data-spreadsheet-reorder-indicator")).toBe(
      "a2",
    );
    expect(headerRow("s").className).not.toContain("bg-accent-blue/10");
  });

  it("PQL-mode hover shows no line", async () => {
    mockData.issues = reorderIssues();
    setGrouping({ groupBy: "state", subGroupBy: "none" });
    renderSpreadsheet("/w/acme/p/ENG/spreadsheet?pql=priority+%3E%3D+0");
    await screen.findByRole("button", { name: "Sort by Title" });
    await waitFor(() => expect(handles().length).toBeGreaterThan(0));
    hoverRow("spreadsheet-drag:s:a1", "spreadsheet-row-drop:s:a2");
    expect(indicatorRow()).toBeNull();
  });

  it("cross-group hover shows no line (highlight only)", async () => {
    mockData.issues = reorderIssues();
    await renderGrouped("state");
    hoverRow("spreadsheet-drag:s:a1", "spreadsheet-row-drop:s2:b1");
    expect(indicatorRow()).toBeNull();
    expect(headerRow("s2").className).toContain("bg-accent-blue/10");
    expect(headerRow("s").className).not.toContain("bg-accent-blue/10");
  });

  it("dropping on the group header PATCHes end-of-group sort_order (prev + 1024)", async () => {
    mockData.issues = reorderIssues();
    await renderGrouped("state");
    const patch = await patchMock();
    drop("spreadsheet-drag:s:a1", "spreadsheet-group:s");
    await waitFor(() => {
      expect(patch).toHaveBeenCalledWith(
        expect.stringContaining("/issues/a1"),
        { sort_order: 4096 },
      );
    });
    await waitFor(() => {
      expect(rowLabels()).toEqual([
        "Open issue ENG-2: Beta",
        "Open issue ENG-3: Gamma",
        "Open issue ENG-1: Alpha",
        "Open issue ENG-4: Delta",
      ]);
    });
  });

  it("dropping on the header of a different state group keeps C148 cross-group semantics (state move, not reorder)", async () => {
    mockData.issues = reorderIssues();
    await renderGrouped("state");
    const patch = await patchMock();
    drop("spreadsheet-drag:s:a1", "spreadsheet-group:s2");
    await waitFor(() => {
      expect(patch).toHaveBeenCalledWith(
        expect.stringContaining("/issues/a1"),
        { state_id: "s2" },
      );
    });
    await waitFor(() => {
      expect(groupHeaders()).toEqual([
        "Backlog (2)",
        "In Progress (2)",
      ]);
    });
  });

  it("header drop when already last PATCHes nothing", async () => {
    mockData.issues = reorderIssues();
    await renderGrouped("state");
    const patch = await patchMock();
    drop("spreadsheet-drag:s:a3", "spreadsheet-group:s");
    expect(patch).not.toHaveBeenCalled();
  });
});

describe("Spreadsheet subgroup reorder indicator (C153T2)", () => {
  /** Backlog (s) rows split across priority sub-sections. */
  function subgroupIssues(): Issue[] {
    return [
      mkIssue({
        id: "a1",
        sequence_id: 1,
        display_id: "ENG-1",
        name: "Alpha",
        priority: 0,
        state_id: "s",
        sort_order: 1024,
        created_by: "u2",
      }),
      mkIssue({
        id: "a2",
        sequence_id: 2,
        display_id: "ENG-2",
        name: "Beta",
        priority: 2,
        state_id: "s",
        sort_order: 2048,
        created_by: "u2",
      }),
      mkIssue({
        id: "a3",
        sequence_id: 3,
        display_id: "ENG-3",
        name: "Gamma",
        priority: 0,
        state_id: "s",
        sort_order: 3072,
        created_by: "u2",
      }),
      mkIssue({
        id: "b1",
        sequence_id: 4,
        display_id: "ENG-4",
        name: "Delta",
        priority: 0,
        state_id: "s2",
        sort_order: 1024,
        created_by: "u2",
      }),
    ];
  }

  function indicatorRow(): HTMLElement | null {
    const table = screen.getByRole("table");
    return table.querySelector(
      "tr[data-spreadsheet-reorder-indicator]",
    ) as HTMLElement | null;
  }

  function headerRow(key: string): HTMLElement {
    const table = screen.getByRole("table");
    return table.querySelector(
      `tr[data-spreadsheet-group-header="${key}"]`,
    ) as HTMLElement;
  }

  function hoverRow(sourceDragId: string, overRowId: string) {
    dragStart(sourceDragId);
    dragOver(overRowId);
  }

  it("hovering a lower row within the run shows the line below it", async () => {
    mockData.issues = subgroupIssues();
    await renderGrouped("state", "priority");
    // a1 and a3 share the priority-0 run; a1 above a3 → below.
    hoverRow("spreadsheet-drag:s:a1", "spreadsheet-row-drop:s:a3");
    const line = indicatorRow();
    expect(line).not.toBeNull();
    expect(line!.getAttribute("data-spreadsheet-reorder-side")).toBe(
      "below",
    );
    expect(line!.getAttribute("data-spreadsheet-reorder-indicator")).toBe(
      "a3",
    );
    // The line sits immediately after the hovered row.
    expect(
      line!.previousElementSibling?.getAttribute("aria-label"),
    ).toBe("Open issue ENG-3: Gamma");
    expect(headerRow("s").className).not.toContain("bg-accent-blue/10");
  });

  it("hovering a higher row within the run shows the line above it", async () => {
    mockData.issues = subgroupIssues();
    await renderGrouped("state", "priority");
    // a3 below a1 in the priority-0 run → above.
    hoverRow("spreadsheet-drag:s:a3", "spreadsheet-row-drop:s:a1");
    const line = indicatorRow();
    expect(line).not.toBeNull();
    expect(line!.getAttribute("data-spreadsheet-reorder-side")).toBe(
      "above",
    );
    // The line sits immediately before the hovered row.
    expect(line!.nextElementSibling?.getAttribute("aria-label")).toBe(
      "Open issue ENG-1: Alpha",
    );
  });

  it("cross-section hover shows no line (C154T2: section highlight, not the primary header)", async () => {
    mockData.issues = subgroupIssues();
    await renderGrouped("state", "priority");
    // a1 (priority 0) and a2 (priority 2) are different runs of
    // the same primary group — no run resolves, no line. C154T2
    // retires the C153 rule (primary header highlighted): the
    // cross-section hover is a move, so the TARGET sub-header
    // highlights and the primary header does not.
    hoverRow("spreadsheet-drag:s:a1", "spreadsheet-row-drop:s:a2");
    expect(indicatorRow()).toBeNull();
    expect(headerRow("s").className).not.toContain("bg-accent-blue/10");
    const table = screen.getByRole("table");
    const target = table.querySelector(
      'tr[data-spreadsheet-sub-group-header="s:priority:2"]',
    ) as HTMLElement;
    expect(target.className).toContain("bg-accent-blue/10");
    const source = table.querySelector(
      'tr[data-spreadsheet-sub-group-header="s:priority:0"]',
    ) as HTMLElement;
    expect(source.className).not.toContain("bg-accent-blue/10");
  });

  it("multi-appearance hover shows no line", async () => {
    mockData.issues = [
      mkIssue({
        id: "a1",
        sequence_id: 1,
        display_id: "ENG-1",
        name: "Alpha",
        state_id: "s",
        sort_order: 1024,
        created_by: "u2",
        assignees: [
          { id: "u2", name: "Ada" },
          { id: "u3", name: "Bob" },
        ],
      }),
      mkIssue({
        id: "a2",
        sequence_id: 2,
        display_id: "ENG-2",
        name: "Beta",
        state_id: "s",
        sort_order: 2048,
        created_by: "u2",
        assignees: [{ id: "u2", name: "Ada" }],
      }),
      mkIssue({
        id: "a3",
        sequence_id: 3,
        display_id: "ENG-3",
        name: "Gamma",
        state_id: "s",
        sort_order: 3072,
        created_by: "u2",
        assignees: [{ id: "u2", name: "Ada" }],
      }),
    ];
    await renderGrouped("state", "assignees");
    // a1 appears under both Ada and Bob — no run resolves.
    hoverRow("spreadsheet-drag:s:a1", "spreadsheet-row-drop:s:a2");
    expect(indicatorRow()).toBeNull();
    expect(headerRow("s").className).toContain("bg-accent-blue/10");
  });

  it("sub-grouped header drop PATCHes end-of-group sort_order (prev + 1024)", async () => {
    mockData.issues = subgroupIssues();
    await renderGrouped("state", "priority");
    const patch = await patchMock();
    drop("spreadsheet-drag:s:a1", "spreadsheet-group:s");
    await waitFor(() => {
      expect(patch).toHaveBeenCalledWith(
        expect.stringContaining("/issues/a1"),
        { sort_order: 4096 },
      );
    });
  });

  it("sub-grouped header drop when already last PATCHes nothing", async () => {
    mockData.issues = subgroupIssues();
    await renderGrouped("state", "priority");
    const patch = await patchMock();
    // a3 holds the max sort_order (3072) of the full Backlog
    // sequence, so it is already last in the group.
    drop("spreadsheet-drag:s:a3", "spreadsheet-group:s");
    expect(patch).not.toHaveBeenCalled();
  });
});

describe("Spreadsheet subsection move highlight (C154T2)", () => {
  /** Backlog (s) rows split across priority sub-sections. */
  function subgroupIssues(): Issue[] {
    return [
      mkIssue({
        id: "a1",
        sequence_id: 1,
        display_id: "ENG-1",
        name: "Alpha",
        priority: 0,
        state_id: "s",
        sort_order: 1024,
        created_by: "u2",
      }),
      mkIssue({
        id: "a2",
        sequence_id: 2,
        display_id: "ENG-2",
        name: "Beta",
        priority: 2,
        state_id: "s",
        sort_order: 2048,
        created_by: "u2",
      }),
      mkIssue({
        id: "a3",
        sequence_id: 3,
        display_id: "ENG-3",
        name: "Gamma",
        priority: 0,
        state_id: "s",
        sort_order: 3072,
        created_by: "u2",
      }),
    ];
  }

  function indicatorRow(): HTMLElement | null {
    const table = screen.getByRole("table");
    return table.querySelector(
      "tr[data-spreadsheet-reorder-indicator]",
    ) as HTMLElement | null;
  }

  function headerRow(key: string): HTMLElement {
    const table = screen.getByRole("table");
    return table.querySelector(
      `tr[data-spreadsheet-group-header="${key}"]`,
    ) as HTMLElement;
  }

  /** Every rendered sub-group header row in the grid. */
  function subHeaderRows(): HTMLElement[] {
    const table = screen.getByRole("table");
    return Array.from(
      table.querySelectorAll("tr[data-spreadsheet-sub-group-header]"),
    ) as HTMLElement[];
  }

  function hoverRow(sourceDragId: string, overRowId: string) {
    dragStart(sourceDragId);
    dragOver(overRowId);
  }

  it("cross-section hover highlights the target sub-header and NOT the primary header", async () => {
    mockData.issues = subgroupIssues();
    await renderGrouped("state", "priority");
    // a1 (priority 0) → a2 (priority 2): a move, so the target
    // section highlights with the primary-header token verbatim
    // and the primary header stays unhighlighted.
    hoverRow("spreadsheet-drag:s:a1", "spreadsheet-row-drop:s:a2");
    expect(indicatorRow()).toBeNull();
    expect(headerRow("s").className).not.toContain("bg-accent-blue/10");
    const table = screen.getByRole("table");
    const target = table.querySelector(
      'tr[data-spreadsheet-sub-group-header="s:priority:2"]',
    ) as HTMLElement;
    expect(target.className).toContain("bg-accent-blue/10");
    const source = table.querySelector(
      'tr[data-spreadsheet-sub-group-header="s:priority:0"]',
    ) as HTMLElement;
    expect(source.className).not.toContain("bg-accent-blue/10");
  });

  it("same-section hover highlights neither sub-header (reorder line per the existing rules)", async () => {
    mockData.issues = subgroupIssues();
    await renderGrouped("state", "priority");
    // a1 and a3 share the priority-0 run: reorder-eligible, so
    // the line shows, the move memo returns null, and no
    // sub-header highlights.
    hoverRow("spreadsheet-drag:s:a1", "spreadsheet-row-drop:s:a3");
    const line = indicatorRow();
    expect(line).not.toBeNull();
    expect(line!.getAttribute("data-spreadsheet-reorder-side")).toBe(
      "below",
    );
    expect(headerRow("s").className).not.toContain("bg-accent-blue/10");
    for (const row of subHeaderRows()) {
      expect(row.className).not.toContain("bg-accent-blue/10");
    }
  });

  it("multi-appearance hover highlights neither sub-header", async () => {
    mockData.issues = [
      mkIssue({
        id: "a1",
        sequence_id: 1,
        display_id: "ENG-1",
        name: "Alpha",
        state_id: "s",
        sort_order: 1024,
        created_by: "u2",
        assignees: [
          { id: "u2", name: "Ada" },
          { id: "u3", name: "Bob" },
        ],
      }),
      mkIssue({
        id: "a2",
        sequence_id: 2,
        display_id: "ENG-2",
        name: "Beta",
        state_id: "s",
        sort_order: 2048,
        created_by: "u2",
        assignees: [{ id: "u2", name: "Ada" }],
      }),
    ];
    await renderGrouped("state", "assignees");
    // a1 appears under both Ada and Bob, so no unique target
    // section resolves: no line, no section highlight. The
    // primary header keeps today's behavior (highlighted).
    hoverRow("spreadsheet-drag:s:a1", "spreadsheet-row-drop:s:a2");
    expect(indicatorRow()).toBeNull();
    expect(headerRow("s").className).toContain("bg-accent-blue/10");
    for (const row of subHeaderRows()) {
      expect(row.className).not.toContain("bg-accent-blue/10");
    }
  });

  it("non-move hover (createdBy sub-group) highlights neither sub-header beyond today's primary-header behavior", async () => {
    mockData.issues = [
      mkIssue({
        id: "i1",
        sequence_id: 1,
        display_id: "ENG-1",
        name: "Alpha",
        priority: 2,
        state_id: "s",
        created_by: "u2",
      }),
      mkIssue({
        id: "i2",
        sequence_id: 2,
        display_id: "ENG-2",
        name: "Beta",
        priority: 2,
        state_id: "s2",
        created_by: "u9",
      }),
    ];
    await renderGrouped("priority", "createdBy");
    // createdBy is a deliberate no-op dimension, so the delegated
    // plan is a noop and no section highlights; the primary
    // header keeps today's behavior (highlighted).
    hoverRow(
      "spreadsheet-drag:priority:2:i1",
      "spreadsheet-row-drop:priority:2:i2",
    );
    expect(indicatorRow()).toBeNull();
    expect(headerRow("priority:2").className).toContain(
      "bg-accent-blue/10",
    );
    for (const row of subHeaderRows()) {
      expect(row.className).not.toContain("bg-accent-blue/10");
    }
  });
});

describe("Spreadsheet subsection header hover highlight (C155T2)", () => {
  /** Backlog (s) rows split across priority sub-sections. */
  function subgroupIssues(): Issue[] {
    return [
      mkIssue({
        id: "a1",
        sequence_id: 1,
        display_id: "ENG-1",
        name: "Alpha",
        priority: 0,
        state_id: "s",
        sort_order: 1024,
        created_by: "u2",
      }),
      mkIssue({
        id: "a2",
        sequence_id: 2,
        display_id: "ENG-2",
        name: "Beta",
        priority: 2,
        state_id: "s",
        sort_order: 2048,
        created_by: "u2",
      }),
      mkIssue({
        id: "a3",
        sequence_id: 3,
        display_id: "ENG-3",
        name: "Gamma",
        priority: 0,
        state_id: "s",
        sort_order: 3072,
        created_by: "u2",
      }),
    ];
  }

  function indicatorRow(): HTMLElement | null {
    const table = screen.getByRole("table");
    return table.querySelector(
      "tr[data-spreadsheet-reorder-indicator]",
    ) as HTMLElement | null;
  }

  function headerRow(key: string): HTMLElement {
    const table = screen.getByRole("table");
    return table.querySelector(
      `tr[data-spreadsheet-group-header="${key}"]`,
    ) as HTMLElement;
  }

  /** Every rendered sub-group header row in the grid. */
  function subHeaderRows(): HTMLElement[] {
    const table = screen.getByRole("table");
    return Array.from(
      table.querySelectorAll("tr[data-spreadsheet-sub-group-header]"),
    ) as HTMLElement[];
  }

  function subHeaderRow(groupKey: string, sectionKey: string): HTMLElement {
    const table = screen.getByRole("table");
    return table.querySelector(
      `tr[data-spreadsheet-sub-group-header="${groupKey}:${sectionKey}"]`,
    ) as HTMLElement;
  }

  function hoverHeader(sourceDragId: string, overHeaderId: string) {
    dragStart(sourceDragId);
    dragOver(overHeaderId);
  }

  it("header hover highlights the target sub-header and NOT the primary header, with no insertion line", async () => {
    mockData.issues = subgroupIssues();
    await renderGrouped("state", "priority");
    // a1 (priority 0) → the priority-2 sub-header: a move, so the
    // target section highlights with the primary-header token
    // verbatim and the primary header stays unhighlighted.
    hoverHeader(
      "spreadsheet-drag:s:a1",
      spreadsheetSubGroupDroppableId("s", "priority:2"),
    );
    expect(indicatorRow()).toBeNull();
    expect(headerRow("s").className).not.toContain("bg-accent-blue/10");
    expect(
      subHeaderRow("s", "priority:2").className,
    ).toContain("bg-accent-blue/10");
    expect(
      subHeaderRow("s", "priority:0").className,
    ).not.toContain("bg-accent-blue/10");
  });

  it("same-section header hover highlights neither sub-header (primary header keeps today's behavior)", async () => {
    mockData.issues = subgroupIssues();
    await renderGrouped("state", "priority");
    // a1 over its own priority-0 sub-header: the header-drop
    // helper returns null (source already in the target section),
    // so no section highlights and no line renders; the primary
    // header keeps today's C148 behavior (highlighted).
    hoverHeader(
      "spreadsheet-drag:s:a1",
      spreadsheetSubGroupDroppableId("s", "priority:0"),
    );
    expect(indicatorRow()).toBeNull();
    expect(headerRow("s").className).toContain("bg-accent-blue/10");
    for (const row of subHeaderRows()) {
      expect(row.className).not.toContain("bg-accent-blue/10");
    }
  });

  it("non-move header hover (createdBy sub-group) highlights neither sub-header", async () => {
    mockData.issues = [
      mkIssue({
        id: "i1",
        sequence_id: 1,
        display_id: "ENG-1",
        name: "Alpha",
        priority: 2,
        state_id: "s",
        created_by: "u2",
      }),
      mkIssue({
        id: "i2",
        sequence_id: 2,
        display_id: "ENG-2",
        name: "Beta",
        priority: 2,
        state_id: "s2",
        created_by: "u9",
      }),
    ];
    await renderGrouped("priority", "createdBy");
    // createdBy is a deliberate no-op dimension, so the delegated
    // plan is a noop and no section highlights; the primary
    // header keeps today's behavior (highlighted).
    hoverHeader(
      "spreadsheet-drag:priority:2:i1",
      spreadsheetSubGroupDroppableId("priority:2", "u9"),
    );
    expect(indicatorRow()).toBeNull();
    expect(headerRow("priority:2").className).toContain(
      "bg-accent-blue/10",
    );
    for (const row of subHeaderRows()) {
      expect(row.className).not.toContain("bg-accent-blue/10");
    }
  });

  it("cross-group header hover highlights neither sub-header (target primary header keeps today's behavior)", async () => {
    mockData.issues = [
      ...subgroupIssues(),
      mkIssue({
        id: "b1",
        sequence_id: 4,
        display_id: "ENG-4",
        name: "Delta",
        priority: 2,
        state_id: "s2",
        sort_order: 1024,
        created_by: "u2",
      }),
    ];
    await renderGrouped("state", "priority");
    // a1 (group s) over the s2 priority-2 sub-header: the memo
    // resolves same-group only, so no section highlights and no
    // line; the target primary header keeps today's C148
    // behavior (highlighted) and the source header stays plain.
    hoverHeader(
      "spreadsheet-drag:s:a1",
      spreadsheetSubGroupDroppableId("s2", "priority:2"),
    );
    expect(indicatorRow()).toBeNull();
    expect(headerRow("s2").className).toContain("bg-accent-blue/10");
    expect(headerRow("s").className).not.toContain("bg-accent-blue/10");
    for (const row of subHeaderRows()) {
      expect(row.className).not.toContain("bg-accent-blue/10");
    }
  });
});
