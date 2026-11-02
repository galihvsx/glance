// @vitest-environment jsdom
// C121T1: IssueCard gates every new row by its toggle — renders when
// enabled + present, hidden when toggled off, absent when the count is
// 0 / the structure is undefined. The ID badge hides but aria-labels
// keep the ID.

import { render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, it } from "vitest";
import IssueCard from "./IssueCard";
import type { DisplayFields } from "./useDisplaySettings";
import type { Issue, IssueState } from "../../lib/types";

const ALL_ON: DisplayFields = {
  state: true,
  priority: true,
  labels: true,
  assignees: true,
  dates: true,
  id: true,
  subitems: true,
  modules: true,
  cycle: true,
  release: true,
  attachments: true,
  links: true,
  dependencies: true,
};

function makeIssue(overrides: Partial<Issue> = {}): Issue {
  return {
    id: "i-1",
    project_id: "p-eng",
    sequence_id: 1,
    display_id: "ENG-1",
    name: "Fix login redirect",
    priority: 3,
    state_id: "s-todo",
    sort_order: 1,
    is_draft: false,
    created_by: "u-1",
    created_at: "",
    updated_at: "",
    assignees: [],
    labels: [],
    start_date: "2026-11-01",
    target_date: "2026-11-05",
    subtask_progress: { total: 4, done: 1 },
    project_structure: {
      modules: [
        { id: "m-1", name: "Auth" },
        { id: "m-2", name: "Checkout" },
      ],
      cycle: { id: "c-1", name: "Sprint 3" },
      release: { id: "r-1", name: "v2.1" },
    },
    attachment_count: 2,
    link_count: 1,
    relation_count: 3,
    ...overrides,
  };
}

const STATE: IssueState = {
  id: "s-todo",
  project_id: "p-eng",
  name: "Todo",
  group: "unstarted",
  color: "#888",
  sequence: 1,
  created_at: "",
  updated_at: "",
};

function renderCard(issue: Issue, fields: DisplayFields = ALL_ON) {
  render(
    <MemoryRouter initialEntries={["/w/acme/p/ENG"]}>
      <IssueCard
        issue={issue}
        state={STATE}
        fields={fields}
        slug="acme"
        identifier="ENG"
      />
    </MemoryRouter>,
  );
}

describe("IssueCard (C121T1)", () => {
  it("renders every new row when enabled and present", () => {
    renderCard(makeIssue());
    expect(screen.getByText("ENG-1")).toBeInTheDocument();
    expect(
      screen.getByRole("progressbar", { name: "Subtasks: 1 of 4 done" }),
    ).toBeInTheDocument();
    expect(screen.getByLabelText("Module: Auth")).toBeInTheDocument();
    expect(screen.getByLabelText("Module: Checkout")).toBeInTheDocument();
    expect(screen.getByLabelText("Cycle: Sprint 3")).toBeInTheDocument();
    expect(screen.getByLabelText("Release: v2.1")).toBeInTheDocument();
    expect(screen.getByLabelText("2 attachments")).toBeInTheDocument();
    expect(screen.getByLabelText("1 link")).toBeInTheDocument();
    expect(screen.getByLabelText("3 dependencies")).toBeInTheDocument();
  });

  it("hides each new row when its toggle is off", () => {
    const off: DisplayFields = {
      ...ALL_ON,
      id: false,
      subitems: false,
      modules: false,
      cycle: false,
      release: false,
      attachments: false,
      links: false,
      dependencies: false,
    };
    renderCard(makeIssue(), off);
    expect(screen.queryByText("ENG-1")).not.toBeInTheDocument();
    expect(
      screen.queryByRole("progressbar", { name: "Subtasks: 1 of 4 done" }),
    ).not.toBeInTheDocument();
    expect(screen.queryByLabelText("Module: Auth")).not.toBeInTheDocument();
    expect(screen.queryByLabelText("Cycle: Sprint 3")).not.toBeInTheDocument();
    expect(screen.queryByLabelText("Release: v2.1")).not.toBeInTheDocument();
    expect(screen.queryByLabelText("2 attachments")).not.toBeInTheDocument();
    expect(screen.queryByLabelText("1 link")).not.toBeInTheDocument();
    expect(screen.queryByLabelText("3 dependencies")).not.toBeInTheDocument();
  });

  it("keeps the ID in aria-labels when the badge is hidden", () => {
    renderCard(makeIssue(), { ...ALL_ON, id: false });
    expect(screen.queryByText("ENG-1")).not.toBeInTheDocument();
    expect(
      screen.getByRole("link", { name: "Open issue ENG-1: Fix login redirect" }),
    ).toBeInTheDocument();
  });

  it("renders no count rows when counts are 0, and no structure chips when structure is undefined", () => {
    renderCard(
      makeIssue({
        attachment_count: 0,
        link_count: 0,
        relation_count: 0,
        project_structure: undefined,
        subtask_progress: { total: 0, done: 0 },
      }),
    );
    expect(screen.queryByLabelText(/attachments/)).not.toBeInTheDocument();
    expect(screen.queryByLabelText(/links/)).not.toBeInTheDocument();
    expect(screen.queryByLabelText(/dependencies/)).not.toBeInTheDocument();
    expect(screen.queryByLabelText("Module: Auth")).not.toBeInTheDocument();
    expect(screen.queryByLabelText("Cycle: Sprint 3")).not.toBeInTheDocument();
    expect(screen.queryByLabelText("Release: v2.1")).not.toBeInTheDocument();
    expect(screen.queryByRole("progressbar")).not.toBeInTheDocument();
  });

  it("renders no count rows when counts are undefined (not computed on this fetch)", () => {
    const { attachment_count: _a, link_count: _l, relation_count: _r, ...rest } =
      makeIssue();
    void _a;
    void _l;
    void _r;
    renderCard(rest as Issue);
    expect(screen.queryByLabelText(/attachments/)).not.toBeInTheDocument();
    expect(screen.queryByLabelText(/links/)).not.toBeInTheDocument();
    expect(screen.queryByLabelText(/dependencies/)).not.toBeInTheDocument();
  });
});
