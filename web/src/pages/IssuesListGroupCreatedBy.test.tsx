// @vitest-environment jsdom
// C134T1: list-view Group-by-Created-by at the page level — creator
// sections in first-appearance order (label = name ?? email from the
// workspace members lookup), unresolved creators collapsed into ONE
// Unknown group last/hidden-when-empty, no raw user id ever rendered
// as a label, NO header drop zones under created-by grouping (no move
// pipeline — created_by is immutable), and NO per-group quick-add
// under created-by grouping (the global add stays available).
// The pending-members contract (no Unknown flash on first paint) is
// covered with a deferred members promise: while it hangs, no group
// renders at all; once it resolves, the sections appear labelled.
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
import type { Issue, IssueState, Member } from "../lib/types";
import {
  makeQueryClient,
  stubMatchMedia,
} from "../components/shell/test-utils";

const mockState = vi.hoisted(() => ({
  settings: null as unknown as DisplaySettings & { subGroupBy: SubGroupBy },
  issues: [] as Issue[],
  // Resolved members by default; the pending test swaps this for a
  // deferred promise it resolves by hand.
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

const states: IssueState[] = [
  { id: "s-todo", project_id: "p", name: "Todo", group: "unstarted", color: "#888", sequence: 1, created_at: "", updated_at: "" },
  { id: "s-doing", project_id: "p", name: "Doing", group: "started", color: "#888", sequence: 2, created_at: "", updated_at: "" },
];

const MEMBERS: Member[] = [
  { id: "u-alice", name: "Alice", email: "alice@x.com", role: 1 },
  // Name null on the wire → section labelled by email.
  { id: "u-bob", name: null, email: "bob@x.com", role: 1 },
];

function mkIssue(
  id: string,
  displayId: string,
  name: string,
  stateId: string,
  sortOrder: number,
  createdBy: string,
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
    created_by: createdBy,
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

function dropIds(): string[] {
  return screen
    .queryAllByTestId("header-dropzone")
    .map((el) => el.getAttribute("data-drop-id") ?? "");
}

function quickAdds(): HTMLElement[] {
  return screen.queryAllByLabelText("Add new work item inline");
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

describe("created-by grouping (C134T1)", () => {
  function createdByIssues() {
    return [
      mkIssue("i-a", "ENG-1", "First", "s-todo", 1024, "u-alice"),
      mkIssue("i-b", "ENG-2", "Second", "s-doing", 2048, "u-bob"),
      mkIssue("i-c", "ENG-3", "Third", "s-todo", 3072, "u-alice"),
      // Removed member: no matching workspace member.
      mkIssue("i-d", "ENG-4", "Fourth", "s-doing", 4096, "u-gone"),
    ];
  }

  it("renders one section per creator in first-appearance order, Unknown last", async () => {
    setSettings({ groupBy: "createdBy", subGroupBy: "none", orderBy: "-updated_at" });
    mockState.issues = createdByIssues();
    renderIssues();
    await screen.findByText("First");
    expect(sectionLabels()).toEqual(["Alice", "bob@x.com", "Unknown"]);
    expect(
      screen.getByRole("heading", { name: "Alice" }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("heading", { name: "bob@x.com" }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("heading", { name: "Unknown" }),
    ).toBeInTheDocument();
  });

  it("never renders a raw creator id as a group label", async () => {
    setSettings({ groupBy: "createdBy", subGroupBy: "none", orderBy: "-updated_at" });
    mockState.issues = createdByIssues();
    renderIssues();
    await screen.findByText("First");
    expect(document.body.textContent).not.toContain("u-gone");
    expect(document.body.textContent).not.toContain("u-alice");
    expect(document.body.textContent).not.toContain("u-bob");
  });

  it("hides Unknown when every creator resolves, and synthesizes no creator groups", async () => {
    setSettings({ groupBy: "createdBy", subGroupBy: "none", orderBy: "-updated_at" });
    mockState.issues = [
      mkIssue("i-a", "ENG-1", "First", "s-todo", 1024, "u-alice"),
      mkIssue("i-c", "ENG-3", "Third", "s-todo", 3072, "u-alice"),
    ];
    renderIssues();
    await screen.findByText("First");
    // Only Alice's group — no Unknown, and no synthesized group for
    // Bob, who has no loaded issues (showEmptyGroups synthesizes
    // nothing for created-by).
    expect(sectionLabels()).toEqual(["Alice"]);
  });

  it("renders NO header drop zones under created-by grouping", async () => {
    setSettings({ groupBy: "createdBy", subGroupBy: "none", orderBy: "sort_order" });
    mockState.issues = createdByIssues();
    renderIssues();
    await screen.findByText("First");
    expect(dropIds()).toHaveLength(0);
  });

  it("renders NO per-group quick-add under created-by grouping", async () => {
    setSettings({ groupBy: "createdBy", subGroupBy: "none", orderBy: "-updated_at" });
    mockState.issues = createdByIssues();
    renderIssues();
    await screen.findByText("First");
    expect(quickAdds()).toHaveLength(0);
  });

  it("control: per-group quick-add still renders under state grouping", async () => {
    setSettings({ groupBy: "state", subGroupBy: "none", orderBy: "-updated_at" });
    mockState.issues = createdByIssues();
    renderIssues();
    await screen.findByText("First");
    // showEmptyGroups renders both states, each with its quick-add.
    expect(quickAdds()).toHaveLength(2);
  });

  it("within-group reorder grips appear under Manual rank", async () => {
    setSettings({ groupBy: "createdBy", subGroupBy: "none", orderBy: "sort_order" });
    mockState.issues = createdByIssues();
    renderIssues();
    await screen.findByText("First");
    expect(grips()).toHaveLength(4);
  });

  it("while members are pending, no group renders — no Unknown flash, no id labels", async () => {
    let resolveMembers!: (v: { members: Member[] }) => void;
    const deferred = new Promise<{ members: Member[] }>((resolve) => {
      resolveMembers = resolve;
    });
    mockState.membersResult = deferred;
    setSettings({ groupBy: "createdBy", subGroupBy: "none", orderBy: "-updated_at" });
    mockState.issues = createdByIssues();
    renderIssues();
    // Let the issues query resolve and the page render its pending
    // (group-less) state; the members promise is still hanging.
    await new Promise((r) => setTimeout(r, 50));
    expect(screen.queryByText("Unknown")).toBeNull();
    expect(screen.queryByText("First")).toBeNull();
    expect(sectionLabels()).toEqual([]);
    // Once members resolve, the labelled sections appear.
    resolveMembers({ members: MEMBERS });
    await screen.findByText("First");
    expect(sectionLabels()).toEqual(["Alice", "bob@x.com", "Unknown"]);
  });
});
