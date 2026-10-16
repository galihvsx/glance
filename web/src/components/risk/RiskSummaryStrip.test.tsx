// @vitest-environment jsdom
// C30T3: risk summary strip — counts per level.
import { afterEach, describe, expect, it } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import RiskSummaryStrip from "./RiskSummaryStrip";
import type { ProjectRisk, RiskLevel } from "../../lib/risk";

afterEach(() => cleanup());

function project(level: RiskLevel, score: number): ProjectRisk {
  return {
    project_id: `p-${level}-${score}`,
    identifier: level.toUpperCase(),
    name: level,
    open_count: 3,
    risk_score: score,
    risk_level: level,
    factors: [],
  };
}

describe("RiskSummaryStrip", () => {
  it("counts projects per level", () => {
    render(
      <RiskSummaryStrip
        projects={[
          project("healthy", 8),
          project("critical", 82),
          project("critical", 90),
          project("watch", 31),
        ]}
      />,
    );
    expect(screen.getByLabelText("Risk summary")).toBeTruthy();
    expect(
      screen.getByTestId("risk-summary-healthy").textContent,
    ).toContain("1");
    expect(screen.getByTestId("risk-summary-watch").textContent).toContain(
      "1",
    );
    expect(
      screen.getByTestId("risk-summary-at-risk").textContent,
    ).toContain("0");
    expect(
      screen.getByTestId("risk-summary-critical").textContent,
    ).toContain("2");
  });

  it("shows each level's score band", () => {
    render(<RiskSummaryStrip projects={[]} />);
    expect(document.body.textContent).toContain("score 0–24");
    expect(document.body.textContent).toContain("score 25–49");
    expect(document.body.textContent).toContain("score 50–74");
    expect(document.body.textContent).toContain("score 75–100");
  });
});
