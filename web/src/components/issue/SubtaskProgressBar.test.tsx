// @vitest-environment jsdom
import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import SubtaskProgressBar from "./SubtaskProgressBar";

describe("SubtaskProgressBar", () => {
  it("renders the detail bar with done/total text", () => {
    render(<SubtaskProgressBar progress={{ total: 8, done: 3 }} variant="detail" />);
    expect(screen.getByText("3/8")).toBeInTheDocument();
    expect(
      screen.getByTitle("3 of 8 subtasks done"),
    ).toBeInTheDocument();
  });

  it("renders nothing when there are no subtasks", () => {
    const { container } = render(
      <SubtaskProgressBar progress={{ total: 0, done: 0 }} variant="detail" />,
    );
    expect(container).toBeEmptyDOMElement();
  });

  it("renders nothing when progress is missing", () => {
    const { container } = render(
      <SubtaskProgressBar progress={null} variant="compact" />,
    );
    expect(container).toBeEmptyDOMElement();
  });

  it("renders a labelled compact progressbar for cards", () => {
    render(<SubtaskProgressBar progress={{ total: 4, done: 2 }} variant="compact" />);
    const bar = screen.getByRole("progressbar", {
      name: "Subtasks: 2 of 4 done",
    });
    expect(bar).toBeInTheDocument();
    expect(bar).toHaveAttribute("aria-valuemax", "4");
  });
});
