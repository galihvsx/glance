// @vitest-environment jsdom
import { fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import BlockerConfirmDialog from "./BlockerConfirmDialog";

afterEach(() => {
  vi.clearAllMocks();
});

function setup(blockers: string[], pending = false) {
  const onCancel = vi.fn();
  const onConfirm = vi.fn();
  render(
    <BlockerConfirmDialog
      open
      blockers={blockers}
      pending={pending}
      onCancel={onCancel}
      onConfirm={onConfirm}
    />,
  );
  return { onCancel, onConfirm };
}

describe("BlockerConfirmDialog", () => {
  it("lists every blocker display ID", () => {
    setup(["ENG-12", "ENG-34"]);
    expect(screen.getByText(/ENG-12/)).toBeInTheDocument();
    expect(screen.getByText(/ENG-34/)).toBeInTheDocument();
  });

  it("confirms with 'Complete anyway'", () => {
    const { onConfirm } = setup(["ENG-12"]);
    fireEvent.click(
      screen.getByRole("button", { name: "Complete anyway" }),
    );
    expect(onConfirm).toHaveBeenCalledTimes(1);
  });

  it("cancels", () => {
    const { onCancel } = setup(["ENG-12"]);
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(onCancel).toHaveBeenCalledTimes(1);
  });

  it("disables the confirm button while the retry is pending", () => {
    setup(["ENG-12"], true);
    expect(
      screen.getByRole("button", { name: "Complete anyway" }),
    ).toBeDisabled();
  });
});
