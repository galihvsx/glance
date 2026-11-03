// @vitest-environment jsdom
// C123T0: "Sub-group by" on the board — sub-group sections inside each
// primary column (sub-header title + count, then that section's cards),
// "none" = today's flat layout, same-as-primary and groupBy-none both
// render flat, epic multi-appearance inside a column, and C122 nested
// rows follow their parent's section. BoardCardBody is module-private,
// so these tests drive the real Board page with a mocked API
// (BoardDisplay.test.tsx pattern).

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import { QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import Board from "./Board";
import { api } from "../lib/api";
import type { Issue, IssueState, SubtaskRow } from "../lib/types";
import {
  makeQueryClient,
  stubMatchMedia,
} from "../components/shell/test-utils";

// Board renders SavedViewsMenu → useAuth; the real provider needs a
// session — stub the auth context (Issues.test.tsx pattern).
vi.mock("../lib/auth", () => ({
  useAuth: () => ({ user: { id: "u-1", email: "u@x.com" }, loading: false }),
}));

const ENG = "/api/v1/workspaces/acme/projects/ENG";
const KEY = "glance:display:acme:ENG";

const states: IssueState[] = [
  {
    id: "s-todo",
    project_id: "p-eng",
    name: "Todo",
    group: "unstarted",
    color: "#888",
    sequence: 1,
    created_at: "",
    updated_at: "",
  },
  {
    id: "s-done",
    project_id: "p-eng",
    name: "Done",
    group: "completed",
    color: "#4ade80",
    sequence: 2,
    created_at: "",
    updated_at: "",
  },
];

function makeIssue(
  id: string,
  displayId: string,
  name: string,
  stateId: string,
  sortOrder: number,
  extra: Partial<Issue> = {},
): Issue {
  return {
    id,
    project_id: "p-eng",
    sequence_id: 1,
    display_id: displayId,
    name,
    priority: 3,
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

const EPIC_A = { id: "epic-a", name: "Alpha" };
const EPIC_B = { id: "epic-b", name: "Beta" };

// Todo column: Urgent ×2, Medium ×1, Low ×1; Done column: Urgent ×1.
const issueA = makeIssue("i-a", "ENG-1", "Fix login", "s-todo", 1, {
  priority: 4,
  epics: [EPIC_A],
});
const issueB = makeIssue("i-b", "ENG-2", "Write docs", "s-todo", 2, {
  priority: 1,
  epics: [],
});
const issueC = makeIssue("i-c", "ENG-3", "Ship feature", "s-todo", 3, {
  priority: 4,
  epics: [EPIC_B],
});
const issueD = makeIssue("i-d", "ENG-4", "Release notes", "s-done", 4, {
  priority: 4,
  epics: [EPIC_A, EPIC_B],
});
const issueE = makeIssue("i-e", "ENG-5", "Multi epic todo", "s-todo", 5, {
  priority: 2,
  epics: [EPIC_A, EPIC_B],
});
const payload = [issueA, issueB, issueC, issueD, issueE];

function childRow(id: string, title: string): SubtaskRow {
  return {
    id,
    display_id: id.toUpperCase(),
    title,
    state_id: "s-done",
    priority: 4,
    assignee_ids: [],
    sort_order: 1,
  };
}
const parent = makeIssue("i-p", "ENG-1", "Parent task", "s-todo", 1, {
  priority: 3,
  subtasks: [childRow("i-c", "Child task")],
});
const child = makeIssue("i-c", "ENG-2", "Child task", "s-done", 2);
const nestedPayload = [parent, child];

function renderBoard(
  issues: Issue[],
  members: unknown[] = [],
) {
  vi.spyOn(api, "get").mockImplementation((path: string) => {
    if (path === "/api/v1/workspaces/acme")
      return Promise.resolve({ role: 15 });
    if (path === ENG) return Promise.resolve({ id: "p-eng", name: "ENG" });
    if (path.endsWith("/states")) return Promise.resolve({ states });
    if (path.endsWith("/workflow"))
      return Promise.resolve({ states, transitions: [] });
    if (path.endsWith("/members"))
      return Promise.resolve({ members });
    if (path.endsWith("/labels")) return Promise.resolve({ labels: [] });
    if (path.endsWith("/estimates")) return Promise.resolve({ estimates: [] });
    if (path.includes("/epics?")) return Promise.resolve({ epics: [] });
    if (path.includes("/projects/") && path.endsWith("/views"))
      return Promise.resolve({ views: [] });
    if (path.startsWith(`${ENG}/issues`))
      return Promise.resolve({ results: issues, next_cursor: null });
    return Promise.reject(new Error(`unexpected GET ${path}`));
  });
  const qc = makeQueryClient();
  render(
    <MemoryRouter initialEntries={["/w/acme/p/ENG/board"]}>
      <QueryClientProvider client={qc}>
        <Routes>
          <Route path="/w/:slug/p/:identifier/board" element={<Board />} />
        </Routes>
      </QueryClientProvider>
    </MemoryRouter>,
  );
}

function storeSettings(settings: Record<string, unknown>) {
  localStorage.setItem(KEY, JSON.stringify(settings));
}

function allSections(): HTMLElement[] {
  return Array.from(
    document.querySelectorAll("[data-sub-group-section]"),
  ) as HTMLElement[];
}

function sectionKeyOfCard(issueId: string): string | null {
  const card = document.querySelector(`[data-board-card="${issueId}"]`);
  return (
    card
      ?.closest("[data-sub-group-section]")
      ?.getAttribute("data-sub-group-section") ?? null
  );
}

/** [title, count] of a section's sub-header (the dot span, when
 *  present, carries no text and drops out of the filtered list). */
function sectionHeader(section: HTMLElement): [string, string] {
  const spans = Array.from(
    (section.firstElementChild as HTMLElement).querySelectorAll("span"),
  )
    .map((s) => s.textContent ?? "")
    .filter((t) => t !== "");
  return [spans[0] ?? "", spans[1] ?? ""];
}

beforeEach(() => {
  stubMatchMedia(false);
  localStorage.clear();
});

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

describe("board sub-group by (C123T0)", () => {
  it("renders priority sub-sections with per-section counts and card placement", async () => {
    storeSettings({
      groupBy: "state",
      subGroupBy: "priority",
      showEmptyGroups: true,
    });
    renderBoard(payload);
    await screen.findByText("Fix login");
    // 2 columns × 5 priority sections (showEmptyGroups on).
    expect(allSections()).toHaveLength(10);
    const urgent = allSections().filter(
      (s) => s.getAttribute("data-sub-group-section") === "priority:4",
    );
    expect(urgent).toHaveLength(2);
    // Todo column first (state sequence order): 2 Urgent, then Done: 1.
    expect(sectionHeader(urgent[0])).toEqual(["Urgent", "2"]);
    expect(sectionHeader(urgent[1])).toEqual(["Urgent", "1"]);
    expect(sectionKeyOfCard("i-a")).toBe("priority:4");
    expect(sectionKeyOfCard("i-b")).toBe("priority:1");
    expect(sectionKeyOfCard("i-e")).toBe("priority:2");
    expect(sectionKeyOfCard("i-d")).toBe("priority:4");
  });

  it("hides empty sub-sections when showEmptyGroups is off", async () => {
    storeSettings({
      groupBy: "state",
      subGroupBy: "priority",
      showEmptyGroups: false,
    });
    renderBoard(payload);
    await screen.findByText("Fix login");
    // Todo: Urgent, Medium, Low; Done: Urgent. No empty sections.
    expect(allSections()).toHaveLength(4);
    expect(
      allSections().map((s) =>
        s.getAttribute("data-sub-group-section"),
      ),
    ).toEqual(["priority:4", "priority:2", "priority:1", "priority:4"]);
  });

  it("same-as-primary sub-group renders flat (no sections)", async () => {
    storeSettings({
      groupBy: "state",
      subGroupBy: "state",
      showEmptyGroups: true,
    });
    renderBoard(payload);
    await screen.findByText("Fix login");
    expect(allSections()).toHaveLength(0);
    expect(
      document.querySelector('[data-board-card="i-a"]'),
    ).not.toBeNull();
  });

  it("groupBy none renders flat whatever sub-group is stored", async () => {
    storeSettings({
      groupBy: "none",
      subGroupBy: "priority",
      showEmptyGroups: true,
    });
    renderBoard(payload);
    await screen.findByText("Fix login");
    expect(allSections()).toHaveLength(0);
  });

  it("subGroupBy none is today's layout exactly", async () => {
    storeSettings({
      groupBy: "state",
      subGroupBy: "none",
      showEmptyGroups: true,
    });
    renderBoard(payload);
    await screen.findByText("Fix login");
    expect(allSections()).toHaveLength(0);
    for (const id of ["i-a", "i-b", "i-c", "i-d", "i-e"]) {
      expect(
        document.querySelector(`[data-board-card="${id}"]`),
      ).not.toBeNull();
    }
  });

  it("epic sub-sections: multi-appearance inside a column, Ungrouped last", async () => {
    storeSettings({
      groupBy: "state",
      subGroupBy: "epic",
      showEmptyGroups: true,
    });
    renderBoard(payload);
    await screen.findByText("Fix login");
    // Todo: Alpha, Beta, Ungrouped; Done: Alpha, Beta.
    expect(
      allSections().map((s) =>
        s.getAttribute("data-sub-group-section"),
      ),
    ).toEqual(["epic-a", "epic-b", "epic:ungrouped", "epic-a", "epic-b"]);
    expect(sectionKeyOfCard("i-a")).toBe("epic-a");
    expect(sectionKeyOfCard("i-b")).toBe("epic:ungrouped");
    // Multi-appearance: the two-epic Todo issue renders twice in its
    // column, once per epic section.
    expect(
      document.querySelectorAll('[data-board-card="i-e"]'),
    ).toHaveLength(2);
    // …and the two-epic Done issue renders twice in its column.
    expect(
      document.querySelectorAll('[data-board-card="i-d"]'),
    ).toHaveLength(2);
  });

  it("C122 nested rows follow the parent's sub-section", async () => {
    storeSettings({
      groupBy: "state",
      subGroupBy: "priority",
      showEmptyGroups: true,
    });
    renderBoard(nestedPayload);
    await screen.findByText("Parent task");
    // Parent is High (priority 3); the nested child row renders in
    // the same High section, and the child's flat card stays hidden.
    expect(sectionKeyOfCard("i-p")).toBe("priority:3");
    const nested = document.querySelector('[data-nested-subtask="i-c"]');
    expect(nested).not.toBeNull();
    expect(
      nested
        ?.closest("[data-sub-group-section]")
        ?.getAttribute("data-sub-group-section"),
    ).toBe("priority:3");
    expect(
      document.querySelector('[data-board-card="i-c"]'),
    ).toBeNull();
  });
});

describe("board sub-group by assignees (C136T2)", () => {
  const ALICE = { id: "u-alice", name: "Alice" };
  const BOB = { id: "u-bob", name: "Bob" };

  // Todo column: Alice ×2 (one shared with Bob), Bob ×1 + shared,
  // Ungrouped ×1; Done column: Bob ×1, no Ungrouped.
  const assigneePayload = [
    makeIssue("i-a", "ENG-1", "Fix login", "s-todo", 1, {
      assignees: [ALICE],
    }),
    makeIssue("i-b", "ENG-2", "Write docs", "s-todo", 2, {
      assignees: [],
    }),
    makeIssue("i-c", "ENG-3", "Ship feature", "s-todo", 3, {
      assignees: [ALICE, BOB],
    }),
    makeIssue("i-d", "ENG-4", "Release notes", "s-done", 4, {
      assignees: [BOB],
    }),
  ];

  /** Every sub-section key holding a copy of this card (multi-
   *  appearance issues appear once per section). */
  function sectionKeysOfCard(issueId: string): string[] {
    return Array.from(
      document.querySelectorAll(`[data-board-card="${issueId}"]`),
    ).map(
      (card) =>
        card.closest("[data-sub-group-section]")?.getAttribute(
          "data-sub-group-section",
        ) ?? "",
    );
  }

  it("renders assignee sub-sections inside a column, Ungrouped last and hidden when empty", async () => {
    storeSettings({
      groupBy: "state",
      subGroupBy: "assignees",
      showEmptyGroups: true,
    });
    renderBoard(assigneePayload);
    await screen.findByText("Fix login");
    // Todo: Alice, Bob, Ungrouped (first-appearance order, Ungrouped
    // last); Done: Bob only — no Ungrouped section when empty.
    expect(
      allSections().map((s) =>
        s.getAttribute("data-sub-group-section"),
      ),
    ).toEqual(["u-alice", "u-bob", "assignee:ungrouped", "u-bob"]);
    const alice = allSections().filter(
      (s) => s.getAttribute("data-sub-group-section") === "u-alice",
    );
    expect(alice).toHaveLength(1);
    expect(sectionHeader(alice[0])).toEqual(["Alice", "2"]);
    const ungrouped = allSections().filter(
      (s) =>
        s.getAttribute("data-sub-group-section") ===
        "assignee:ungrouped",
    );
    expect(ungrouped).toHaveLength(1);
    expect(sectionHeader(ungrouped[0])).toEqual(["Ungrouped", "1"]);
    expect(sectionKeyOfCard("i-b")).toBe("assignee:ungrouped");
  });

  it("multi-appearance card copies sit in distinct sections, so their compound drag ids are distinct", async () => {
    storeSettings({
      groupBy: "state",
      subGroupBy: "assignees",
      showEmptyGroups: true,
    });
    renderBoard(assigneePayload);
    // Multi-appearance means two matching texts by design.
    expect(await screen.findAllByText("Ship feature")).toHaveLength(2);
    // The two-assignee Todo issue renders twice in its column, once
    // per assignee section. cardDragId builds the sortable id as
    // `subgroup-card:<sectionKey>:<issueId>`, so copies in distinct
    // sections carry distinct compound ids — no two draggables share
    // one id (the ids themselves live inside dnd-kit's SortableContext,
    // not the DOM, so distinct sections are the page-level proof).
    expect(
      document.querySelectorAll('[data-board-card="i-c"]'),
    ).toHaveLength(2);
    expect(sectionKeysOfCard("i-c")).toEqual(["u-alice", "u-bob"]);
    // The Done issue's single-assignee copy stays single.
    expect(
      document.querySelectorAll('[data-board-card="i-d"]'),
    ).toHaveLength(1);
  });

  it("same-dimension (primary assignees + sub-group assignees) renders flat", async () => {
    storeSettings({
      groupBy: "assignees",
      subGroupBy: "assignees",
      showEmptyGroups: true,
    });
    renderBoard(assigneePayload);
    await screen.findByText("Fix login");
    expect(allSections()).toHaveLength(0);
    expect(
      document.querySelector('[data-board-card="i-a"]'),
    ).not.toBeNull();
  });

  it("assignee sub-sections are visual only: no per-section quick-add or drop affordance", async () => {
    storeSettings({
      groupBy: "state",
      subGroupBy: "assignees",
      showEmptyGroups: true,
    });
    renderBoard(assigneePayload);
    await screen.findByText("Fix login");
    // Sub-headers are headers only (title + count spans, no tools):
    // the column header "+" is the only quick-add, and sections are
    // never droppables — BoardColumn registers exactly one droppable,
    // the column itself, so a drop resolves by primary column only
    // and never re-stamps assignees. Pointer-drag drop resolution has
    // no simulation in this repo (C129 precedent); the live browser
    // drag is the plan matrix's final verifier, as for epic drops.
    for (const section of allSections()) {
      expect(
        (section.firstElementChild as HTMLElement).querySelector(
          "button",
        ),
      ).toBeNull();
    }
  });
});

describe("board sub-group by labels (C137T2)", () => {
  const BUG = { id: "lab-bug", name: "Bug", color: "#e22718" };
  const FEATURE = { id: "lab-feature", name: "Feature", color: "#1c69d4" };

  // Todo column: Bug ×2 (one shared with Feature), Feature ×1 +
  // shared, Ungrouped ×1; Done column: Feature ×1, no Ungrouped.
  const labelPayload = [
    makeIssue("i-a", "ENG-1", "Fix login", "s-todo", 1, {
      labels: [BUG],
    }),
    makeIssue("i-b", "ENG-2", "Write docs", "s-todo", 2, {
      labels: [],
    }),
    makeIssue("i-c", "ENG-3", "Ship feature", "s-todo", 3, {
      labels: [BUG, FEATURE],
    }),
    makeIssue("i-d", "ENG-4", "Release notes", "s-done", 4, {
      labels: [FEATURE],
    }),
  ];

  /** Every sub-section key holding a copy of this card (multi-
   *  appearance issues appear once per section). */
  function sectionKeysOfCard(issueId: string): string[] {
    return Array.from(
      document.querySelectorAll(`[data-board-card="${issueId}"]`),
    ).map(
      (card) =>
        card.closest("[data-sub-group-section]")?.getAttribute(
          "data-sub-group-section",
        ) ?? "",
    );
  }

  it("renders label sub-sections inside a column, Ungrouped last and hidden when empty", async () => {
    storeSettings({
      groupBy: "state",
      subGroupBy: "labels",
      showEmptyGroups: true,
    });
    renderBoard(labelPayload);
    await screen.findByText("Fix login");
    // Todo: Bug, Feature, Ungrouped (first-appearance order,
    // Ungrouped last); Done: Feature only — no Ungrouped section
    // when empty (groupByLabel never emits an empty Ungrouped).
    expect(
      allSections().map((s) =>
        s.getAttribute("data-sub-group-section"),
      ),
    ).toEqual(["lab-bug", "lab-feature", "label:ungrouped", "lab-feature"]);
    const bug = allSections().filter(
      (s) => s.getAttribute("data-sub-group-section") === "lab-bug",
    );
    expect(bug).toHaveLength(1);
    expect(sectionHeader(bug[0])).toEqual(["Bug", "2"]);
    const ungrouped = allSections().filter(
      (s) =>
        s.getAttribute("data-sub-group-section") === "label:ungrouped",
    );
    expect(ungrouped).toHaveLength(1);
    expect(sectionHeader(ungrouped[0])).toEqual(["Ungrouped", "1"]);
    expect(sectionKeyOfCard("i-b")).toBe("label:ungrouped");
  });

  it("multi-appearance card copies sit in distinct sections, so their compound drag ids are distinct", async () => {
    storeSettings({
      groupBy: "state",
      subGroupBy: "labels",
      showEmptyGroups: true,
    });
    renderBoard(labelPayload);
    // Multi-appearance means two matching texts by design.
    expect(await screen.findAllByText("Ship feature")).toHaveLength(2);
    // The two-label Todo issue renders twice in its column, once
    // per label section. cardDragId builds the sortable id as
    // `subgroup-card:<sectionKey>:<issueId>`, so copies in distinct
    // sections carry distinct compound ids — no two draggables share
    // one id (the ids themselves live inside dnd-kit's
    // SortableContext, not the DOM, so distinct sections are the
    // page-level proof). The generic last-colon unwrap in
    // subgroupDragIssueId covers the `label:ungrouped` key too.
    expect(
      document.querySelectorAll('[data-board-card="i-c"]'),
    ).toHaveLength(2);
    expect(sectionKeysOfCard("i-c")).toEqual([
      "lab-bug",
      "lab-feature",
    ]);
    // The Done issue's single-label copy stays single.
    expect(
      document.querySelectorAll('[data-board-card="i-d"]'),
    ).toHaveLength(1);
  });

  it("same-dimension (primary labels + sub-group labels) renders flat", async () => {
    storeSettings({
      groupBy: "labels",
      subGroupBy: "labels",
      showEmptyGroups: true,
    });
    renderBoard(labelPayload);
    await screen.findByText("Fix login");
    expect(allSections()).toHaveLength(0);
    expect(
      document.querySelector('[data-board-card="i-a"]'),
    ).not.toBeNull();
  });

  it("label sub-sections are visual only: a sub-section drop resolves no column", async () => {
    storeSettings({
      groupBy: "state",
      subGroupBy: "labels",
      showEmptyGroups: true,
    });
    renderBoard(labelPayload);
    await screen.findByText("Fix login");
    // Sub-headers are headers only (title + count spans, no tools):
    // the column header "+" is the only quick-add, and sections are
    // never droppables — BoardColumn registers exactly one droppable,
    // the column itself, so a drop resolves by primary column only
    // and never re-stamps labels. Pointer-drag drop resolution has
    // no simulation in this repo (C129 precedent); the live browser
    // drag is the plan matrix's final verifier, as for epic and
    // assignee drops.
    for (const section of allSections()) {
      expect(
        (section.firstElementChild as HTMLElement).querySelector(
          "button",
        ),
      ).toBeNull();
    }
  });
});

describe("board sub-group by created-by (C138T2)", () => {
  const MEMBERS = [
    { id: "u-alice", name: "Alice", email: "alice@x.com" },
    { id: "u-bob", name: null, email: "bob@x.com" },
  ];

  // Todo column: Alice ×2, Bob ×1, Unknown ×1 (member-miss);
  // Done column: Bob ×1, no Unknown.
  const creatorPayload = [
    makeIssue("i-a", "ENG-1", "Fix login", "s-todo", 1, {
      created_by: "u-alice",
    }),
    makeIssue("i-b", "ENG-2", "Write docs", "s-todo", 2, {
      created_by: "u-gone",
    }),
    makeIssue("i-c", "ENG-3", "Ship feature", "s-todo", 3, {
      created_by: "u-bob",
    }),
    makeIssue("i-d", "ENG-4", "Release notes", "s-done", 4, {
      created_by: "u-bob",
    }),
    makeIssue("i-e", "ENG-5", "Second alice task", "s-todo", 5, {
      created_by: "u-alice",
    }),
  ];

  it("renders creator sub-sections inside a column, Unknown last and hidden when empty", async () => {
    storeSettings({
      groupBy: "state",
      subGroupBy: "createdBy",
      showEmptyGroups: true,
    });
    renderBoard(creatorPayload, MEMBERS);
    await screen.findByText("Fix login");
    // Todo: Alice, Bob, Unknown (first-appearance order, Unknown
    // last); Done: Bob only — no Unknown section when empty.
    expect(
      allSections().map((s) =>
        s.getAttribute("data-sub-group-section"),
      ),
    ).toEqual(["u-alice", "u-bob", "createdby:unknown", "u-bob"]);
    const alice = allSections().filter(
      (s) => s.getAttribute("data-sub-group-section") === "u-alice",
    );
    expect(alice).toHaveLength(1);
    expect(sectionHeader(alice[0])).toEqual(["Alice", "2"]);
    // Label = name ?? email: Bob has no name, so his email labels
    // the section — never the raw user id.
    const bob = allSections().filter(
      (s) => s.getAttribute("data-sub-group-section") === "u-bob",
    );
    expect(sectionHeader(bob[0])).toEqual(["bob@x.com", "1"]);
    const unknown = allSections().filter(
      (s) =>
        s.getAttribute("data-sub-group-section") ===
        "createdby:unknown",
    );
    expect(unknown).toHaveLength(1);
    expect(sectionHeader(unknown[0])).toEqual(["Unknown", "1"]);
    expect(sectionKeyOfCard("i-b")).toBe("createdby:unknown");
    expect(screen.queryByText("u-gone")).toBeNull();
    expect(screen.queryByText("u-alice")).toBeNull();
    expect(screen.queryByText("u-bob")).toBeNull();
  });

  it("single-appearance: every card carries a PLAIN issue id, exactly one copy per column", async () => {
    storeSettings({
      groupBy: "state",
      subGroupBy: "createdBy",
      showEmptyGroups: true,
    });
    renderBoard(creatorPayload, MEMBERS);
    await screen.findByText("Fix login");
    // Created-by is single-appearance (one creator per issue), so
    // unlike epic/assignee/label sub-groups there is exactly one
    // card copy per column and NO compound drag id branch: the
    // card's DOM id is the plain issue id, not a
    // `subgroup-card:<section>:<issue>` compound.
    for (const id of ["i-a", "i-b", "i-c", "i-d", "i-e"]) {
      expect(
        document.querySelectorAll(`[data-board-card="${id}"]`),
      ).toHaveLength(1);
    }
    expect(sectionKeyOfCard("i-a")).toBe("u-alice");
    expect(sectionKeyOfCard("i-c")).toBe("u-bob");
    expect(sectionKeyOfCard("i-d")).toBe("u-bob");
    expect(sectionKeyOfCard("i-e")).toBe("u-alice");
  });

  it("hides Unknown when every creator matches a member", async () => {
    storeSettings({
      groupBy: "state",
      subGroupBy: "createdBy",
      showEmptyGroups: true,
    });
    renderBoard(
      [makeIssue("i-a", "ENG-1", "Fix login", "s-todo", 1, {
        created_by: "u-alice",
      })],
      MEMBERS,
    );
    await screen.findByText("Fix login");
    expect(
      allSections().map((s) =>
        s.getAttribute("data-sub-group-section"),
      ),
    ).toEqual(["u-alice"]);
  });

  it("same-dimension (primary created-by + sub-group created-by) renders flat", async () => {
    storeSettings({
      groupBy: "createdBy",
      subGroupBy: "createdBy",
      showEmptyGroups: true,
    });
    renderBoard(creatorPayload, MEMBERS);
    await screen.findByText("Fix login");
    expect(allSections()).toHaveLength(0);
    expect(
      document.querySelector('[data-board-card="i-a"]'),
    ).not.toBeNull();
  });

  it("creator sub-sections are visual only: a sub-section drop resolves no column", async () => {
    storeSettings({
      groupBy: "state",
      subGroupBy: "createdBy",
      showEmptyGroups: true,
    });
    renderBoard(creatorPayload, MEMBERS);
    await screen.findByText("Fix login");
    // Sub-headers are headers only (title + count spans, no tools):
    // the column header "+" is the only quick-add, and sections are
    // never droppables — BoardColumn registers exactly one droppable,
    // the column itself, so a drop resolves by primary column only.
    // There is no creator-move pipeline to resolve: created_by is
    // immutable, so creator sub-sections carry no target fields.
    // Pointer-drag drop resolution has no simulation in this repo
    // (C129 precedent); the live browser drag is the plan matrix's
    // final verifier, as for epic, assignee, and label drops.
    for (const section of allSections()) {
      expect(
        (section.firstElementChild as HTMLElement).querySelector(
          "button",
        ),
      ).toBeNull();
    }
  });
});


describe("board sub-group by state-groups (C139T2)", () => {
  // Fixture states: s-todo → "unstarted", s-done → "completed".
  // Todo column: Unstarted ×2, Unknown ×1 (state-miss);
  // Done column: Completed ×1, no Unknown.
  const stateGroupPayload = [
    makeIssue("i-a", "ENG-1", "Fix login", "s-todo", 1),
    makeIssue("i-b", "ENG-2", "Write docs", "s-todo", 2),
    makeIssue("i-c", "ENG-3", "Ship feature", "s-gone", 3),
    makeIssue("i-d", "ENG-4", "Release notes", "s-done", 4),
  ];

  it("renders state-group sub-sections inside a column in canonical order, Unknown last", async () => {
    storeSettings({
      groupBy: "priority",
      subGroupBy: "stateGroups",
      showEmptyGroups: true,
    });
    renderBoard(stateGroupPayload);
    await screen.findByText("Fix login");
    // Helper synthesizes all six canonical groups (show-empty on),
    // in backend order, with Unknown last and only where it has
    // rows. Primary priority (not state) is the vehicle: a
    // state-miss issue still lands in its priority column, so its
    // Unknown sub-section renders — under primary state grouping
    // that issue would belong to no column at all.
    const SIX = [
      "stategroup:triage",
      "stategroup:backlog",
      "stategroup:unstarted",
      "stategroup:started",
      "stategroup:completed",
      "stategroup:cancelled",
    ];
    expect(
      allSections().map((s) =>
        s.getAttribute("data-sub-group-section"),
      ),
    ).toEqual([...SIX, "stategroup:unknown"]);
    const unstarted = allSections().filter(
      (s) =>
        s.getAttribute("data-sub-group-section") ===
        "stategroup:unstarted",
    );
    expect(unstarted).toHaveLength(1);
    expect(sectionHeader(unstarted[0])).toEqual(["Unstarted", "2"]);
    const unknown = allSections().filter(
      (s) =>
        s.getAttribute("data-sub-group-section") ===
        "stategroup:unknown",
    );
    expect(unknown).toHaveLength(1);
    expect(sectionHeader(unknown[0])).toEqual(["Unknown", "1"]);
    expect(sectionKeyOfCard("i-c")).toBe("stategroup:unknown");
    expect(sectionKeyOfCard("i-a")).toBe("stategroup:unstarted");
    expect(sectionKeyOfCard("i-d")).toBe("stategroup:completed");
    // Never a raw state id as a label.
    expect(screen.queryByText("s-todo")).toBeNull();
    expect(screen.queryByText("s-done")).toBeNull();
    expect(screen.queryByText("s-gone")).toBeNull();
  });

  it("single-appearance: every card carries a PLAIN issue id, exactly one copy per column", async () => {
    storeSettings({
      groupBy: "priority",
      subGroupBy: "stateGroups",
      showEmptyGroups: true,
    });
    renderBoard(stateGroupPayload);
    await screen.findByText("Fix login");
    // State-groups is single-appearance (one state per issue, one
    // group per state), so unlike epic/assignee/label sub-groups
    // there is exactly one card copy per column and NO compound
    // drag id branch: the card's DOM id is the plain issue id.
    for (const id of ["i-a", "i-b", "i-c", "i-d"]) {
      expect(
        document.querySelectorAll(`[data-board-card="${id}"]`),
      ).toHaveLength(1);
    }
    expect(sectionKeyOfCard("i-b")).toBe("stategroup:unstarted");
  });

  it("hides Unknown when every state resolves, even with show-empty on", async () => {
    storeSettings({
      groupBy: "state",
      subGroupBy: "stateGroups",
      showEmptyGroups: true,
    });
    renderBoard([
      makeIssue("i-a", "ENG-1", "Fix login", "s-todo", 1),
    ]);
    await screen.findByText("Fix login");
    // Six canonical sections (synthesized), no Unknown section.
    expect(
      allSections().map((s) =>
        s.getAttribute("data-sub-group-section"),
      ),
    ).toEqual([
      "stategroup:triage",
      "stategroup:backlog",
      "stategroup:unstarted",
      "stategroup:started",
      "stategroup:completed",
      "stategroup:cancelled",
    ]);
  });

  it("same-dimension (primary state-groups + sub-group state-groups) renders flat", async () => {
    storeSettings({
      groupBy: "stateGroups",
      subGroupBy: "stateGroups",
      showEmptyGroups: true,
    });
    renderBoard(stateGroupPayload);
    await screen.findByText("Fix login");
    expect(allSections()).toHaveLength(0);
    expect(
      document.querySelector('[data-board-card="i-a"]'),
    ).not.toBeNull();
  });

  it("state-group sub-sections are visual only: a sub-section drop resolves no column", async () => {
    storeSettings({
      groupBy: "state",
      subGroupBy: "stateGroups",
      showEmptyGroups: true,
    });
    renderBoard(stateGroupPayload);
    await screen.findByText("Fix login");
    // Sub-headers are headers only (title + count spans, no tools):
    // the column header "+" is the only quick-add, and sections are
    // never droppables — BoardColumn registers exactly one droppable,
    // the column itself, so a drop resolves by primary column only.
    // There is no state-group-move pipeline to resolve: a group
    // holds several states, so a cross-group drop has no single
    // target state and state-group sections carry no target fields.
    // Pointer-drag drop resolution has no simulation in this repo
    // (C129 precedent); the live browser drag is the plan matrix's
    // final verifier, as for epic, assignee, and label drops.
    for (const section of allSections()) {
      expect(
        (section.firstElementChild as HTMLElement).querySelector(
          "button",
        ),
      ).toBeNull();
    }
  });
});
