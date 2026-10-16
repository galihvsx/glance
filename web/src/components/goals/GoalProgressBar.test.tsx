// @vitest-environment jsdom
import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";
import GoalProgressBar from "./GoalProgressBar";

describe("GoalProgressBar", () => {
  it("renders the percent label from the 0–1 fraction", () => {
    render(<GoalProgressBar progress={0.62} />);
    expect(screen.getByText("62%")).toBeTruthy();
    expect(
      screen.getByRole("progressbar", { name: "Goal progress 62%" }),
    ).toBeTruthy();
  });

  it("clamps out-of-range progress", () => {
    const { rerender } = render(<GoalProgressBar progress={1.5} />);
    expect(screen.getByText("100%")).toBeTruthy();
    rerender(<GoalProgressBar progress={-0.2} />);
    expect(screen.getByText("0%")).toBeTruthy();
    rerender(<GoalProgressBar progress={NaN} />);
    expect(screen.getByText("0%")).toBeTruthy();
  });

  it("renders the caption when given", () => {
    render(<GoalProgressBar progress={0.5} caption="5 of 10 issues done" />);
    expect(screen.getByText("5 of 10 issues done")).toBeTruthy();
  });
});
