// @vitest-environment jsdom
// C47T2: ObjectiveProgressChart — exact geometry + the C42/C43
// guards at detail scale (empty → null, single point → centered
// dot, zero time range → index-spread, zero value range → midline).
import { describe, expect, it } from "vitest";
import { render, screen, within } from "@testing-library/react";
import ObjectiveProgressChart from "./ObjectiveProgressChart";
import type { ObjectiveProgressPoint } from "../../lib/types";

const pt = (t: string, progress: number): ObjectiveProgressPoint => ({
  t,
  progress,
});

describe("ObjectiveProgressChart", () => {
  it("renders nothing for empty history", () => {
    const { container } = render(<ObjectiveProgressChart points={[]} />);
    expect(container).toBeEmptyDOMElement();
  });

  it("renders a rising line with exact geometry and percent axis labels", () => {
    render(
      <ObjectiveProgressChart
        points={[
          pt("2026-01-01T00:00:00Z", 0),
          pt("2026-01-02T00:00:00Z", 1),
        ]}
      />,
    );
    const chart = screen.getByTestId("objective-progress-chart");
    expect(
      screen.getByRole("img", {
        name: "Objective progress over time: 0% → 100%",
      }),
    ).toBeTruthy();
    expect(
      screen.getByRole("img", {
        name: "Objective progress over time: 0% → 100%",
      }).querySelector("title")?.textContent,
    ).toBe("Objective progress over time: 0% → 100%");
    expect(
      screen.getByTestId("objective-chart-line").getAttribute("d"),
    ).toBe("M44.0,98.0 L312.0,8.0");
    const dots = within(chart).getAllByTestId("objective-chart-point");
    expect(dots).toHaveLength(2);
    expect(dots.map((d) => [d.getAttribute("cx"), d.getAttribute("cy")])).toEqual([
      ["44", "98"],
      ["312", "8"],
    ]);
    // Percent axis labels (hi top, lo bottom) + first/last dates.
    expect(within(chart).getByText("100%")).toBeTruthy();
    expect(within(chart).getByText("0%")).toBeTruthy();
    expect(within(chart).getByText("1 Jan 2026")).toBeTruthy();
    expect(within(chart).getByText("2 Jan 2026")).toBeTruthy();
  });

  it("renders a single point as a centered dot, no line", () => {
    render(
      <ObjectiveProgressChart
        points={[pt("2026-01-01T00:00:00Z", 0.5)]}
      />,
    );
    expect(screen.queryByTestId("objective-chart-line")).toBeNull();
    const dot = screen.getByTestId("objective-chart-point");
    // Centered x: LEFT + plotW/2 = 44 + 134. Zero value range →
    // midline: TOP + plotH/2 = 8 + 45.
    expect(dot.getAttribute("cx")).toBe("178");
    expect(dot.getAttribute("cy")).toBe("53");
    expect(
      screen.getByRole("img", {
        name: "Objective progress over time: 50% → 50%",
      }),
    ).toBeTruthy();
  });

  it("spreads points evenly by index when all timestamps are equal", () => {
    render(
      <ObjectiveProgressChart
        points={[
          pt("2026-01-01T00:00:00Z", 0),
          pt("2026-01-01T00:00:00Z", 0.5),
          pt("2026-01-01T00:00:00Z", 1),
        ]}
      />,
    );
    expect(
      screen.getByTestId("objective-chart-line").getAttribute("d"),
    ).toBe("M44.0,98.0 L178.0,53.0 L312.0,8.0");
    const dots = screen.getAllByTestId("objective-chart-point");
    expect(dots).toHaveLength(3);
    expect(dots.map((d) => d.getAttribute("cx"))).toEqual([
      "44",
      "178",
      "312",
    ]);
    expect(
      dots.every((d) => Number.isFinite(Number(d.getAttribute("cx")))),
    ).toBe(true);
  });

  it("renders a flat midline when all values are equal", () => {
    render(
      <ObjectiveProgressChart
        points={[
          pt("2026-01-01T00:00:00Z", 0.5),
          pt("2026-01-02T00:00:00Z", 0.5),
        ]}
      />,
    );
    expect(
      screen.getByTestId("objective-chart-line").getAttribute("d"),
    ).toBe("M44.0,53.0 L312.0,53.0");
    const dots = screen.getAllByTestId("objective-chart-point");
    expect(dots.map((d) => d.getAttribute("cy"))).toEqual(["53", "53"]);
  });

  it("clamps out-of-range progress values to 0–100%", () => {
    render(
      <ObjectiveProgressChart
        points={[
          pt("2026-01-01T00:00:00Z", -0.2),
          pt("2026-01-02T00:00:00Z", 1.4),
        ]}
      />,
    );
    expect(
      screen.getByRole("img", {
        name: "Objective progress over time: 0% → 100%",
      }),
    ).toBeTruthy();
  });
});
