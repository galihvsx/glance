import { describe, expect, it } from "vitest";
import {
  draftToInput,
  formatCustomValue,
  formatYMD,
  resolveVisibleCustomFields,
  validateCustomValueInput,
  validateFieldDraft,
  valueToDraft,
  type CustomField,
  type FieldDraft,
} from "./customFields";

function field(partial: Partial<CustomField>): CustomField {
  return {
    id: "f1",
    project_id: "p1",
    name: "Est. hours",
    field_type: "number",
    options: [],
    required: false,
    position: 0,
    created_at: "",
    updated_at: "",
    ...partial,
  };
}

describe("validateCustomValueInput", () => {
  it("passes text through; empty text means clear (null)", () => {
    const f = field({ field_type: "text", name: "Note" });
    expect(validateCustomValueInput(f, "hello")).toEqual({
      ok: true,
      value: "hello",
    });
    expect(validateCustomValueInput(f, "   ")).toEqual({
      ok: true,
      value: null,
    });
  });

  it("accepts numbers, rejects non-numeric, empty clears", () => {
    const f = field({ field_type: "number" });
    expect(validateCustomValueInput(f, "42")).toEqual({ ok: true, value: 42 });
    expect(validateCustomValueInput(f, "3.5")).toEqual({ ok: true, value: 3.5 });
    expect(validateCustomValueInput(f, " 12 ")).toEqual({ ok: true, value: 12 });
    expect(validateCustomValueInput(f, "")).toEqual({ ok: true, value: null });
    const bad = validateCustomValueInput(f, "abc");
    expect(bad.ok).toBe(false);
    if (!bad.ok) expect(bad.error).toMatch(/must be a number/);
  });

  it("accepts YYYY-MM-DD dates, rejects bad ones, empty clears", () => {
    const f = field({ field_type: "date", name: "Due" });
    expect(validateCustomValueInput(f, "2026-10-09")).toEqual({
      ok: true,
      value: "2026-10-09",
    });
    expect(validateCustomValueInput(f, "")).toEqual({ ok: true, value: null });
    for (const bad of ["2026-13-01", "2026-02-30", "09/10/2026", "not-a-date"]) {
      const r = validateCustomValueInput(f, bad);
      expect(r.ok).toBe(false);
      if (!r.ok) expect(r.error).toMatch(/YYYY-MM-DD/);
    }
  });

  it("only accepts declared select options, empty clears", () => {
    const f = field({
      field_type: "select",
      name: "Size",
      options: [{ value: "S" }, { value: "L" }],
    });
    expect(validateCustomValueInput(f, "S")).toEqual({ ok: true, value: "S" });
    expect(validateCustomValueInput(f, "")).toEqual({ ok: true, value: null });
    const r = validateCustomValueInput(f, "XL");
    expect(r.ok).toBe(false);
    if (!r.ok) expect(r.error).toMatch(/one of its options/);
  });

  it("rejects string input for checkbox (boolean path only)", () => {
    const f = field({ field_type: "checkbox", name: "Blocked" });
    const r = validateCustomValueInput(f, "true");
    expect(r.ok).toBe(false);
    if (!r.ok) expect(r.error).toMatch(/boolean/);
  });
});

describe("valueToDraft", () => {
  it("converts stored values back to editable strings", () => {
    expect(
      valueToDraft(field({ field_type: "text" }), {
        field_id: "f1",
        name: "Note",
        field_type: "text",
        value: "hi",
      }),
    ).toBe("hi");
    expect(
      valueToDraft(field({ field_type: "number" }), {
        field_id: "f1",
        name: "N",
        field_type: "number",
        value: 7.25,
      }),
    ).toBe("7.25");
    expect(
      valueToDraft(field({ field_type: "date" }), {
        field_id: "f1",
        name: "D",
        field_type: "date",
        value: "2026-10-09",
      }),
    ).toBe("2026-10-09");
  });

  it("returns empty for unset values and checkbox", () => {
    expect(valueToDraft(field({ field_type: "text" }), undefined)).toBe("");
    expect(valueToDraft(field({ field_type: "checkbox" }), undefined)).toBe("");
  });
});

describe("formatYMD", () => {
  it("renders 2026-10-09 as 9 Oct 2026 without timezone drift", () => {
    expect(formatYMD("2026-10-09")).toBe("9 Oct 2026");
    expect(formatYMD("2027-01-01")).toBe("1 Jan 2027");
  });

  it("passes non-dates through untouched", () => {
    expect(formatYMD("tomorrow")).toBe("tomorrow");
    expect(formatYMD("")).toBe("");
  });
});

