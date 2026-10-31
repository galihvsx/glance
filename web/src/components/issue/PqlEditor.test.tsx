// @vitest-environment jsdom
// PqlEditor save-hint prop (C96T1): the "Views → Save current view"
// hint is shown by default (the Issues page, where that save path
// exists) and hidden when showSaveHint is false (Board, cycle
// detail, module detail — no Views save path for PQL there).
// Hiding the hint must not remove anything else from the editor.

import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import PqlEditor from "./PqlEditor";

function renderEditor(showSaveHint?: boolean) {
  render(
    <PqlEditor
      appliedQuery=""
      onApply={vi.fn().mockResolvedValue(undefined)}
      onClear={vi.fn()}
      {...(showSaveHint === undefined ? {} : { showSaveHint })}
    />,
  );
}

afterEach(() => {
  cleanup();
});

describe("PqlEditor save hint (C96T1)", () => {
  it("shows the save hint by default", () => {
    renderEditor();
    expect(
      screen.getByText(/To save this query, use Views/),
    ).toBeInTheDocument();
  });

  it("shows the save hint when showSaveHint is true", () => {
    renderEditor(true);
    expect(
      screen.getByText(/To save this query, use Views/),
    ).toBeInTheDocument();
  });

  it("hides the save hint when showSaveHint is false, keeping the rest of the editor", () => {
    renderEditor(false);
    expect(screen.queryByText(/To save this query, use Views/)).toBeNull();
    // Everything else survives: editor, Apply/Clear, cheat-sheet.
    expect(screen.getByLabelText("PQL query")).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "Apply" }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "Clear" }),
    ).toBeInTheDocument();
    expect(screen.getByText("Fields & operators")).toBeInTheDocument();
  });
});
