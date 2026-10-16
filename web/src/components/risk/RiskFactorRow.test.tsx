// @vitest-environment jsdom
// C30T3: risk factor row — label/value/detail + drill-down link.
import { afterEach, describe, expect, it } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import RiskFactorRow from "./RiskFactorRow";
import type { ProjectRisk, RiskFactor } from "../../lib/risk";

afterEach(() => cleanup());

const project: ProjectRisk = {
  project_id: "p1",
  identifier: "API",
  name: "API service",
  open_count: 9,
  risk_score: 82,
  risk_level: "critical",
  factors: [],
};

function renderRow(factor: RiskFactor) {
  return render(
    <MemoryRouter>
      <RiskFactorRow slug="acme" project={project} factor={factor} />
    </MemoryRouter>,
  );
}

describe("RiskFactorRow", () => {
  it("renders label, value (3 decimals), and detail", () => {
    renderRow({
      key: "overdue",
      label: "Overdue issues",
      value: 0.556,
      detail: "5 of 9 open issues overdue",
    });
    const row = screen.getByTestId("risk-factor-row");
    expect(row.getAttribute("data-factor-key")).toBe("overdue");
    expect(row.textContent).toContain("Overdue issues");
    expect(row.textContent).toContain("0.556");
    expect(row.textContent).toContain("5 of 9 open issues overdue");
  });

  it("links overdue to the Issues view with due_before", () => {
    renderRow({
      key: "overdue",
      label: "Overdue issues",
      value: 0.5,
      detail: "1 of 2 open issues overdue",
    });
    const link = screen.getByRole("link", { name: /view overdue issues/i });
    expect(link.getAttribute("href")).toMatch(
      /^\/w\/acme\/p\/API\?due_before=\d{4}-\d{2}-\d{2}$/,
    );
  });

  it("links unassigned to assignee=none and overload to Workload", () => {
    renderRow({
      key: "unassigned",
      label: "Unassigned issues",
      value: 0.25,
      detail: "1 of 4 open issues with no assignee",
    });
    expect(
      screen
        .getByRole("link", { name: /view unassigned issues/i })
        .getAttribute("href"),
    ).toBe("/w/acme/p/API?assignee=none");
    cleanup();

    renderRow({
      key: "overload",
      label: "Overloaded members",
      value: 0.5,
      detail: "2 of 4 members overloaded",
    });
    expect(
      screen
        .getByRole("link", { name: /open workload view/i })
        .getAttribute("href"),
    ).toBe("/w/acme/workload");
  });

  it("renders no link for an unknown factor key", () => {
    renderRow({
      key: "mystery",
      label: "Mystery",
      value: 0.1,
      detail: "something",
    });
    expect(screen.queryByRole("link")).toBeNull();
  });
});
