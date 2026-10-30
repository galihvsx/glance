// @vitest-environment jsdom
// C91T1: FilterPanel PQL mode — mode toggle, editor, Apply/Clear,
// inline error display, and the visual/PQL exclusivity of the panel.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { QueryClientProvider } from "@tanstack/react-query";
import FilterPanel from "./FilterPanel";
import { ApiError } from "../../lib/api";
import { EMPTY_FILTERS, type IssueFilters } from "../../lib/filters";
import {
  makeQueryClient,
  stubMatchMedia,
} from "../shell/test-utils";

function renderPanel(opts: {
  filters?: IssueFilters;
  pqlMode?: boolean;
  pqlAppliedQuery?: string;
  onPqlModeChange?: (mode: "visual" | "pql") => void;
  onPqlApply?: (query: string) => Promise<void>;
  onPqlClear?: () => void;
}) {
  const filters = opts.filters ?? EMPTY_FILTERS;
  const onChange = vi.fn();
  const onClear = vi.fn();
  const onPqlModeChange = opts.onPqlModeChange ?? vi.fn();
  const onPqlApply = opts.onPqlApply ?? vi.fn().mockResolvedValue(undefined);
  const onPqlClear = opts.onPqlClear ?? vi.fn();
  const qc = makeQueryClient([
    [["states", "acme", "ENG"], []],
    [["members", "acme"], []],
    [["labels", "acme", "ENG"], []],
    [["estimates", "acme", "ENG"], []],
    [["epics", "acme", "ENG", "status=active&status=archived"], []],
    [["parent-filter-search", "acme", "ENG", ""], []],
  ]);
  render(
    <QueryClientProvider client={qc}>
      <FilterPanel
        slug="acme"
        identifier="ENG"
        filters={filters}
        onChange={onChange}
        onClear={onClear}
        pqlMode={opts.pqlMode}
        pqlAppliedQuery={opts.pqlAppliedQuery}
        onPqlModeChange={onPqlModeChange}
        onPqlApply={onPqlApply}
        onPqlClear={onPqlClear}
      />
    </QueryClientProvider>,
  );
  fireEvent.click(screen.getByRole("button", { name: /^filter/i }));
  return { onChange, onClear, onPqlModeChange, onPqlApply, onPqlClear };
}

beforeEach(() => {
  stubMatchMedia(false);
});

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  vi.useRealTimers();
});

describe("FilterPanel PQL mode (C91T1)", () => {
  it("shows no mode toggle when the page does not wire PQL", () => {
    const qc = makeQueryClient([
      [["states", "acme", "ENG"], []],
      [["members", "acme"], []],
      [["labels", "acme", "ENG"], []],
      [["estimates", "acme", "ENG"], []],
      [["epics", "acme", "ENG", "status=active&status=archived"], []],
      [["parent-filter-search", "acme", "ENG", ""], []],
    ]);
    render(
      <QueryClientProvider client={qc}>
        <FilterPanel
          slug="acme"
          identifier="ENG"
          filters={EMPTY_FILTERS}
          onChange={vi.fn()}
          onClear={vi.fn()}
        />
      </QueryClientProvider>,
    );
    fireEvent.click(screen.getByRole("button", { name: /^filter/i }));
    expect(screen.queryByLabelText("Filter mode")).toBeNull();
    expect(screen.queryByLabelText("PQL query")).toBeNull();
  });

  it("toggle buttons report the mode change to the page", () => {
    const { onPqlModeChange } = renderPanel({ pqlMode: false });
    fireEvent.click(screen.getByRole("button", { name: "PQL" }));
    expect(onPqlModeChange).toHaveBeenCalledWith("pql");
    fireEvent.click(screen.getByRole("button", { name: "Filters" }));
    expect(onPqlModeChange).toHaveBeenCalledWith("visual");
  });

  it("PQL mode replaces the visual controls with the editor + cheat-sheet", () => {
    renderPanel({ pqlMode: true, pqlAppliedQuery: "priority = High" });
    expect(screen.getByLabelText("PQL query")).toHaveValue("priority = High");
    // A cheat-sheet example unique to the sheet (the draft value also
    // renders as the textarea's text node, so "priority = High" alone
    // would match twice).
    expect(
      screen.getByText('state in (Todo, "In Progress")'),
    ).toBeInTheDocument();
    expect(screen.getByText("Fields & operators")).toBeInTheDocument();
    // Visual sections are gone, not merely hidden behind the editor.
    expect(screen.queryByText("State")).toBeNull();
    expect(screen.queryByText("No due date")).toBeNull();
  });

  it("Apply sends the draft query and closes on success", async () => {
    const { onPqlApply } = renderPanel({ pqlMode: true });
    fireEvent.change(screen.getByLabelText("PQL query"), {
      target: { value: "state = Todo" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Apply" }));
    await waitFor(() =>
      expect(onPqlApply).toHaveBeenCalledWith("state = Todo"),
    );
    await waitFor(() =>
      expect(screen.queryByLabelText("PQL query")).toBeNull(),
    );
  });

  it("a rejected Apply shows the server message + column inline and keeps the draft", async () => {
    const onPqlApply = vi.fn().mockRejectedValue(
      new ApiError(400, 'unknown state "Foo"', "bad_request", {
        position: 24,
      }),
    );
    renderPanel({ pqlMode: true, onPqlApply });
    fireEvent.change(screen.getByLabelText("PQL query"), {
      target: { value: 'state = Foo' },
    });
    fireEvent.click(screen.getByRole("button", { name: "Apply" }));
    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent('unknown state "Foo" (at column 24)');
    // The failed query stays in the editor; the list is untouched
    // because the parent only commits on a resolved Apply.
    expect(screen.getByLabelText("PQL query")).toHaveValue("state = Foo");
  });

  it("Clear empties the editor and commits an empty query", () => {
    const { onPqlClear } = renderPanel({
      pqlMode: true,
      pqlAppliedQuery: "state = Todo",
    });
    fireEvent.click(screen.getByRole("button", { name: "Clear" }));
    expect(onPqlClear).toHaveBeenCalledTimes(1);
    expect(screen.getByLabelText("PQL query")).toHaveValue("");
  });
});
