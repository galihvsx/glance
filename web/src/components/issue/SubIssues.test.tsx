// @vitest-environment jsdom
// C39T1: the Sub-issues section renders the existing
// SubtaskProgressBar (detail variant) under the header, above the child
// list, fed by issue.subtask_progress — and renders nothing extra when
// there are no sub-issues.
import { afterEach, describe, expect, it } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import { QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router-dom";
import SubIssues from "./SubIssues";
import { makeQueryClient } from "../shell/test-utils";
import type { Issue, SubtaskProgress } from "../../lib/types";

afterEach(() => {
  cleanup();
});

function makeIssue(
  overrides: Partial<Issue> = {},
): Issue {
  return {
    id: "parent-1",
    project_id: "p",
    sequence_id: 1,
    display_id: "ENG-100",
    name: "Parent",
    priority: 0,
    state_id: "st-backlog",
    sort_order: 0,
    is_draft: false,
    created_by: "u",
    created_at: "2026-01-01",
    updated_at: "2026-01-01",
    assignees: [],
    labels: [],
    children: [
      {
        uuid: "child-1",
        identifier: "ENG-101",
        title: "First child",
        state: "Completed",
      },
      {
        uuid: "child-2",
        identifier: "ENG-102",
        title: "Second child",
        state: "Backlog",
      },
    ],
    ...overrides,
  };
}

function renderSubIssues(issue: Issue) {
  const qc = makeQueryClient();
  return render(
    <MemoryRouter initialEntries={["/w/acme/p/ENG"]}>
      <QueryClientProvider client={qc}>
        <SubIssues
          slug="acme"
          identifier="ENG"
          issue={issue}
          issueKey={["issue", "acme", "ENG", issue.id]}
          onError={() => {}}
        />
      </QueryClientProvider>
    </MemoryRouter>,
  );
}

function progress(total: number, done: number): SubtaskProgress {
  return { total, done };
}

describe("SubIssues progress bar (C39T1)", () => {
  it("renders the detail bar with done/total above the child list", () => {
    const { container } = renderSubIssues(
      makeIssue({ subtask_progress: progress(5, 3) }),
    );
    expect(screen.getByText("3/5")).toBeTruthy();
    expect(
      screen.getByTitle("3 of 5 subtasks done"),
    ).toBeTruthy();
    // Bar sits between the header and the child list: h2, bar, ul, add button.
    const section = container.querySelector('section[aria-label="Sub-work items"]');
    expect(section).not.toBeNull();
    expect(section!.children[0].tagName).toBe("H2");
    expect(section!.children[1].textContent).toContain("3/5");
    expect(section!.children[2].tagName).toBe("UL");
  });

  it("renders no bar when subtask_progress is missing", () => {
    renderSubIssues(makeIssue({ subtask_progress: undefined }));
    expect(screen.queryByTitle(/subtasks done/)).toBeNull();
    // Empty-state copy is unchanged.
    expect(screen.getByText("ENG-101")).toBeTruthy();
  });

  it("renders no bar when total is zero", () => {
    renderSubIssues(
      makeIssue({
        children: [],
        subtask_progress: progress(0, 0),
      }),
    );
    expect(screen.queryByTitle(/subtasks done/)).toBeNull();
    expect(
      screen.getByText("No sub-work items yet. Add one below."),
    ).toBeTruthy();
  });
});
