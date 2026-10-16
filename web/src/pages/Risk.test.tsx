// @vitest-environment jsdom
// C30T3: Risk page tests — summary strip, per-project cards, factor rows,
// drill-down links, empty / loading / error states.
import { afterEach, describe, expect, it, vi } from "vitest";
import {
  cleanup,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import Risk from "./Risk";
import { api } from "../lib/api";
import type { Workspace } from "../lib/types";
import { workspacesKey } from "../lib/useWorkspaces";
import type { RiskReport } from "../lib/risk";
import { makeQueryClient } from "../components/shell/test-utils";

const workspace: Workspace = {
  id: "w1",
  slug: "acme",
  name: "Acme",
  role: 15,
  slack_configured: false,
  created_at: "",
  updated_at: "",
};

// Seeded in identifier order (backend order); the page sorts by score desc.
const report: RiskReport = {
  projects: [
    {
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
    },
    {
      project_id: "p-mob",
      identifier: "MOB",
      name: "Mobile app",
      open_count: 22,
      risk_score: 31,
      risk_level: "watch",
      factors: [
        {
          key: "unassigned",
          label: "Unassigned issues",
          value: 0.318,
          detail: "7 of 22 open issues with no assignee",
        },
      ],
    },
    {
      project_id: "p-ops",
      identifier: "OPS",
      name: "Ops & infra",
      open_count: 6,
      risk_score: 8,
      risk_level: "healthy",
      factors: [],
    },
    {
      project_id: "p-web",
      identifier: "WEB",
      name: "Web app",
      open_count: 14,
      risk_score: 68,
      risk_level: "at-risk",
      factors: [
        {
          key: "overdue",
          label: "Overdue issues",
          value: 0.357,
          detail: "5 of 14 open issues overdue",
        },
      ],
    },
  ],
  generated_at: new Date().toISOString(),
};

function renderPage(opts: { reportSeed?: RiskReport | null } = {}) {
  const seeds: [readonly unknown[], unknown][] = [
    [["risk", "acme"], opts.reportSeed ?? report],
    [["workspace", "acme"], workspace],
    [workspacesKey, [workspace]],
  ];
  if (opts.reportSeed === null) seeds.shift();
  const qc = makeQueryClient(seeds);
  return render(
    <MemoryRouter initialEntries={["/w/acme/risk"]}>
      <QueryClientProvider client={qc}>
        <Routes>
          <Route path="/w/:slug/risk" element={<Risk />} />
        </Routes>
      </QueryClientProvider>
    </MemoryRouter>,
  );
}

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

describe("Risk page", () => {
  it("shows summary counts per level", () => {
    renderPage({});
    expect(
      screen.getByTestId("risk-summary-healthy").textContent,
    ).toContain("1");
    expect(screen.getByTestId("risk-summary-watch").textContent).toContain(
      "1",
    );
    expect(
      screen.getByTestId("risk-summary-at-risk").textContent,
    ).toContain("1");
    expect(
      screen.getByTestId("risk-summary-critical").textContent,
    ).toContain("1");
  });

  it("renders per-project cards sorted by score desc", () => {
    renderPage({});
    const cards = screen.getAllByTestId("risk-card");
    expect(cards).toHaveLength(4);
    expect(cards[0].textContent).toContain("API service");
    expect(cards[1].textContent).toContain("Web app");
    expect(cards[2].textContent).toContain("Mobile app");
    expect(cards[3].textContent).toContain("Ops & infra");
    expect(cards[0].textContent).toContain("82");
    expect(cards[0].textContent).toContain("9 open issues");
  });

  it("renders factor rows with label, value, and detail", () => {
    renderPage({});
    const apiCard = screen
      .getAllByTestId("risk-card")
      .find((c) => c.textContent?.includes("API service"));
    expect(apiCard?.textContent).toContain("Overdue issues");
    expect(apiCard?.textContent).toContain("0.556");
    expect(apiCard?.textContent).toContain("5 of 9 open issues overdue");
    expect(apiCard?.textContent).toContain("Stalled issues");
  });

  it("drill-down links point at Issues with the right filter params", () => {
    renderPage({});
    const overdue = screen.getAllByRole("link", {
      name: /view overdue issues/i,
    })[0];
    expect(overdue.getAttribute("href")).toMatch(
      /^\/w\/acme\/p\/API\?due_before=\d{4}-\d{2}-\d{2}$/,
    );
    const unassigned = screen.getByRole("link", {
      name: /view unassigned issues/i,
    });
    expect(unassigned.getAttribute("href")).toBe(
      "/w/acme/p/MOB?assignee=none",
    );
  });

  it("shows the empty state when the workspace has no projects", () => {
    renderPage({ reportSeed: { projects: [], generated_at: "" } });
    expect(
      screen.getByText(/no projects in this workspace yet/i),
    ).toBeTruthy();
    expect(screen.queryByTestId("risk-card")).toBeNull();
  });

  it("shows a loading skeleton while the report loads", () => {
    vi.spyOn(api, "get").mockReturnValue(new Promise(() => {}));
    renderPage({ reportSeed: null });
    expect(
      document.querySelector('[data-testid="risk-skeleton"]'),
    ).toBeTruthy();
  });

  it("shows an error state when the report fails to load", async () => {
    vi.spyOn(api, "get").mockRejectedValue(new Error("boom"));
    renderPage({ reportSeed: null });
    await waitFor(() => {
      expect(
        screen.getByText(/couldn't load risk/i),
      ).toBeTruthy();
    });
  });
});
