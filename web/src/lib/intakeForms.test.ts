// @vitest-environment jsdom
// C82T1: intakeForms lib — field-key helpers, public-field mapping,
// submit-payload building, required checks, and URL generation.
import { describe, expect, it } from "vitest";
import {
  availableFields,
  buildSubmitValues,
  canonicalFieldLabel,
  customFieldIdFromKey,
  defaultFormFields,
  intakeFormPublicUrl,
  isCustomFieldKey,
  missingRequiredLabels,
  resolvePublicFieldType,
  toPublicFields,
  type PublicIntakeFormField,
} from "./intakeForms";
import type { CustomField } from "./customFields";

const textField: CustomField = {
  id: "cf-1",
  project_id: "p1",
  name: "Environment",
  field_type: "text",
  required: false,
  options: [],
  position: 0,
  created_at: "",
  updated_at: "",
};

const selectField: CustomField = {
  id: "cf-2",
  project_id: "p1",
  name: "Browser",
  field_type: "select",
  required: false,
  options: [{ value: "Chrome" }, { value: "Firefox" }],
  position: 1,
  created_at: "",
  updated_at: "",
};

const byId = new Map([
  [textField.id, textField],
  [selectField.id, selectField],
]);

describe("defaultFormFields", () => {
  it("starts with exactly one required title field", () => {
    expect(defaultFormFields()).toEqual([
      { key: "title", label: "Title", required: true },
    ]);
  });
});

describe("field key helpers", () => {
  it("detects custom field keys", () => {
    expect(isCustomFieldKey("custom:abc")).toBe(true);
    expect(isCustomFieldKey("title")).toBe(false);
    expect(isCustomFieldKey("priority")).toBe(false);
  });

  it("extracts the custom field id", () => {
    expect(customFieldIdFromKey("custom:abc-123")).toBe("abc-123");
  });

  it("returns canonical labels", () => {
    expect(canonicalFieldLabel("title", byId)).toBe("Title");
    expect(canonicalFieldLabel("description", byId)).toBe("Description");
    expect(canonicalFieldLabel("priority", byId)).toBe("Priority");
    expect(canonicalFieldLabel("custom:cf-1", byId)).toBe("Environment");
    expect(canonicalFieldLabel("custom:gone", byId)).toBe("custom:gone");
  });

  it("resolves public field types from keys", () => {
    expect(resolvePublicFieldType("title", byId)).toBe("title");
    expect(resolvePublicFieldType("description", byId)).toBe("description");
    expect(resolvePublicFieldType("priority", byId)).toBe("priority");
    expect(resolvePublicFieldType("custom:cf-1", byId)).toBe("text");
    expect(resolvePublicFieldType("custom:cf-2", byId)).toBe("select");
    // Deleted custom fields fall back to text (public GET drops them).
    expect(resolvePublicFieldType("custom:gone", byId)).toBe("text");
  });
});

describe("toPublicFields", () => {
  it("maps manage-view fields to the public shape with select options", () => {
    const pub = toPublicFields(
      [
        { key: "title", label: "Summary", required: true },
        { key: "priority", label: "Priority", required: false },
        { key: "custom:cf-2", label: "Browser", required: true },
      ],
      byId,
    );
    expect(pub).toEqual([
      {
        key: "title",
        label: "Summary",
        required: true,
        type: "title",
        options: [],
      },
      {
        key: "priority",
        label: "Priority",
        required: false,
        type: "priority",
        options: [],
      },
      {
        key: "custom:cf-2",
        label: "Browser",
        required: true,
        type: "select",
        options: [{ value: "Chrome", color: null }, { value: "Firefox", color: null }],
      },
    ]);
  });
});

describe("availableFields", () => {
  it("offers description, priority, then every project custom field", () => {
    const offered = availableFields([textField, selectField]);
    expect(offered.map((f) => f.key)).toEqual([
      "description",
      "priority",
      "custom:cf-1",
      "custom:cf-2",
    ]);
    expect(offered[2].fieldType).toBe("text");
    expect(offered[3].fieldType).toBe("select");
  });
});

describe("buildSubmitValues", () => {
  const fields: PublicIntakeFormField[] = [
    { key: "title", label: "Title", required: true, type: "title", options: [] },
    { key: "description", label: "Description", required: false, type: "description", options: [] },
    { key: "priority", label: "Priority", required: false, type: "priority", options: [] },
    { key: "custom:cf-1", label: "Environment", required: false, type: "text", options: [] },
    { key: "custom:cf-3", label: "Count", required: false, type: "number", options: [] },
    { key: "custom:cf-4", label: "Agreed", required: true, type: "checkbox", options: [] },
  ];

  it("keeps the checkbox boolean (false is an explicit choice)", () => {
    expect(
      buildSubmitValues(fields, { title: "T", "custom:cf-4": false }),
    ).toEqual({ title: "T", "custom:cf-4": false });
  });

  it("omits empty optional values and parses numbers/priorities", () => {
    expect(
      buildSubmitValues(fields, {
        title: "  T  ",
        description: "   ",
        priority: "2",
        "custom:cf-1": "prod",
        "custom:cf-3": "42",
        "custom:cf-4": true,
      }),
    ).toEqual({
      title: "  T  ",
      priority: 2,
      "custom:cf-1": "prod",
      "custom:cf-3": 42,
      "custom:cf-4": true,
    });
  });
});

describe("missingRequiredLabels", () => {
  const fields: PublicIntakeFormField[] = [
    { key: "title", label: "Title", required: true, type: "title", options: [] },
    { key: "description", label: "Description", required: true, type: "description", options: [] },
    { key: "custom:cf-4", label: "Agreed", required: true, type: "checkbox", options: [] },
  ];

  it("names blank required fields and never flags checkboxes", () => {
    expect(
      missingRequiredLabels(fields, { title: "", description: "x", "custom:cf-4": false }),
    ).toEqual(["Title"]);
  });

  it("returns [] when all required fields are filled", () => {
    expect(
      missingRequiredLabels(fields, {
        title: "T",
        description: "D",
        "custom:cf-4": true,
      }),
    ).toEqual([]);
  });
});

describe("intakeFormPublicUrl", () => {
  it("builds the public link from the share token", () => {
    expect(intakeFormPublicUrl("abc123")).toBe(
      `${window.location.origin}/f/abc123`,
    );
  });
});
