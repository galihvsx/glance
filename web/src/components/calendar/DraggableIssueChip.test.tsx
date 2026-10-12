// @vitest-environment jsdom
import { render, screen } from "@testing-library/react";
import { DndContext } from "@dnd-kit/core";
import { describe, expect, it } from "vitest";
import { DraggableIssueChip } from "./DraggableIssueChip";
import type { Issue } from "../../lib/types";

const issue: Issue = {
  id: "i1",
  project_id: "p",
  sequence_id: 1,
  display_id: "ENG-1",
  name: "Fix login redirect",
  priority: 0,
  state_id: "s",
  sort_order: 0,
  is_draft: false,
  created_by: "u",
  created_at: "2026-01-01T00:00:00Z",
  updated_at: "2026-01-01T00:00:00Z",
  assignees: [],
  labels: [],
};

describe("DraggableIssueChip", () => {
  it("renders a draggable chip with grab affordance when enabled", () => {
    render(
      <DndContext>
        <DraggableIssueChip
          issue={issue}
          onOpen={() => undefined}
          disabled={false}
        />
      </DndContext>,
    );
    const chip = screen.getByRole("button", { name: /ENG-1.*Fix login redirect/ });
    expect(chip.className).toContain("cursor-grab");
  });

  it("renders a plain non-draggable chip when canEdit is false", () => {
    render(
      <DndContext>
        <DraggableIssueChip
          issue={issue}
          onOpen={() => undefined}
          disabled
        />
      </DndContext>,
    );
    const chip = screen.getByRole("button", { name: /ENG-1.*Fix login redirect/ });
    expect(chip.className).not.toContain("cursor-grab");
    expect(chip.className).not.toContain("touch-none");
    // No dnd-kit draggable wiring leaks onto the read-only chip.
    expect(chip).not.toHaveAttribute("aria-roledescription");
  });

  it("still opens the peek drawer on click when disabled", () => {
    let opened = 0;
    render(
      <DndContext>
        <DraggableIssueChip
          issue={issue}
          onOpen={() => {
            opened += 1;
          }}
          disabled
        />
      </DndContext>,
    );
    screen.getByRole("button", { name: /ENG-1/ }).click();
    expect(opened).toBe(1);
  });
});
