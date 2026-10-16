// @vitest-environment jsdom
// C43T2: GoalSparkline — exact geometry + the C42 guards at
// sparkline scale (empty → null, single point → centered dot,
// zero time range → index-spread, zero value range → midline).
import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";
import GoalSparkline from "./GoalSparkline";
import type { ObjectiveProgressPoint } from "../../lib/types";

const pt = (t: string, progress: number): ObjectiveProgressPoint => ({
  t,
  progress,
});

describe("GoalSparkline", () => {
  it("renders nothing for empty history", () => {
    const { container } = render(<GoalSparkline points={[]} />);
    expect(container).toBeEmptyDOMElement();
  });

  it("renders a rising line with exact geometry", () => {
    render(
      <GoalSparkline
        points={[
          pt("2026-01-01T00:00:00Z", 0),
          pt("2026-01-02T00:00:00Z", 1),
        ]}
      />,
    );
    const line = screen.getByTestId("goal-sparkline-line");
    expect(line.getAttribute("d")).toBe("M0.0,26.0 L260.0,2.0");
    const dot = screen.getByTestId("goal-sparkline-dot");
    expect(dot.getAttribute("cx")).toBe("260");
    expect(dot.getAttribute("cy")).toBe("2");
    expect(
      screen.getByRole("img", { name: "Progress history: 0% → 100%" }),
    ).toBeTruthy();
  });

  it("renders a single point as a centered dot, no line", () => {
    render(<GoalSparkline points={[pt("2026-01-01T00:00:00Z", 0.5)]} />);
    expect(screen.queryByTestId("goal-sparkline-line")).toBeNull();
    const dot = screen.getByTestId("goal-sparkline-dot");
    expect(dot.getAttribute("cx")).toBe("130");
    // Zero value range → midline (TOP + plotH/2 = 2 + 12).
    expect(dot.getAttribute("cy")).toBe("14");
    expect(
      screen.getByRole("img", { name: "Progress history: 50% → 50%" }),
    ).toBeTruthy();
  });

  it("spreads points evenly by index when all timestamps are equal", () => {
    render(
      <GoalSparkline
        points={[
          pt("2026-01-01T00:00:00Z", 0),
          pt("2026-01-01T00:00:00Z", 0.5),
          pt("2026-01-01T00:00:00Z", 1),
        ]}
      />,
    );
    expect(screen.getByTestId("goal-sparkline-line").getAttribute("d")).toBe(
      "M0.0,26.0 L130.0,14.0 L260.0,2.0",
    );
  });

  it("renders a flat midline when all values are equal", () => {
    render(
      <GoalSparkline
        points={[
          pt("2026-01-01T00:00:00Z", 0.5),
          pt("2026-01-02T00:00:00Z", 0.5),
        ]}
      />,
    );
    expect(screen.getByTestId("goal-sparkline-line").getAttribute("d")).toBe(
      "M0.0,14.0 L260.0,14.0",
    );
  });
});
