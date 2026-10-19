// projectLabelFilters tests (C76T1): URL schema for the Projects page
// label filter — parse/serialize round-trip, OR semantics preserved,
// malformed pasted params dropped (never crash, never 400 the list),
// and the useProjectLabelFilters hook's URL behavior.
// @vitest-environment jsdom
import { describe, expect, it } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import { MemoryRouter, useLocation } from "react-router-dom";
import {
  isProjectLabelId,
  parseProjectLabelIds,
  projectLabelApiParams,
  serializeProjectLabelIds,
  useProjectLabelFilters,
} from "./projectLabelFilters";

const A = "11111111-1111-1111-1111-111111111111";
const B = "22222222-2222-2222-2222-222222222222";
const C = "33333333-3333-3333-3333-333333333333";

describe("isProjectLabelId", () => {
  it("accepts well-formed UUIDs", () => {
    expect(isProjectLabelId(A)).toBe(true);
    expect(isProjectLabelId(A.toUpperCase())).toBe(true);
  });
  it("rejects garbage", () => {
    expect(isProjectLabelId("")).toBe(false);
    expect(isProjectLabelId("none")).toBe(false);
    expect(isProjectLabelId("not-a-uuid")).toBe(false);
    expect(isProjectLabelId("11111111-1111-1111-1111")).toBe(false);
  });
});

describe("parseProjectLabelIds", () => {
  it("parses repeatable label= params", () => {
    const p = new URLSearchParams();
    p.append("label", A);
    p.append("label", B);
    expect(parseProjectLabelIds(p)).toEqual([A, B]);
  });
  it("parses comma-separated values in one param", () => {
    const p = new URLSearchParams(`label=${A},${B}`);
    expect(parseProjectLabelIds(p)).toEqual([A, B]);
  });
  it("drops malformed values from a pasted URL", () => {
    const p = new URLSearchParams(`label=${A}&label=garbage&label=&label=none&label=${B},bogus`);
    expect(parseProjectLabelIds(p)).toEqual([A, B]);
  });
  it("dedupes while keeping first-seen order", () => {
    const p = new URLSearchParams(`label=${B}&label=${A}&label=${B}`);
    expect(parseProjectLabelIds(p)).toEqual([B, A]);
  });
  it("returns [] for an empty query", () => {
    expect(parseProjectLabelIds(new URLSearchParams())).toEqual([]);
  });
});

describe("serializeProjectLabelIds", () => {
  it("round-trips through parse", () => {
    const ids = [A, B, C];
    expect(parseProjectLabelIds(serializeProjectLabelIds(ids))).toEqual(ids);
  });
  it("produces no params for an empty selection", () => {
    expect(serializeProjectLabelIds([]).toString()).toBe("");
  });
  it("never serializes malformed ids", () => {
    expect(serializeProjectLabelIds([A, "bogus"]).toString()).toBe(
      `label=${encodeURIComponent(A)}`,
    );
  });
});

describe("projectLabelApiParams", () => {
  it("builds repeatable ?label= for the list API", () => {
    expect(projectLabelApiParams([A, B])).toBe(
      `label=${A}&label=${B}`,
    );
  });
  it("is empty when nothing is selected", () => {
    expect(projectLabelApiParams([])).toBe("");
  });
});

describe("useProjectLabelFilters", () => {
  function Harness() {
    const [ids, setIds, clearIds] = useProjectLabelFilters();
    const l = useLocation();
    return (
      <div>
        <div data-testid="ids">{ids.join(",")}</div>
        <div data-testid="loc">
          {l.pathname}
          {l.search}
        </div>
        <button type="button" onClick={() => setIds([A, B])}>
          set
        </button>
        <button type="button" onClick={clearIds}>
          clear
        </button>
      </div>
    );
  }

  function renderUrl(url: string) {
    render(
      <MemoryRouter initialEntries={[url]}>
        <Harness />
      </MemoryRouter>,
    );
  }

  it("reads the selection from the URL and drops malformed params", () => {
    renderUrl(`/w/acme/projects?label=${A}&label=nope`);
    expect(screen.getByTestId("ids")).toHaveTextContent(A);
  });

  it("writes the selection back to the URL", () => {
    renderUrl(`/w/acme/projects`);
    fireEvent.click(screen.getByText("set"));
    expect(screen.getByTestId("ids")).toHaveTextContent(`${A},${B}`);
    expect(screen.getByTestId("loc")).toHaveTextContent(
      `/w/acme/projects?label=${A}&label=${B}`,
    );
  });

  it("clear keeps non-label params", () => {
    renderUrl(`/w/acme/projects?label=${A}&tab=x`);
    fireEvent.click(screen.getByText("clear"));
    expect(screen.getByTestId("ids")).toHaveTextContent("");
    expect(screen.getByTestId("loc")).toHaveTextContent(
      "/w/acme/projects?tab=x",
    );
  });

  it("set preserves non-label params", () => {
    renderUrl(`/w/acme/projects?tab=x`);
    fireEvent.click(screen.getByText("set"));
    const loc = screen.getByTestId("loc").textContent ?? "";
    expect(loc).toContain("tab=x");
    expect(loc).toContain(`label=${A}`);
    expect(loc).toContain(`label=${B}`);
  });
});
