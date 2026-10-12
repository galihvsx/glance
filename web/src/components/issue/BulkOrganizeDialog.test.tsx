// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import BulkOrganizeDialog from "./BulkOrganizeDialog";

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

const items = [
  { id: "c1", name: "Sprint 12", meta: "Active" },
  { id: "c2", name: "Sprint 13", meta: "Upcoming" },
];

function renderDialog(overrides: Partial<Parameters<typeof BulkOrganizeDialog>[0]> = {}) {
  const onConfirm = vi.fn();
  render(
    <BulkOrganizeDialog
      open
      onOpenChange={() => {}}
      title="Add to cycle"
      description="Add 2 selected issues to a cycle."
      items={items}
      loading={false}
      emptyText="No cycles."
      confirmLabel="Add 2 issues"
      onConfirm={onConfirm}
      {...overrides}
    />,
  );
  return onConfirm;
}

describe("BulkOrganizeDialog", () => {
  it("disables confirm until an item is picked, then calls onConfirm with the picked id", () => {
    const onConfirm = renderDialog();
    const confirm = screen.getByRole("button", { name: "Add 2 issues" });
    expect(confirm).toBeDisabled();

    fireEvent.click(screen.getByRole("radio", { name: /Sprint 13/ }));
    expect(confirm).not.toBeDisabled();

    fireEvent.click(confirm);
    expect(onConfirm).toHaveBeenCalledTimes(1);
    expect(onConfirm).toHaveBeenCalledWith("c2");
  });

  it("marks the picked row as checked", () => {
    renderDialog();
    const sprint12 = screen.getByRole("radio", { name: /Sprint 12/ });
    expect(sprint12).toHaveAttribute("aria-checked", "false");
    fireEvent.click(sprint12);
    expect(sprint12).toHaveAttribute("aria-checked", "true");
  });

  it("shows loading skeletons and disables confirm while loading", () => {
    renderDialog({ loading: true });
    expect(screen.getByLabelText("Loading options")).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "Add 2 issues" }),
    ).toBeDisabled();
    expect(screen.queryByRole("radiogroup")).not.toBeInTheDocument();
  });

  it("shows the empty text when there are no items", () => {
    renderDialog({ items: [], emptyText: "No cycles in this project." });
    expect(
      screen.getByText("No cycles in this project."),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "Add 2 issues" }),
    ).toBeDisabled();
  });

  it("resets the picked selection when the dialog is closed and reopened", () => {
    const onOpenChange = vi.fn();
    const { rerender } = render(
      <BulkOrganizeDialog
        open
        onOpenChange={onOpenChange}
        title="Add to cycle"
        description="Add 2 selected issues to a cycle."
        items={items}
        loading={false}
        emptyText="No cycles."
        confirmLabel="Add 2 issues"
        onConfirm={() => {}}
      />,
    );
    fireEvent.click(screen.getByRole("radio", { name: /Sprint 12/ }));
    expect(
      screen.getByRole("button", { name: "Add 2 issues" }),
    ).not.toBeDisabled();

    rerender(
      <BulkOrganizeDialog
        open={false}
        onOpenChange={onOpenChange}
        title="Add to cycle"
        description="Add 2 selected issues to a cycle."
        items={items}
        loading={false}
        emptyText="No cycles."
        confirmLabel="Add 2 issues"
        onConfirm={() => {}}
      />,
    );
    rerender(
      <BulkOrganizeDialog
        open
        onOpenChange={onOpenChange}
        title="Add to cycle"
        description="Add 2 selected issues to a cycle."
        items={items}
        loading={false}
        emptyText="No cycles."
        confirmLabel="Add 2 issues"
        onConfirm={() => {}}
      />,
    );
    expect(
      screen.getByRole("button", { name: "Add 2 issues" }),
    ).toBeDisabled();
  });

  it("cancel closes via onOpenChange", () => {
    const onOpenChange = vi.fn();
    renderDialog({ onOpenChange });
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(onOpenChange).toHaveBeenCalledWith(false);
  });
});
