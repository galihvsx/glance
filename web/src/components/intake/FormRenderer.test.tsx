// @vitest-environment jsdom
// C82T1: FormRenderer — shared renderer for intake-form fields (builder
// preview + public page). Renders each field type, marks required
// fields, and reports edits through onChange.
import { describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import FormRenderer from "./FormRenderer";
import type { PublicIntakeFormField } from "../../lib/intakeForms";

const fields: PublicIntakeFormField[] = [
  { key: "title", label: "Summary", required: true, type: "title", options: [] },
  { key: "description", label: "Details", required: false, type: "description", options: [] },
  { key: "priority", label: "Priority", required: false, type: "priority", options: [] },
  { key: "custom:text", label: "Environment", required: false, type: "text", options: [] },
  { key: "custom:num", label: "Repro count", required: false, type: "number", options: [] },
  { key: "custom:date", label: "Seen on", required: false, type: "date", options: [] },
  {
    key: "custom:sel",
    label: "Browser",
    required: true,
    type: "select",
    options: [{ value: "Chrome", color: null }, { value: "Firefox", color: null }],
  },
  { key: "custom:chk", label: "Reproducible", required: false, type: "checkbox", options: [] },
];

function renderRenderer(values: Record<string, unknown> = {}) {
  const onChange = vi.fn();
  render(
    <FormRenderer fields={fields} values={values} onChange={onChange} />,
  );
  return onChange;
}

describe("FormRenderer", () => {
  it("renders one input per field with required marks", () => {
    renderRenderer();
    expect(screen.getByLabelText(/Summary/)).toBeInTheDocument();
    expect(screen.getByLabelText("Details")).toBeInTheDocument();
    expect(screen.getByLabelText("Priority")).toBeInTheDocument();
    expect(screen.getByLabelText("Environment")).toBeInTheDocument();
    expect(screen.getByLabelText("Repro count")).toBeInTheDocument();
    expect(screen.getByLabelText("Seen on")).toBeInTheDocument();
    expect(screen.getByLabelText(/Browser/)).toBeInTheDocument();
    expect(
      screen.getByRole("checkbox", { name: /Reproducible/ }),
    ).toBeInTheDocument();
    // Required marks: title + browser select.
    expect(screen.getAllByTitle("Required")).toHaveLength(2);
  });

  it("renders the priority options 0-4", () => {
    renderRenderer();
    const select = screen.getByLabelText("Priority");
    const options = Array.from(
      (select as HTMLSelectElement).options,
    ).map((o) => o.textContent);
    expect(options).toEqual([
      "Select priority…",
      "None",
      "Low",
      "Medium",
      "High",
      "Urgent",
    ]);
  });

  it("reports text and number edits through onChange", () => {
    const onChange = renderRenderer();
    fireEvent.change(screen.getByLabelText(/Summary/), {
      target: { value: "Bug!" },
    });
    expect(onChange).toHaveBeenCalledWith("title", "Bug!");
    fireEvent.change(screen.getByLabelText("Repro count"), {
      target: { value: "3" },
    });
    expect(onChange).toHaveBeenCalledWith("custom:num", "3");
  });

  it("reports priority selection as a number", () => {
    const onChange = renderRenderer();
    fireEvent.change(screen.getByLabelText("Priority"), {
      target: { value: "3" },
    });
    expect(onChange).toHaveBeenCalledWith("priority", 3);
  });

  it("reports select and checkbox edits", () => {
    const onChange = renderRenderer();
    fireEvent.change(screen.getByLabelText(/Browser/), {
      target: { value: "Firefox" },
    });
    expect(onChange).toHaveBeenCalledWith("custom:sel", "Firefox");
    fireEvent.click(screen.getByRole("checkbox", { name: /Reproducible/ }));
    expect(onChange).toHaveBeenCalledWith("custom:chk", true);
  });

  it("shows current values for controlled inputs", () => {
    renderRenderer({
      title: "Existing",
      priority: 2,
      "custom:chk": true,
    });
    expect(screen.getByLabelText(/Summary/)).toHaveValue("Existing");
    expect(screen.getByLabelText("Priority")).toHaveValue("2");
    expect(
      screen.getByRole("checkbox", { name: /Reproducible/ }),
    ).toBeChecked();
  });

  it("disables all inputs when disabled", () => {
    render(
      <FormRenderer
        fields={fields}
        values={{}}
        onChange={() => {}}
        disabled
      />,
    );
    expect(screen.getByLabelText(/Summary/)).toBeDisabled();
    expect(screen.getByLabelText("Priority")).toBeDisabled();
    expect(
      screen.getByRole("checkbox", { name: /Reproducible/ }),
    ).toHaveAttribute("aria-disabled", "true");
  });
});
