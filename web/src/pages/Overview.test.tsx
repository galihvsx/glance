// @vitest-environment jsdom
// Component tests for the Overview Milestones section (C38T1): rows
// with progress bar + counts + date, links to the release detail and
// to the Releases page, and the empty state (section stays visible).
import { afterEach, describe, expect, it } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { OverviewMilestones } from "./Overview";
import type { OverviewRelease, OverviewState } from "../lib/overview";

afterEach(() => {
  cleanup();
});

const states: OverviewState[] = [
  { id: "s1", name: "Triage", group: "triage", color: "#111111" },
  { id: "s2", name: "Backlog", group: "backlog", color: "#222222" },
  { id: "s3", name: "Todo", group: "unstarted", color: "#333333" },
  { id: "s4", name: "Doing", group: "started", color: "#444444" },
  { id: "s5", name: "Done", group: "completed", color: "#555555" },
  { id: "s6", name: "Cancelled", group: "cancelled", color: "#666666" },
];

const releases: OverviewRelease[] = [
  {
    id: "r1",
    name: "v0.4.0",
    status: "planned",
    release_date: "2026-11-01T00:00:00Z",
    issue_count: 12,
    progress_snapshot: {
      triage: 1,
      backlog: 2,
      unstarted: 0,
      started: 3,
      completed: 5,
      cancelled: 1,
    },
  },
  {
    id: "r2",
    name: "v0.3.1",
    status: "planned",
    release_date: null,
    issue_count: 0,
    progress_snapshot: {
      triage: 0,
      backlog: 0,
      unstarted: 0,
      started: 0,
      completed: 0,
      cancelled: 0,
    },
  },
];

function renderSection(rels: OverviewRelease[]) {
  render(
    <MemoryRouter>
      <OverviewMilestones
        releases={rels}
        states={states}
        issueBase="/w/acme/p/ENG"
      />
    </MemoryRouter>,
  );
}

describe("OverviewMilestones", () => {
  it("renders rows with progress bar, counts, and release date", () => {
    renderSection(releases);
    expect(screen.getByText("Milestones")).toBeTruthy();
    expect(screen.getByText("v0.4.0")).toBeTruthy();
    expect(screen.getByText("2026-11-01")).toBeTruthy();
    // Fold: open = 1 + 2 + 0, in progress = 3, done = 5 + 1.
    const row = screen.getByRole("link", { name: "Open release v0.4.0" });
    expect(row.textContent).toContain("6 done");
    expect(row.textContent).toContain("3 in progress");
    expect(row.textContent).toContain("3 open");
    expect(screen.getAllByRole("progressbar")).toHaveLength(2);
    // Empty-snapshot row still renders its all-open bar + zero counts.
    const emptyRow = screen.getByRole("link", { name: "Open release v0.3.1" });
    expect(emptyRow.textContent).toContain("No date");
    expect(emptyRow.textContent).toContain("0 done");
  });

  it("rows link to the release detail, header links to the Releases page", () => {
    renderSection(releases);
    const row = screen.getByRole("link", { name: "Open release v0.4.0" });
    expect(row.getAttribute("href")).toBe(
      "/w/acme/p/ENG/releases?release=r1",
    );
    const header = screen.getByRole("link", { name: "View all releases →" });
    expect(header.getAttribute("href")).toBe("/w/acme/p/ENG/releases");
  });

  it("empty state keeps the section visible with a link to Releases", () => {
    renderSection([]);
    // The section header is still rendered — it does not disappear.
    expect(screen.getByText("Milestones")).toBeTruthy();
    expect(
      screen.getByText(/No upcoming milestones — plan a release in/),
    ).toBeTruthy();
    const link = screen.getByRole("link", { name: "Releases" });
    expect(link.getAttribute("href")).toBe("/w/acme/p/ENG/releases");
    expect(screen.queryByRole("progressbar")).toBeNull();
  });
});
