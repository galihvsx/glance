// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import BulkActionBar, { addableCycles } from "./BulkActionBar";
import type { Cycle, Module } from "../../lib/types";

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

const cycles = [
  {
    id: "c-upcoming",
    project_id: "p1",
    name: "Sprint 13",
    start_date: "2026-10-20",
    end_date: "2026-11-02",
    status: "upcoming",
    progress_snapshot: {},
    created_at: "",
    updated_at: "",
  },
  {
    id: "c-current",
    project_id: "p1",
    name: "Sprint 12",
    start_date: "2026-10-06",
    end_date: "2026-10-19",
    status: "current",
    progress_snapshot: {},
    created_at: "",
    updated_at: "",
  },
  {
    id: "c-done",
    project_id: "p1",
    name: "Sprint 11",
    start_date: "2026-09-22",
    end_date: "2026-10-05",
    status: "completed",
    progress_snapshot: {},
    created_at: "",
    updated_at: "",
  },
] as Cycle[];

const modules = [
  {
    id: "m1",
    project_id: "p1",
    name: "Auth",
    status: "active",
    issue_count: 3,
    created_at: "",
    updated_at: "",
  },
] as Module[];

const baseProps = {
  states: [],
  members: null,
  labels: null,
  selectedCount: 2,
  loadedCount: 10,
  allLoadedSelected: false,
  onToggleSelectAll: () => {},
  onClearSelection: () => {},
  onApply: () => {},
  applying: false,
  error: null,
  cycles,
  modules,
  structureLoading: false,
  onAddToCycle: () => {},
  onAddToModule: () => {},
  organizeApplying: false,
  organizeError: null,
};

describe("addableCycles", () => {
  it("excludes completed cycles", () => {
    expect(addableCycles(cycles).map((c) => c.id)).toEqual([
      "c-upcoming",
      "c-current",
    ]);
  });

  it("handles null", () => {
    expect(addableCycles(null)).toEqual([]);
  });
});

describe("BulkActionBar organize actions", () => {
  it("opens the cycle picker, hides completed cycles, and calls onAddToCycle on confirm", () => {
    const onAddToCycle = vi.fn();
    render(<BulkActionBar {...baseProps} onAddToCycle={onAddToCycle} />);

    fireEvent.click(screen.getByRole("button", { name: /Add to cycle/ }));
    expect(
      screen.getByRole("dialog", { name: "Add to cycle" }),
    ).toBeInTheDocument();
    expect(screen.getByText("Sprint 12")).toBeInTheDocument();
    expect(screen.getByText("Sprint 13")).toBeInTheDocument();
    expect(screen.queryByText("Sprint 11")).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole("radio", { name: /Sprint 12/ }));
    fireEvent.click(screen.getByRole("button", { name: "Add 2 issues" }));
    expect(onAddToCycle).toHaveBeenCalledTimes(1);
    expect(onAddToCycle).toHaveBeenCalledWith("c-current");
  });

  it("opens the module picker and calls onAddToModule on confirm", () => {
    const onAddToModule = vi.fn();
    render(<BulkActionBar {...baseProps} onAddToModule={onAddToModule} />);

    fireEvent.click(screen.getByRole("button", { name: /Add to module/ }));
    expect(
      screen.getByRole("dialog", { name: "Add to module" }),
    ).toBeInTheDocument();

    fireEvent.click(screen.getByRole("radio", { name: /Auth/ }));
    fireEvent.click(screen.getByRole("button", { name: "Add 2 issues" }));
    expect(onAddToModule).toHaveBeenCalledTimes(1);
    expect(onAddToModule).toHaveBeenCalledWith("m1");
  });

  it("shows the empty text when there are no addable cycles", () => {
    render(<BulkActionBar {...baseProps} cycles={[cycles[2]]} />);
    fireEvent.click(screen.getByRole("button", { name: /Add to cycle/ }));
    expect(
      screen.getByText("No upcoming or active cycles in this project."),
    ).toBeInTheDocument();
  });

  it("disables the organize buttons while a membership POST is in flight", () => {
    render(<BulkActionBar {...baseProps} organizeApplying />);
    expect(
      screen.getByRole("button", { name: /Add to cycle/ }),
    ).toBeDisabled();
    expect(
      screen.getByRole("button", { name: /Add to module/ }),
    ).toBeDisabled();
  });

  it("renders the organize error in the bar", () => {
    render(
      <BulkActionBar {...baseProps} organizeError="Failed to add issues to cycle" />,
    );
    expect(
      screen.getByRole("alert"),
    ).toHaveTextContent("Failed to add issues to cycle");
  });
});
