// @vitest-environment jsdom
// C83T1: DashboardDialog "Start from template" picker — template
// cards render the catalog (name, description, widget preview), and
// choosing one calls onCreateTemplate with the template id (plus the
// typed name as the optional override).
import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import DashboardDialog from "./DashboardDialog";
import type { DashboardTemplate } from "../../lib/dashboards";

const templates: DashboardTemplate[] = [
  {
    id: "sprint-health",
    name: "Sprint health",
    description: "Completion at a glance",
    widgets: [
      {
        title: "Completion by state group",
        widget_type: "smart_gauge",
        config: { group_by: "state_group" },
        grid_x: 0,
        grid_y: 0,
        grid_w: 6,
        grid_h: 4,
      },
      {
        title: "Workload by assignee",
        widget_type: "smart_counter",
        config: { group_by: "assignee" },
        grid_x: 6,
        grid_y: 0,
        grid_w: 6,
        grid_h: 4,
      },
    ],
  },
  {
    id: "triage",
    name: "Triage performance",
    description: "Intake throughput",
    widgets: [
      {
        title: "Intake",
        widget_type: "intake_breakdown",
        config: {},
        grid_x: 0,
        grid_y: 0,
        grid_w: 12,
        grid_h: 3,
      },
    ],
  },
];

const mockFetchTemplates = vi.hoisted(() =>
  vi.fn(() => Promise.resolve(templates)),
);

vi.mock("../../lib/dashboards", async (importOriginal) => {
  const mod =
    await importOriginal<typeof import("../../lib/dashboards")>();
  return { ...mod, fetchDashboardTemplates: mockFetchTemplates };
});

afterEach(() => {
  cleanup();
  mockFetchTemplates.mockClear();
});

function renderCreate(onCreateTemplate = vi.fn()) {
  render(
    <DashboardDialog
      open
      onOpenChange={() => {}}
      mode="create"
      onSubmit={() => {}}
      slug="acme"
      onCreateTemplate={onCreateTemplate}
    />,
  );
  return { onCreateTemplate };
}

describe("DashboardDialog template picker", () => {
  it("renders template cards with the widget list preview", async () => {
    renderCreate();
    expect(await screen.findByText("Start from template")).toBeInTheDocument();
    expect(screen.getByText("Sprint health")).toBeInTheDocument();
    expect(screen.getByText("Triage performance")).toBeInTheDocument();
    expect(screen.getByText("Completion at a glance")).toBeInTheDocument();
    expect(
      screen.getByText(/Completion by state group \(Smart gauge\)/),
    ).toBeInTheDocument();
  });

  it("creates from the chosen template with the {template: id} shape", async () => {
    const { onCreateTemplate } = renderCreate();
    fireEvent.click(await screen.findByText("Sprint health"));
    expect(onCreateTemplate).toHaveBeenCalledWith("sprint-health", undefined);
  });

  it("passes the typed name as the optional override", async () => {
    const { onCreateTemplate } = renderCreate();
    fireEvent.change(screen.getByLabelText(/Name/), {
      target: { value: "My sprint board" },
    });
    fireEvent.click(await screen.findByText("Triage performance"));
    expect(onCreateTemplate).toHaveBeenCalledWith(
      "triage",
      "My sprint board",
    );
  });
});
