// @vitest-environment jsdom
// C141T1: the nested subtask grip — present only on sortable nested
// rows, never on plain ones, and a grip click/keypress never opens
// peek (the handle stops propagation like the C126 handle) while the
// title button keeps opening it. The overlay test drives a real
// dnd-kit pointer drag far enough to pass the 6px activation
// constraint and asserts the DragOverlay shows the nested face —
// the closest jsdom gets to the live drag; the live browser drag
// remains the final verifier.
import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router-dom";
import { DndContext } from "@dnd-kit/core";
import { SortableContext } from "@dnd-kit/sortable";
import NestedListSubtaskRow, {
  SortableNestedSubtaskRow,
} from "./NestedListSubtaskRow";
import ListDragRoot from "./ListDragRoot";
import type { IssueState, SubtaskRow } from "../../lib/types";
import { makeQueryClient } from "../shell/test-utils";

const STATE: IssueState = {
  id: "s-doing",
  project_id: "p-eng",
  name: "Doing",
  group: "started",
  color: "#888",
  sequence: 2,
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
  sort_order: 1024,
};

const NESTED_ID = "nested:flat:i-p:i-c1";

function renderSortable(onOpen = vi.fn()) {
  render(
    <MemoryRouter initialEntries={["/w/acme/p/ENG"]}>
      <QueryClientProvider client={makeQueryClient()}>
        <DndContext>
          <SortableContext items={[NESTED_ID]}>
            <SortableNestedSubtaskRow
              row={CHILD}
              state={STATE}
              onOpen={onOpen}
              sortableId={NESTED_ID}
            />
          </SortableContext>
        </DndContext>
      </QueryClientProvider>
    </MemoryRouter>,
  );
  return { onOpen };
}

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

describe("nested drag grip (C141T1)", () => {
  it("renders a grip handle on a sortable nested row", () => {
    renderSortable();
    expect(
      screen.getByLabelText("Drag ENG-2 to reorder subtask"),
    ).toBeInTheDocument();
  });

  it("clicking the grip never opens peek, but the title button does", () => {
    const { onOpen } = renderSortable();
    fireEvent.click(
      screen.getByLabelText("Drag ENG-2 to reorder subtask"),
    );
    expect(onOpen).not.toHaveBeenCalled();
    fireEvent.click(screen.getByText("Child one"));
    expect(onOpen).toHaveBeenCalledWith("i-c1");
  });

  it("a keypress on the grip never opens peek (listeners stop propagation)", () => {
    const { onOpen } = renderSortable();
    fireEvent.keyDown(
      screen.getByLabelText("Drag ENG-2 to reorder subtask"),
      { key: " " },
    );
    expect(onOpen).not.toHaveBeenCalled();
  });

  it("a plain nested row (drag disabled) renders no grip and is unchanged", () => {
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
    expect(screen.getByText("Child one")).toBeInTheDocument();
    // Byte-identical disabled shape: no group class, no extra handle.
    expect(
      screen.getByText("Child one").closest("[data-nested-subtask]"),
    ).toHaveAttribute("data-nested-subtask", "i-c1");
    expect(
      screen.getByText("Child one").closest("[data-nested-subtask]")?.className,
    ).not.toContain("group/nested");
  });

  it("a nested drag shows the nested row's face in the drag overlay", async () => {
    const onDragEnd = vi.fn();
    render(
      <MemoryRouter initialEntries={["/w/acme/p/ENG"]}>
        <QueryClientProvider client={makeQueryClient()}>
          <ListDragRoot
            enabled
            onDragEnd={onDragEnd}
            overlayIssueForId={() => undefined}
            renderOverlayRow={() => null}
            overlayNestedForId={(id) =>
              id === NESTED_ID
                ? { row: CHILD, state: STATE }
                : undefined
            }
            renderOverlayNestedRow={(row) => (
              <div data-testid="nested-overlay-face">{row.title}</div>
            )}
          >
            <SortableContext items={[NESTED_ID]}>
              <SortableNestedSubtaskRow
                row={CHILD}
                state={STATE}
                onOpen={() => {}}
                sortableId={NESTED_ID}
              />
            </SortableContext>
          </ListDragRoot>
        </QueryClientProvider>
      </MemoryRouter>,
    );
    const grip = screen.getByLabelText("Drag ENG-2 to reorder subtask");
    fireEvent.pointerDown(grip, {
      button: 0,
      buttons: 1,
      pointerId: 1,
      pointerType: "mouse",
      isPrimary: true,
      clientX: 10,
      clientY: 10,
    });
    // Move events bubble from the activator to the sensor's
    // document/window listeners; 30px clears the 6px constraint.
    fireEvent.pointerMove(grip, {
      buttons: 1,
      pointerId: 1,
      pointerType: "mouse",
      isPrimary: true,
      clientX: 10,
      clientY: 40,
    });
    expect(
      await screen.findByTestId("nested-overlay-face"),
    ).toHaveTextContent("Child one");
  });
});
