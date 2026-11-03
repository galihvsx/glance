// @vitest-environment jsdom
// C127T0: the header drop zone renders the header's own div (no wrapper
// element) and swaps only the border-color token when dragged over —
// the board column's isOver token (border-accent-blue/70), idle = the
// same hairline the plain header uses.
import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, render } from "@testing-library/react";
import ListHeaderDropZone from "./ListHeaderDropZone";

const mockState = vi.hoisted(() => ({ isOver: false }));

vi.mock("@dnd-kit/core", () => ({
  useDroppable: () => ({ setNodeRef: vi.fn(), isOver: mockState.isOver }),
}));

afterEach(() => {
  cleanup();
  mockState.isOver = false;
});

describe("ListHeaderDropZone", () => {
  it("renders its children in a single div, no wrapper element", () => {
    const { container } = render(
      <ListHeaderDropZone
        droppableId="header:s-doing:state:s-doing"
        className="flex h-[35px] items-center gap-2 border-b"
      >
        <h2>Doing</h2>
      </ListHeaderDropZone>,
    );
    expect(container.children).toHaveLength(1);
    expect(container.firstChild?.nodeName).toBe("DIV");
    expect(container.textContent).toContain("Doing");
  });

  it("idle: the border token is the plain hairline", () => {
    mockState.isOver = false;
    const { container } = render(
      <ListHeaderDropZone
        droppableId="header:s-doing:state:s-doing"
        className="flex h-[35px] items-center gap-2 border-b"
      >
        <h2>Doing</h2>
      </ListHeaderDropZone>,
    );
    const div = container.firstChild as HTMLElement;
    expect(div.className).toContain("border-border-hairline");
    expect(div.className).not.toContain("border-accent-blue/70");
  });

  it("dragged over: the border token swaps to the board highlight", () => {
    mockState.isOver = true;
    const { container } = render(
      <ListHeaderDropZone
        droppableId="header:s-doing:state:s-doing"
        className="flex h-[35px] items-center gap-2 border-b"
      >
        <h2>Doing</h2>
      </ListHeaderDropZone>,
    );
    const div = container.firstChild as HTMLElement;
    expect(div.className).toContain("border-accent-blue/70");
    expect(div.className).not.toContain("border-border-hairline");
  });
});
