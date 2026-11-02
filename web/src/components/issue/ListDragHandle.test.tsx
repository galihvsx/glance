// @vitest-environment jsdom
// C126T0: the list drag grip — present only on sortable top-level rows,
// never on nested subtask rows, and clicking it never opens the peek
// drawer (the handle stops propagation like the checkbox and row-menu
// triggers) while the checkbox keeps working.
import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router-dom";
import { DndContext } from "@dnd-kit/core";
import { SortableContext } from "@dnd-kit/sortable";
import IssueRow from "./IssueRow";
import SortableIssueRow from "./SortableIssueRow";
import NestedListSubtaskRow from "./NestedListSubtaskRow";
import type { DisplayFields } from "./useDisplaySettings";
import type { Issue, IssueState, SubtaskRow } from "../../lib/types";
import { makeQueryClient } from "../shell/test-utils";

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
  sort_order: 1024,
  is_draft: false,
  created_by: "u-1",
  created_at: "",
  updated_at: "",
  assignees: [],
  labels: [],
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

const CHILD: SubtaskRow = {
  id: "i-c1",
  display_id: "ENG-2",
  title: "Child one",
  state_id: "s-doing",
  priority: 2,
  assignee_ids: [],
  sort_order: 1,
};

function renderSortable(onOpen = vi.fn(), onToggleSelect = vi.fn()) {
  render(
    <MemoryRouter initialEntries={["/w/acme/p/ENG"]}>
      <QueryClientProvider client={makeQueryClient()}>
        <DndContext>
          <SortableContext items={["i-1"]}>
            <SortableIssueRow
              issue={ISSUE}
              state={STATE}
              fields={ALL_ON}
              onOpen={onOpen}
              onToggleSelect={onToggleSelect}
            />
          </SortableContext>
        </DndContext>
      </QueryClientProvider>
    </MemoryRouter>,
  );
  return { onOpen, onToggleSelect };
}

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

describe("list drag grip (C126T0)", () => {
  it("renders a grip handle on a sortable top-level row", () => {
    renderSortable();
    expect(
      screen.getByLabelText("Drag ENG-1 to reorder"),
    ).toBeInTheDocument();
  });

  it("clicking the grip never opens the peek drawer", () => {
    const { onOpen } = renderSortable();
    fireEvent.click(screen.getByLabelText("Drag ENG-1 to reorder"));
    expect(onOpen).not.toHaveBeenCalled();
  });

  it("the checkbox still toggles selection beside the grip", () => {
    const { onToggleSelect } = renderSortable();
    fireEvent.click(screen.getByLabelText("Select issue ENG-1"));
    expect(onToggleSelect).toHaveBeenCalledTimes(1);
  });

  it("a plain IssueRow (drag disabled) renders no grip", () => {
    render(
      <MemoryRouter initialEntries={["/w/acme/p/ENG"]}>
        <QueryClientProvider client={makeQueryClient()}>
          <IssueRow
            issue={ISSUE}
            state={STATE}
            fields={ALL_ON}
            onToggleSelect={() => {}}
          />
        </QueryClientProvider>
      </MemoryRouter>,
    );
    expect(
      screen.queryByLabelText("Drag ENG-1 to reorder"),
    ).not.toBeInTheDocument();
  });

  it("a nested subtask row never carries a grip", () => {
    render(
      <MemoryRouter initialEntries={["/w/acme/p/ENG"]}>
        <QueryClientProvider client={makeQueryClient()}>
          <NestedListSubtaskRow
            row={CHILD}
            state={STATE}
            onOpen={() => {}}
          />
        </QueryClientProvider>
      </MemoryRouter>,
    );
    expect(
      screen.queryByLabelText(/to reorder/),
    ).not.toBeInTheDocument();
  });
});
