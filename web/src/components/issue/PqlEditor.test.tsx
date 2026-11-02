// @vitest-environment jsdom
// PqlEditor save-hint prop (C96T1): the "Views → Save current view"
// hint is shown by default (the Issues page, where that save path
// exists) and hidden when showSaveHint is false (Board — its Views
// menu saves visual filters only). Hiding the hint must not remove
// anything else from the editor.
// C108T1: saveHintText customizes the hint wording (cycle/module
// detail editors point at the All issues / PQL toggle, not the page
// header); the default stays the Issues-page wording byte-for-byte.

import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import PqlEditor from "./PqlEditor";

const DEFAULT_HINT =
  "To save this query, use Views → Save current view in the page header; it re-applies when the view is opened.";
const DETAIL_HINT =
  "To save this query, use Views → Save current view next to the All issues / PQL toggle; it re-applies when the view is opened.";

function renderEditor(opts: {
  showSaveHint?: boolean;
  saveHintText?: string;
} = {}) {
  render(
    <PqlEditor
      appliedQuery=""
      onApply={vi.fn().mockResolvedValue(undefined)}
      onClear={vi.fn()}
      {...opts}
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
    renderEditor({ showSaveHint: true });
    expect(
      screen.getByText(/To save this query, use Views/),
    ).toBeInTheDocument();
  });

  it("hides the save hint when showSaveHint is false, keeping the rest of the editor", () => {
    renderEditor({ showSaveHint: false });
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

describe("PqlEditor project cheat-sheet row (C114T1)", () => {
  it("renders the project row with its workspace-only meaning", () => {
    renderEditor();
    expect(screen.getByText("project = ABC")).toBeInTheDocument();
    expect(
      screen.getByText(
        "Project by key or name (workspace lists like MyWork only)",
      ),
    ).toBeInTheDocument();
  });
});

describe("PqlEditor custom save hint (C108T1)", () => {
  it("renders the default text byte-for-byte when no hint is passed", () => {
    renderEditor();
    expect(screen.getByText(DEFAULT_HINT)).toBeInTheDocument();
  });

  it("renders custom hint text when passed", () => {
    renderEditor({ showSaveHint: true, saveHintText: DETAIL_HINT });
    expect(screen.getByText(DETAIL_HINT)).toBeInTheDocument();
    expect(screen.queryByText(DEFAULT_HINT)).toBeNull();
  });

  it("hides a custom hint when showSaveHint is false", () => {
    renderEditor({ showSaveHint: false, saveHintText: DETAIL_HINT });
    expect(screen.queryByText(/To save this query, use Views/)).toBeNull();
  });
});
