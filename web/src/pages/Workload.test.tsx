// @vitest-environment jsdom
// C29T1: Workload page tests — member rows, flags, project filter chips,
// drill-down links, unassigned section.
import { afterEach, describe, expect, it, vi } from "vitest";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import Workload from "./Workload";
import { api } from "../lib/api";
import type { Project, Workspace } from "../lib/types";
import { workspacesKey } from "../lib/useWorkspaces";
import type { WorkloadReport } from "../lib/workload";
import { makeQueryClient } from "../components/shell/test-utils";

const workspace: Workspace = {
  id: "w1",
  slug: "acme",
  name: "Acme",
  role: 15,
  slack_configured: false,
    github_webhook_configured: false,
  created_at: "",
  updated_at: "",
};

const projects: Project[] = [
  {
    id: "p1",
    workspace_id: "w1",
    identifier: "ENG",
    labels: [],
    name: "Engineering",
    description: "",
    created_at: "",
    updated_at: "",
  },
  {
    id: "p2",
    workspace_id: "w1",
    identifier: "DES",
    labels: [],
    name: "Design",
    description: "",
    created_at: "",
    updated_at: "",
  },
];

const report: WorkloadReport = {
  members: [
    {
      user_id: "u1",
      display_name: "Alice Nguyen",
      role: 15,
      weekly_capacity_points: 20,
      open_count: 3,
      open_points: 26,
      overdue_count: 2,
      priority: { urgent: 1, high: 1, medium: 1, low: 0, none: 0 },
      completed_14d: 5,
      overloaded: true,
      project_ids: ["p1"],
    },
    {
      user_id: "u2",
      display_name: "Bob Lee",
      role: 20,
      weekly_capacity_points: null,
      open_count: 1,
      open_points: 3,
      overdue_count: 0,
      priority: { urgent: 0, high: 0, medium: 1, low: 0, none: 0 },
      completed_14d: 2,
      overloaded: false,
      project_ids: ["p2"],
    },
  ],
  unassigned: [
    { project_id: "p1", project_identifier: "ENG", open_count: 4 },
  ],
  generated_at: new Date().toISOString(),
};

const desReport: WorkloadReport = {
  members: [
    {
      user_id: "u3",
      display_name: "Carol Park",
      role: 15,
      weekly_capacity_points: 10,
      open_count: 2,
      open_points: 5,
      overdue_count: 0,
      priority: { urgent: 0, high: 1, medium: 1, low: 0, none: 0 },
      completed_14d: 3,
      overloaded: false,
      project_ids: ["p2"],
    },
  ],
  unassigned: [],
  generated_at: new Date().toISOString(),
};

function renderPage(opts: {
  initialEntries?: string[];
  reports?: Record<string, WorkloadReport>;
  projectsSeed?: Project[];
}) {
  const reports = opts.reports ?? { "": report };
  const seeds: [readonly unknown[], unknown][] = [
    [["projects", "acme"], opts.projectsSeed ?? projects],
    [["workspace", "acme"], workspace],
    [workspacesKey, [workspace]],
  ];
  for (const [key, r] of Object.entries(reports)) {
    seeds.push([["workload", "acme", key], r]);
  }
  const qc = makeQueryClient(seeds);
  return render(
    <MemoryRouter initialEntries={opts.initialEntries ?? ["/w/acme/workload"]}>
      <QueryClientProvider client={qc}>
        <Routes>
          <Route path="/w/:slug/workload" element={<Workload />} />
        </Routes>
      </QueryClientProvider>
    </MemoryRouter>,
  );
}

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

describe("Workload page", () => {
  it("renders member rows with the capacity bar text", () => {
    renderPage({});
    const rows = screen.getAllByTestId("workload-member-row");
    expect(rows).toHaveLength(2);
    expect(screen.getByText("Alice Nguyen")).toBeTruthy();
    expect(document.body.textContent).toContain("26 / 20 pts");
    // Unset capacity reads back as "no capacity set".
    expect(document.body.textContent).toContain("3 / no capacity set");
  });

  it("flags overloaded members only", () => {
    renderPage({});
    const flags = screen.getAllByTestId("overloaded-flag");
    expect(flags).toHaveLength(1);
    expect(flags[0].textContent).toContain("Overloaded");
  });

  it("shows overdue and urgent flags with counts", () => {
    renderPage({});
    expect(screen.getByTestId("overdue-flag").textContent).toContain(
      "2 overdue",
    );
    expect(screen.getByTestId("urgent-flag").textContent).toContain("1 urgent");
    // Bob has neither flag.
    const bobRow = screen
      .getAllByTestId("workload-member-row")
      .find((r) => r.getAttribute("data-user-id") === "u2");
    expect(bobRow?.querySelector("[data-testid='overdue-flag']")).toBeNull();
  });

  it("shows 14-day velocity per member", () => {
    renderPage({});
    expect(document.body.textContent).toContain("5 done in 14d");
    expect(document.body.textContent).toContain("2 done in 14d");
  });

  it("drill-down links into the issues view filtered by assignee", () => {
    renderPage({});
    const link = screen.getByRole("link", {
      name: /view alice nguyen's open issues/i,
    });
    expect(link.getAttribute("href")).toBe("/w/acme/p/ENG?assignee=u1");
  });

  it("renders the unassigned section with assignee=none triage links", () => {
    renderPage({});
    const link = screen.getByRole("link", {
      name: /view unassigned issues in eng/i,
    });
    expect(link.getAttribute("href")).toBe("/w/acme/p/ENG?assignee=none");
    expect(link.textContent).toContain("4");
  });

  it("filters by project chip and refetches with the project param", async () => {
    renderPage({ reports: { "": report, p2: desReport } });
    fireEvent.click(screen.getByRole("button", { name: "DES" }));
    await waitFor(() => {
      expect(screen.getByText("Carol Park")).toBeTruthy();
    });
    expect(screen.queryByText("Alice Nguyen")).toBeNull();
  });

  it("shows a loading skeleton while the report loads", () => {
    vi.spyOn(api, "get").mockReturnValue(new Promise(() => {}));
    renderPage({ reports: {} });
    expect(
      document.querySelector('[data-testid="workload-skeleton"]'),
    ).toBeTruthy();
  });
});
