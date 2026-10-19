// ProjectLabelChips tests (C76T1): chip rendering, the +N overflow with
// accessible titles, and the empty state.
// @vitest-environment jsdom
import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";
import ProjectLabelChips from "./ProjectLabelChips";
import type { ProjectLabel } from "../../lib/types";

const l = (id: string, name: string, color = "#ef4444"): ProjectLabel => ({
  id,
  name,
  color,
});

describe("ProjectLabelChips", () => {
  it("renders one chip per label with a color dot", () => {
    render(
      <ProjectLabelChips
        labels={[l("1", "platform", "#3b82f6"), l("2", "mobile")]}
      />,
    );
    expect(screen.getByTitle("platform")).toBeInTheDocument();
    expect(screen.getByTitle("mobile")).toBeInTheDocument();
    const dot = screen
      .getByTitle("platform")
      .querySelector('span[aria-hidden="true"]');
    expect(dot).toHaveStyle({ backgroundColor: "#3b82f6" });
  });

  it("collapses overflow into a +N chip whose title lists the hidden names", () => {
    render(
      <ProjectLabelChips
        max={2}
        labels={[
          l("1", "one"),
          l("2", "two"),
          l("3", "three"),
          l("4", "four"),
        ]}
      />,
    );
    expect(screen.getByTitle("one")).toBeInTheDocument();
    expect(screen.getByTitle("two")).toBeInTheDocument();
    expect(screen.queryByTitle("three")).not.toBeInTheDocument();
    const overflow = screen.getByTitle("three, four");
    expect(overflow).toHaveTextContent("+2");
  });

  it("renders nothing when there are no labels", () => {
    const { container } = render(<ProjectLabelChips labels={[]} />);
    expect(container).toBeEmptyDOMElement();
  });

  it("exposes the full label list to assistive tech", () => {
    render(
      <ProjectLabelChips labels={[l("1", "platform"), l("2", "mobile")]} />,
    );
    expect(
      screen.getByRole("group", {
        name: "Project labels: platform, mobile",
      }),
    ).toBeInTheDocument();
  });
});