describe("formatCustomValue", () => {
  it("renders each type honestly, — when unset", () => {
    expect(formatCustomValue(field({ field_type: "text" }), undefined)).toBe(
      "—",
    );
    expect(
      formatCustomValue(field({ field_type: "checkbox", name: "B" }), {
        field_id: "f1",
        name: "B",
        field_type: "checkbox",
        value: true,
      }),
    ).toBe("Yes");
    expect(
      formatCustomValue(field({ field_type: "checkbox", name: "B" }), {
        field_id: "f1",
        name: "B",
        field_type: "checkbox",
        value: false,
      }),
    ).toBe("No");
    expect(
      formatCustomValue(field({ field_type: "date", name: "D" }), {
        field_id: "f1",
        name: "D",
        field_type: "date",
        value: "2026-10-09",
      }),
    ).toBe("9 Oct 2026");
    expect(
      formatCustomValue(field({ field_type: "number", name: "N" }), {
        field_id: "f1",
        name: "N",
        field_type: "number",
        value: 42,
      }),
    ).toBe("42");
  });
});

function draft(partial: Partial<FieldDraft>): FieldDraft {
  return {
    name: "Priority",
    field_type: "text",
    options: [],
    required: false,
    ...partial,
  };
}

describe("validateFieldDraft", () => {
  it("requires a name", () => {
    const r = validateFieldDraft(draft({ name: "  " }));
    expect(r.ok).toBe(false);
  });

  it("requires >=1 unique non-empty option for select", () => {
    expect(
      validateFieldDraft(draft({ field_type: "select", options: [] })).ok,
    ).toBe(false);
    expect(
      validateFieldDraft(
        draft({
          field_type: "select",
          options: [
            { value: "A", color: "" },
            { value: "A", color: "" },
          ],
        }),
      ).ok,
    ).toBe(false);
    expect(
      validateFieldDraft(
        draft({
          field_type: "select",
          options: [
            { value: "A", color: "" },
            { value: "B", color: "" },
          ],
        }),
      ),
    ).toEqual({ ok: true });
  });

  it("accepts non-select fields without options", () => {
    expect(validateFieldDraft(draft({ field_type: "number" }))).toEqual({
      ok: true,
    });
  });
});

describe("draftToInput", () => {
  it("trims, drops empty options, omits empty colors", () => {
    const input = draftToInput(
      draft({
        name: "  Size ",
        field_type: "select",
        options: [
          { value: " S ", color: "#ff0000" },
          { value: "  ", color: "" },
          { value: "L", color: "" },
        ],
        required: true,
      }),
    );
    expect(input.name).toBe("Size");
    expect(input.required).toBe(true);
    expect(input.options).toEqual([{ value: "S", color: "#ff0000" }, { value: "L" }]);
  });

  it("sends an empty options array for non-select types", () => {
    expect(draftToInput(draft({ field_type: "date" })).options).toEqual([]);
  });
});

describe("resolveVisibleCustomFields", () => {
  const f1 = field({ id: "f1", name: "Beta", position: 2 });
  const f2 = field({ id: "f2", name: "Alpha", position: 0 });
  const f3 = field({ id: "f3", name: "Gamma", position: 1 });

  it("defaults everything OFF: empty visibility shows no columns", () => {
    expect(resolveVisibleCustomFields([f1, f2, f3], {})).toEqual([]);
  });

  it("shows only enabled fields, ordered by position", () => {
    const out = resolveVisibleCustomFields([f1, f2, f3], {
      f1: true,
      f2: true,
    });
    expect(out.map((f) => f.id)).toEqual(["f2", "f1"]);
  });

  it("ignores unknown ids and explicit false", () => {
    const out = resolveVisibleCustomFields([f1], {
      f1: false,
      "deleted-field": true,
    });
    expect(out).toEqual([]);
  });

  it("breaks position ties by name for a stable column order", () => {
    const a = field({ id: "a", name: "Zebra", position: 0 });
    const b = field({ id: "b", name: "Apple", position: 0 });
    expect(
      resolveVisibleCustomFields([a, b], { a: true, b: true }).map(
        (f) => f.id,
      ),
    ).toEqual(["b", "a"]);
  });
});
