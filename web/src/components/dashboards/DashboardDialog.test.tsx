// @vitest-environment jsdom
// C81T1: DashboardDialog — visibility defaults, name requirement,
// edit-mode prefill.
import { describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach } from "vitest";
import DashboardDialog from "./DashboardDialog";

afterEach(cleanup);

function renderCreate(props: Partial<Parameters<typeof DashboardDialog>[0]> = {}) {
  const onSubmit = vi.fn();
  const onOpenChange = vi.fn();
  render(
    <DashboardDialog
      open
      onOpenChange={onOpenChange}
      mode="create"
      onSubmit={onSubmit}
      {...props}
    />,
  );
  return { onSubmit, onOpenChange };
}

describe("DashboardDialog", () => {
  it("defaults visibility to public (Plane's default)", () => {
    renderCreate();
    const publicRadio = screen.getByLabelText(/Public/, { selector: "input" });
    const privateRadio = screen.getByLabelText(/Private/, { selector: "input" });
    expect((publicRadio as HTMLInputElement).checked).toBe(true);
    expect((privateRadio as HTMLInputElement).checked).toBe(false);
  });

  it("requires a name before saving", () => {
    const { onSubmit } = renderCreate();
    const save = screen.getByRole("button", { name: /Create dashboard/ });
    expect(save).toBeDisabled();
    fireEvent.click(save);
    expect(onSubmit).not.toHaveBeenCalled();

    fireEvent.change(screen.getByLabelText(/Name/), {
      target: { value: "Sprint health" },
    });
    expect(save).toBeEnabled();
    fireEvent.click(save);
    expect(onSubmit).toHaveBeenCalledWith({
      name: "Sprint health",
      description: "",
      visibility: "public",
    });
  });

  it("submits the chosen private visibility", () => {
    const { onSubmit } = renderCreate();
    fireEvent.change(screen.getByLabelText(/Name/), {
      target: { value: "Secret" },
    });
    fireEvent.click(screen.getByLabelText(/Private/, { selector: "input" }));
    fireEvent.click(screen.getByRole("button", { name: /Create dashboard/ }));
    expect(onSubmit).toHaveBeenCalledWith({
      name: "Secret",
      description: "",
      visibility: "private",
    });
  });

  it("prefills current values in edit mode", () => {
    render(
      <DashboardDialog
        open
        onOpenChange={() => {}}
        mode="edit"
        initial={{
          name: "Old name",
          description: "Old desc",
          visibility: "private",
        }}
        onSubmit={() => {}}
      />,
    );
    expect(screen.getByLabelText(/Name/)).toHaveValue("Old name");
    expect(screen.getByLabelText(/Description/)).toHaveValue("Old desc");
    expect(
      (screen.getByLabelText(/Private/, { selector: "input" }) as HTMLInputElement)
        .checked,
    ).toBe(true);
  });

  it("shows the server error when present", () => {
    renderCreate({ serverError: "Name is already taken" });
    expect(screen.getByText("Name is already taken")).toBeInTheDocument();
  });
});
