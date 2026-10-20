// @vitest-environment jsdom
// C82T1: FormBuilderDialog — create/edit builder for intake forms: title
// locked/required, available-fields picker, required toggles, remove
// buttons, and the live preview through the shared FormRenderer.
import { describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, within } from "@testing-library/react";
import FormBuilderDialog from "./FormBuilderDialog";
import type { IntakeForm } from "../../lib/intakeForms";
import type { CustomField } from "../../lib/customFields";

vi.mock("../../lib/api", () => ({
  api: { post: vi.fn(), patch: vi.fn() },
  ApiError: class ApiError extends Error {},
}));

const customFields: CustomField[] = [
  {
    id: "cf-1",
    project_id: "p1",
    name: "Environment",
    field_type: "text",
    required: false,
    options: [],
    position: 0,
    created_at: "",
    updated_at: "",
  },
  {
    id: "cf-2",
    project_id: "p1",
    name: "Browser",
    field_type: "select",
    required: false,
    options: [{ value: "Chrome" }, { value: "Firefox" }],
    position: 1,
    created_at: "",
    updated_at: "",
  },
];

function renderDialog(form: IntakeForm | null = null) {
  const onOpenChange = vi.fn();
  const onSaved = vi.fn();
  render(
    <FormBuilderDialog
      open
      onOpenChange={onOpenChange}
      slug="acme"
      identifier="ENG"
      form={form}
      customFields={customFields}
      onSaved={onSaved}
    />,
  );
  return { onOpenChange, onSaved };
}

/** The builder row for the field whose display label is `label`. */
function rowFor(label: string) {
  const input = screen.getByDisplayValue(label);
  const row = input.closest("[data-field-row]");
  if (!row) throw new Error(`no builder row for label ${label}`);
  return within(row as HTMLElement);
}

describe("FormBuilderDialog", () => {
  it("create mode: title is auto-included, locked, and required", () => {
    renderDialog();
    expect(screen.getByText("required · locked")).toBeInTheDocument();
    const titleRow = rowFor("Title");
    // The title row's remove button is disabled (title not removable).
    expect(
      titleRow.getByRole("button", { name: "Remove Title" }),
    ).toBeDisabled();
    // The title's required switch is locked on.
    const titleSwitch = titleRow.getByRole("switch");
    expect(titleSwitch).toHaveAttribute("aria-disabled", "true");
    expect(titleSwitch).toBeChecked();
  });

  it("offers description, priority, and custom fields in the picker", () => {
    renderDialog();
    expect(
      screen.getByRole("button", { name: "Add Description to the form" }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "Add Priority to the form" }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "Add Environment to the form" }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "Add Browser to the form" }),
    ).toBeInTheDocument();
  });

  it("adds a picked field to the form fields column", () => {
    renderDialog();
    fireEvent.click(
      screen.getByRole("button", { name: "Add Description to the form" }),
    );
    expect(rowFor("Description").getByRole("switch")).not.toBeChecked();
    expect(
      rowFor("Description").getByRole("button", { name: "Remove Description" }),
    ).not.toBeDisabled();
    // Added → the picker shows "Added" instead of an add button.
    expect(
      screen.queryByRole("button", { name: "Add Description to the form" }),
    ).not.toBeInTheDocument();
  });

  it("toggles required for non-title fields and removes them", () => {
    renderDialog();
    fireEvent.click(
      screen.getByRole("button", { name: "Add Priority to the form" }),
    );
    const priorityRow = rowFor("Priority");
    const required = priorityRow.getByRole("switch");
    fireEvent.click(required);
    expect(required).toBeChecked();
    fireEvent.click(
      priorityRow.getByRole("button", { name: "Remove Priority" }),
    );
    expect(screen.queryByDisplayValue("Priority")).not.toBeInTheDocument();
  });

  it("live preview renders the shared FormRenderer inputs", () => {
    renderDialog();
    fireEvent.click(screen.getByRole("switch", { name: "Live preview" }));
    // The preview renders the title input (placeholder "Title" — the
    // builder's own rows have no placeholders, so this is unique).
    expect(screen.getByPlaceholderText("Title")).toBeInTheDocument();
  });

  it("edit mode prefills name, description, enabled, and fields", () => {
    renderDialog({
      id: "f-1",
      project_id: "p1",
      intake_id: "i1",
      name: "Bug report",
      description: "Report bugs",
      is_enabled: false,
      share_token: "tok",
      fields: [
        { key: "title", label: "Summary", required: true },
        { key: "priority", label: "Priority", required: false },
      ],
      created_by: "u1",
      created_at: "",
      updated_at: "",
    });
    expect(screen.getByLabelText("Name")).toHaveValue("Bug report");
    expect(screen.getByLabelText("Description")).toHaveValue("Report bugs");
    expect(
      screen.getByRole("switch", { name: /Enabled/ }),
    ).not.toBeChecked();
    expect(rowFor("Summary").getByRole("switch")).toHaveAttribute(
      "aria-disabled",
      "true",
    );
    expect(rowFor("Priority").getByRole("switch")).not.toBeChecked();
    expect(
      screen.getByRole("button", { name: /Save changes/ }),
    ).toBeInTheDocument();
  });

  it("shows a validation error when the name is empty", () => {
    renderDialog();
    fireEvent.click(screen.getByRole("button", { name: "Create form" }));
    expect(screen.getByText("Give the form a name.")).toBeInTheDocument();
  });
});
