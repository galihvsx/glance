// @vitest-environment jsdom
// C30T3: risk card — per-project score/level/open count + factor rows in
// received order + healthy empty-factors state.
import { afterEach, describe, expect, it } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import RiskCard from "./RiskCard";
import type { ProjectRisk } from "../../lib/risk";

afterEach(() => cleanup());

const apiProject: ProjectRisk = {
  project_id: "p-api",
  identifier: "API",
  name: "API service",
  open_count: 9,
  risk_score: 82,
  risk_level: "critical",
  factors: [
    {
      key: "overdue",
      label: "Overdue issues",
      value: 0.556,
      detail: "5 of 9 open issues overdue",
    },
    {
      key: "stalled",
      label: "Stalled issues",
      value: 0.333,
      detail: "3 of 9 open issues with no activity in 7 days",
    },
  ],
};

function renderCard(project: ProjectRisk) {
  return render(
    <MemoryRouter>
      <RiskCard slug="acme" project={project} />
    </MemoryRouter>,
  );
}

describe("RiskCard", () => {
  it("renders identifier, name, open count, score, and level", () => {
    renderCard(apiProject);
    const card = screen.getByTestId("risk-card");
    expect(card.getAttribute("data-project-id")).toBe("p-api");
    expect(card.textContent).toContain("API");
    expect(card.textContent).toContain("API service");
    expect(card.textContent).toContain("9 open issues");
    expect(card.textContent).toContain("82");
    expect(card.textContent).toContain("/ 100");
    expect(screen.getByTestId("risk-level-badge").textContent).toBe(
      "Critical",
    );
  });

  it("renders factor rows in the order received (no client re-sort)", () => {
    // Deliberately NOT contribution order: the card must render as received.
    renderCard({
      ...apiProject,
      factors: [
        {
          key: "unassigned",
          label: "Unassigned issues",
          value: 0.1,
          detail: "1 of 9 open issues with no assignee",
        },
        {
          key: "overdue",
          label: "Overdue issues",
          value: 0.556,
          detail: "5 of 9 open issues overdue",
        },
      ],
    });
    const rows = screen.getAllByTestId("risk-factor-row");
    expect(rows).toHaveLength(2);
    expect(rows[0].textContent).toContain("Unassigned issues");
    expect(rows[1].textContent).toContain("Overdue issues");
  });

  it("shows the no-factors state for a healthy project", () => {
    renderCard({
      project_id: "p-ops",
      identifier: "OPS",
      name: "Ops & infra",
      open_count: 6,
      risk_score: 8,
      risk_level: "healthy",
      factors: [],
    });
    expect(screen.getByTestId("risk-card").textContent).toContain(
      "No risk factors",
    );
    expect(screen.queryByTestId("risk-factor-row")).toBeNull();
  });

  it("singularizes one open issue", () => {
    renderCard({ ...apiProject, open_count: 1 });
    expect(screen.getByTestId("risk-card").textContent).toContain(
      "1 open issue",
    );
  });
});
