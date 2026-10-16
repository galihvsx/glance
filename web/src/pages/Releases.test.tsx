// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import { QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import Releases, { progressCounts, ReleaseCard } from "./Releases";
import { api } from "../lib/api";
import { makeQueryClient } from "../components/shell/test-utils";
import { snapshotProgressCounts } from "../components/org/OrgViews";
import type { Issue, IssueState, Release } from "../lib/types";

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

function issue(id: string, state_id: string): Issue {
  return {
    id,
    project_id: "p",
    sequence_id: 1,
    display_id: "T-1",
    name: "x",
    priority: 0,
    state_id,
    sort_order: 0,
    is_draft: false,
    created_by: "u",
    created_at: "2026-01-01",
    updated_at: "2026-01-01",
    assignees: [],
    labels: [],
  };
}

function state(id: string, group: string): IssueState {
  return {
    id,
    name: id,
    group,
    color: "#000",
    sequence: 0,
    project_id: "p",
    created_at: "2026-01-01",
    updated_at: "2026-01-01",
  };
}

describe("progressCounts", () => {
  const states = [
    state("s1", "backlog"),
    state("s2", "started"),
    state("s3", "completed"),
    state("s4", "cancelled"),
  ];

  it("buckets by state group, cancelled counts as done", () => {
    const issues = [
      issue("a", "s1"),
      issue("b", "s2"),
      issue("c", "s3"),
      issue("d", "s4"),
    ];
    expect(progressCounts(issues, states)).toEqual({
      open: 1,
      inProgress: 1,
      done: 2,
    });
  });

  it("unknown state ids are ignored", () => {
    expect(progressCounts([issue("a", "nope")], states)).toEqual({
      open: 0,
      inProgress: 0,
      done: 0,
    });
  });

  it("empty inputs give zeros", () => {
    expect(progressCounts([], [])).toEqual({ open: 0, inProgress: 0, done: 0 });
  });
});

describe("snapshotProgressCounts", () => {
  it("folds the six groups exactly like progressCounts", () => {
    expect(
      snapshotProgressCounts({
        triage: 1,
        backlog: 2,
        unstarted: 3,
        started: 4,
        completed: 5,
        cancelled: 6,
      }),
    ).toEqual({ open: 6, inProgress: 4, done: 11 });
  });

  it("empty snapshot gives zeros, unknown groups ignored", () => {
    expect(snapshotProgressCounts({})).toEqual({
      open: 0,
      inProgress: 0,
      done: 0,
    });
    expect(
      snapshotProgressCounts({ triage: 1, something_new: 9 }),
    ).toEqual({ open: 1, inProgress: 0, done: 0 });
  });
});

function releaseCardRelease(
  overrides: Partial<Release> = {},
): Release {
  return {
    id: "rel-1",
    project_id: "p",
    name: "v0.4.0",
    description: "",
    status: "planned",
    release_date: "2026-11-01",
    issue_count: 12,
    progress_snapshot: {
      triage: 1,
      backlog: 2,
      unstarted: 0,
      started: 3,
      completed: 5,
      cancelled: 1,
    },
    created_at: "2026-01-01",
    updated_at: "2026-01-01",
    ...overrides,
  };
}

const cardStates: IssueState[] = [
  state("s-triage", "triage"),
  state("s-backlog", "backlog"),
  state("s-unstarted", "unstarted"),
  state("s-started", "started"),
  state("s-completed", "completed"),
  state("s-cancelled", "cancelled"),
];

describe("ReleaseCard", () => {
  it("renders a progress bar + folded counts from the snapshot", () => {
    render(
      <ReleaseCard
        release={releaseCardRelease()}
        states={cardStates}
        onSelect={() => {}}
      />,
    );
    const card = screen.getByRole("button", { name: "Open release v0.4.0" });
    expect(screen.getByRole("progressbar")).toBeTruthy();
    // Fold: open = 1 + 2 + 0, in progress = 3, done = 5 + 1.
    expect(card.textContent).toContain("6 done");
    expect(card.textContent).toContain("3 in progress");
    expect(card.textContent).toContain("3 open");
  });

  it("empty snapshot renders an all-open bar, not a missing bar", () => {
    render(
      <ReleaseCard
        release={releaseCardRelease({
          name: "v0.3.1",
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
        })}
        states={cardStates}
        onSelect={() => {}}
      />,
    );
    // The bar is still rendered (role=progressbar), with zero counts.
    expect(screen.getByRole("progressbar")).toBeTruthy();
    const card = screen.getByRole("button", { name: "Open release v0.3.1" });
    expect(card.textContent).toContain("0 done");
    expect(card.textContent).toContain("0 in progress");
    expect(card.textContent).toContain("0 open");
  });
});

describe("unknown ?release= id (C39T1)", () => {
  const seededRelease: Release = releaseCardRelease();

  function renderWithReleaseParam(releaseParam: string | null) {
    const qc = makeQueryClient([
      [["releases", "acme", "ENG"], [seededRelease]],
      [["states", "acme", "ENG"], cardStates],
    ]);
    const entry =
      releaseParam === null
        ? "/w/acme/p/ENG/releases"
        : `/w/acme/p/ENG/releases?release=${releaseParam}`;
    render(
      <MemoryRouter initialEntries={[entry]}>
        <QueryClientProvider client={qc}>
          <Routes>
            <Route
              path="/w/:slug/p/:identifier/releases"
              element={<Releases />}
            />
          </Routes>
        </QueryClientProvider>
      </MemoryRouter>,
    );
  }

  it("fires no detail-issues fetch for an unknown id and shows the list", async () => {
    // Never-resolving fetch: any query that fires hangs instead of
    // failing the render, so the assertion below is the verdict.
    const getSpy = vi
      .spyOn(api, "get")
      .mockImplementation(() => new Promise(() => {}) as Promise<never>);
    renderWithReleaseParam("unknown-id");
    // List view: the known release card renders.
    expect(
      await screen.findByRole("button", { name: "Open release v0.4.0" }),
    ).toBeTruthy();
    expect(
      getSpy.mock.calls.map(([url]) => String(url)),
    ).not.toContain(
      expect.stringContaining("/releases/unknown-id/issues"),
    );
  });

  it("still fetches detail issues for a known id (positive control)", async () => {
    const getSpy = vi.spyOn(api, "get").mockImplementation((url: string) => {
      if (url.includes("/releases/rel-1/issues")) {
        return Promise.resolve({ issues: [] });
      }
      throw new Error(`unexpected fetch: ${url}`);
    });
    renderWithReleaseParam("rel-1");
    // Detail view: the issues section renders once the fetch resolves.
    expect(await screen.findByText("Issues in this release")).toBeTruthy();
    expect(
      getSpy.mock.calls.map(([url]) => String(url)),
    ).toContainEqual(
      expect.stringContaining("/releases/rel-1/issues"),
    );
  });
});
