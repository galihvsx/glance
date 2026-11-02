// @vitest-environment jsdom
// C121T1: IssueRow is the dense 47px list row — of the 8 new fields it
// carries ONLY `id` (the one with a column). The subtask progress bar,
// structure chips and count rows have no slot in the fixed single-line
// grid (the labels/assignees/dates precedent), so they render nothing
// here and stay on IssueCard / board cards. The ID text hides but
// aria-labels keep the ID.

import { render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, it } from "vitest";
import IssueRow from "./IssueRow";
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

const ISSUE: Issue = {
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
  subtask_progress: { total: 4, done: 1 },
  project_structure: {
    modules: [{ id: "m-1", name: "Auth" }],
    cycle: { id: "c-1", name: "Sprint 3" },
    release: { id: "r-1", name: "v2.1" },
  },
  attachment_count: 2,
  link_count: 1,
  relation_count: 3,
};

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

function renderRow(fields: DisplayFields = ALL_ON) {
  render(
    <MemoryRouter initialEntries={["/w/acme/p/ENG"]}>
      <IssueRow
        issue={ISSUE}
        state={STATE}
        fields={fields}
        slug="acme"
        identifier="ENG"
        onToggleSelect={() => {}}
      />
    </MemoryRouter>,
  );
}

describe("IssueRow (C121T1)", () => {
  it("shows the ID column when the id toggle is on", () => {
    renderRow();
    expect(screen.getByText("ENG-1")).toBeInTheDocument();
  });

  it("hides the ID text when the id toggle is off but keeps it in aria-labels", () => {
    renderRow({ ...ALL_ON, id: false });
    expect(screen.queryByText("ENG-1")).not.toBeInTheDocument();
    expect(
      screen.getByRole("link", { name: "Open issue ENG-1: Fix login redirect" }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("checkbox", { name: "Select issue ENG-1" }),
    ).toBeInTheDocument();
  });

  it("renders no card rows for the other new fields even when present and enabled", () => {
    renderRow();
    // No subtask bar, no structure chips, no count rows on the dense row.
    expect(screen.queryByRole("progressbar")).not.toBeInTheDocument();
    expect(screen.queryByText("Auth")).not.toBeInTheDocument();
    expect(screen.queryByText("Sprint 3")).not.toBeInTheDocument();
    expect(screen.queryByText("v2.1")).not.toBeInTheDocument();
    expect(screen.queryByLabelText(/attachments/)).not.toBeInTheDocument();
    expect(screen.queryByLabelText(/links?/)).not.toBeInTheDocument();
    expect(screen.queryByLabelText(/dependenc/)).not.toBeInTheDocument();
  });
});
