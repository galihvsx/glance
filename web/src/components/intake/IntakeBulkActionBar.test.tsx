// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import IntakeBulkActionBar from "./IntakeBulkActionBar";
import type { Member } from "../../lib/types";

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

const members: Member[] = [
  {
    id: "u-alice",
    name: "Alice",
    email: "alice@example.com",
    role: 15,
  },
  {
    id: "u-bob",
    name: "Bob",
    email: "bob@example.com",
    role: 15,
  },
];

const baseProps = {
  selectedCount: 3,
  loadedCount: 5,
  allLoadedSelected: false,
  onToggleSelectAll: () => {},
  onClearSelection: () => {},
  members: null as Member[] | null,
  onApply: () => {},
  applying: false,
  error: null as string | null,
};

describe("IntakeBulkActionBar", () => {
  it("renders the selection summary, select-all and Clear", () => {
    render(<IntakeBulkActionBar {...baseProps} />);
    expect(screen.getByText("3 selected")).toBeInTheDocument();
    expect(
      screen.getByLabelText("Select all 5 loaded items"),
    ).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Clear" })).toBeInTheDocument();
  });

  it("calls onToggleSelectAll and onClearSelection", () => {
    const onToggleSelectAll = vi.fn();
    const onClearSelection = vi.fn();
    render(
      <IntakeBulkActionBar
        {...baseProps}
        onToggleSelectAll={onToggleSelectAll}
        onClearSelection={onClearSelection}
      />,
    );
    fireEvent.click(
      screen.getByLabelText("Select all 5 loaded items"),
    );
    expect(onToggleSelectAll).toHaveBeenCalledTimes(1);
    fireEvent.click(screen.getByRole("button", { name: "Clear" }));
    expect(onClearSelection).toHaveBeenCalledTimes(1);
  });

  it("disables Apply until an action is armed, then calls onApply with the action", () => {
    const onApply = vi.fn();
    render(<IntakeBulkActionBar {...baseProps} onApply={onApply} />);

    const apply = screen.getByRole("button", { name: "Apply to 3 items" });
    expect(apply).toBeDisabled();

    fireEvent.click(screen.getByRole("button", { name: "Accept" }));
    expect(apply).toBeEnabled();
    fireEvent.click(apply);
    expect(onApply).toHaveBeenCalledTimes(1);
    expect(onApply).toHaveBeenCalledWith({ action: "accept" });
  });

  it("toggles the armed action off when clicked again", () => {
    render(<IntakeBulkActionBar {...baseProps} />);
    const accept = screen.getByRole("button", { name: "Accept" });
    fireEvent.click(accept);
    expect(accept).toHaveAttribute("aria-pressed", "true");
    fireEvent.click(accept);
    expect(accept).toHaveAttribute("aria-pressed", "false");
    expect(
      screen.getByRole("button", { name: "Apply to 3 items" }),
    ).toBeDisabled();
  });

  it("switches between Accept and Reject and applies the reject", () => {
    const onApply = vi.fn();
    render(<IntakeBulkActionBar {...baseProps} onApply={onApply} />);
    fireEvent.click(screen.getByRole("button", { name: "Accept" }));
    fireEvent.click(screen.getByRole("button", { name: "Reject" }));
    fireEvent.click(screen.getByRole("button", { name: "Apply to 3 items" }));
    expect(onApply).toHaveBeenCalledWith({ action: "reject" });
  });

  it("stages an assignee and includes assignee_id on Apply (accept only)", () => {
    const onApply = vi.fn();
    render(
      <IntakeBulkActionBar
        {...baseProps}
        members={members}
        onApply={onApply}
      />,
    );
    fireEvent.click(screen.getByRole("button", { name: "Accept" }));

    // Open the assignee popover and pick Alice.
    fireEvent.click(screen.getByRole("button", { name: /Assign/ }));
    const aliceRow = screen.getByText("Alice");
    fireEvent.click(aliceRow);

    fireEvent.click(screen.getByRole("button", { name: "Apply to 3 items" }));
    expect(onApply).toHaveBeenCalledWith({
      action: "accept",
      assignee_id: "u-alice",
    });
  });

  it("disables the assignee picker when Reject is armed (ignored server-side)", () => {
    render(<IntakeBulkActionBar {...baseProps} members={members} />);
    fireEvent.click(screen.getByRole("button", { name: "Reject" }));
    expect(screen.getByRole("button", { name: /Assign/ })).toBeDisabled();
  });

  it("renders the error in the bar", () => {
    render(<IntakeBulkActionBar {...baseProps} error="Bulk triage failed" />);
    expect(screen.getByRole("alert")).toHaveTextContent("Bulk triage failed");
  });
});
