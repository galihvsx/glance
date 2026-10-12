// @vitest-environment jsdom
import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";
import InitiativeProgressBar from "./InitiativeProgressBar";

describe("InitiativeProgressBar", () => {
  it("renders the completion percentage label", () => {
    render(<InitiativeProgressBar completionPct={42.4} />);
    expect(screen.getByText("42%")).toBeTruthy();
    expect(
      screen.getByRole("progressbar", { name: /42%/i }),
    ).toBeTruthy();
  });

  it("shows done-of-total counts when provided", () => {
    render(
      <InitiativeProgressBar completionPct={75} total={40} doneCount={30} />,
    );
    expect(screen.getByText(/30 of 40/i)).toBeTruthy();
  });

  it("clamps out-of-range percentages to 0–100", () => {
    const { rerender } = render(<InitiativeProgressBar completionPct={150} />);
    expect(screen.getByText("100%")).toBeTruthy();
    rerender(<InitiativeProgressBar completionPct={-5} />);
    expect(screen.getByText("0%")).toBeTruthy();
  });

  it("renders a zero state without counts", () => {
    render(<InitiativeProgressBar completionPct={0} total={0} doneCount={0} />);
    expect(screen.getByText("0%")).toBeTruthy();
    expect(screen.getByText(/0 of 0/i)).toBeTruthy();
  });
});
