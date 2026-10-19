// @vitest-environment jsdom
import { afterEach, describe, expect, it } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import EpicGroupBreakdown from "./EpicGroupBreakdown";

afterEach(cleanup);

describe("EpicGroupBreakdown", () => {
  it("renders five segments (no triage) with the fixed group order", () => {
    render(
      <EpicGroupBreakdown
        byGroup={{ backlog: 2, unstarted: 3, started: 4, completed: 3, cancelled: 0 }}
        total={12}
        epicName="Auth revamp"
      />,
    );
    const bar = screen.getByRole("progressbar", { name: "Auth revamp progress" });
    expect(bar.children).toHaveLength(4); // zero-count groups render no segment
    for (const label of ["Backlog", "Unstarted", "Started", "Completed", "Cancelled"]) {
      expect(screen.getByText(label)).toBeTruthy();
    }
  });

  it("renders the empty state for an epic with no issues", () => {
    render(<EpicGroupBreakdown byGroup={{}} total={0} epicName="Empty" />);
    expect(screen.getByText(/no issues yet/i)).toBeTruthy();
    expect(screen.queryByRole("progressbar")).toBeNull();
  });
});
