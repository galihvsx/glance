// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
} from "@testing-library/react";
import { QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, Route, Routes, useSearchParams } from "react-router-dom";
import Initiatives from "./Initiatives";
import {
  EMPTY_INITIATIVE_FILTERS,
  initiativeActiveFilterCount,
  parseInitiativeFilters,
  serializeInitiativeFilters,
  toInitiativeApiParams,
  type InitiativeFilters,
} from "../lib/initiativeFilters";
import type {
  Initiative,
  InitiativeRollup,
  Member,
  Project,
  Workspace,
} from "../lib/types";
import { workspacesKey } from "../lib/useWorkspaces";
import {
  makeQueryClient,
  stubMatchMedia,
} from "../components/shell/test-utils";

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

const initiative: Initiative = {
  id: "init-1",
  workspace_id: "w1",
  name: "Q4 Platform",
  description: "Platform bets for Q4",
  status: "active",
  project_count: 2,
  created_at: "2026-10-01T00:00:00Z",
  updated_at: "2026-10-01T00:00:00Z",
};

const rollup: InitiativeRollup = {
  initiative_id: "init-1",
  total: 40,
  done_count: 30,
  completion_pct: 75,
  by_group: { completed: 30, started: 10 },
  projects: [],
};

const members: Member[] = [
  { id: "u1", name: "Ada", email: "ada@example.com", role: 15 },
  { id: "u2", name: null, email: "bo@example.com", role: 15 },
];

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
    parallel_cycles: false,
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
    parallel_cycles: false,
  },
];

/** Exposes the live query string so tests can assert on the URL state. */
function SearchProbe() {
  const [sp] = useSearchParams();
  return <div data-testid="search-probe">{sp.toString()}</div>;
}

function probeText(): string {
  return screen.getByTestId("search-probe").textContent ?? "";
}

function renderPage(opts: {
  entry?: string;
  initiatives?: Initiative[];
  initiativesKey?: string;
}) {
  const entry = opts.entry ?? "/w/acme/initiatives";
  const initiatives = opts.initiatives ?? [initiative];
  const initiativesKey = opts.initiativesKey ?? "sort=created_at&order=desc";
  const seeds: [readonly unknown[], unknown][] = [
    [["initiatives", "acme", initiativesKey], initiatives],
    [["workspace", "acme"], workspace],
    [["projects", "acme"], projects],
    [["members", "acme"], members],
    [["initiative-rollup", "acme", initiative.id], rollup],
    [workspacesKey, [workspace]],
  ];
  const qc = makeQueryClient(seeds);
  return render(
    <MemoryRouter initialEntries={[entry]}>
      <QueryClientProvider client={qc}>
        <Routes>
          <Route
            path="/w/:slug/initiatives"
            element={
              <>
                <SearchProbe />
                <Initiatives />
              </>
            }
          />
        </Routes>
      </QueryClientProvider>
    </MemoryRouter>,
  );
}

function openFilterPopover() {
  fireEvent.click(screen.getByRole("button", { name: /filters/i }));
}

beforeEach(() => {
  stubMatchMedia(false);
});

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  vi.useRealTimers();
});

describe("initiativeFilters serialization", () => {
  it("serializes statuses as repeatable status params", () => {
    const f: InitiativeFilters = {
      ...EMPTY_INITIATIVE_FILTERS,
      statuses: ["active", "completed"],
    };
    const p = serializeInitiativeFilters(f);
    expect(p.getAll("status")).toEqual(["active", "completed"]);
  });

  it("omits empty dimensions and trims q", () => {
    const p = serializeInitiativeFilters({
      ...EMPTY_INITIATIVE_FILTERS,
      q: "  platform  ",
    });
    expect(p.toString()).toBe("q=platform");
    expect(serializeInitiativeFilters(EMPTY_INITIATIVE_FILTERS).toString()).toBe("");
  });

  it("drops unknown statuses on parse, keeps vocabulary order", () => {
    const f = parseInitiativeFilters(
      new URLSearchParams("status=bogus&status=completed&status=active&status=completed"),
    );
    expect(f.statuses).toEqual(["active", "completed"]);
  });

  it("falls back to newest on an unknown sort value", () => {
    const f = parseInitiativeFilters(new URLSearchParams("sort=bogus"));
    expect(f.sort).toBe("newest");
  });

  it("drops impossible dates on parse", () => {
    const f = parseInitiativeFilters(
      new URLSearchParams("created_from=2026-13-99&created_to=2026-10-05"),
    );
    expect(f.createdAfter).toBe("");
    expect(f.createdBefore).toBe("2026-10-05");
  });

  it("round-trips through serialize/parse", () => {
    const f: InitiativeFilters = {
      statuses: ["archived", "active"],
      q: "platform",
      createdBy: "u1",
      createdAfter: "2026-09-01",
      createdBefore: "2026-10-01",
      projectId: "p1",
      sort: "most_projects",
    };
    const back = parseInitiativeFilters(serializeInitiativeFilters(f));
    expect(back).toEqual({ ...f, statuses: ["active", "archived"] });
  });

  it("maps UI sort options to backend sort/order params", () => {
    const cases: [InitiativeFilters["sort"], string, string][] = [
      ["newest", "created_at", "desc"],
      ["oldest", "created_at", "asc"],
      ["name", "name", "asc"],
      ["most_projects", "project_count", "desc"],
    ];
    for (const [ui, sort, order] of cases) {
      const p = toInitiativeApiParams({ ...EMPTY_INITIATIVE_FILTERS, sort: ui });
      expect(p.get("sort")).toBe(sort);
      expect(p.get("order")).toBe(order);
    }
  });

  it("sends dates as RFC3339, with created_to covering the whole day", () => {
    const p = toInitiativeApiParams({
      ...EMPTY_INITIATIVE_FILTERS,
      createdAfter: "2026-10-01",
      createdBefore: "2026-10-05",
    });
    expect(p.get("created_from")).toBe("2026-10-01T00:00:00Z");
    expect(p.get("created_to")).toBe("2026-10-06T00:00:00Z");
  });

  it("counts active filter dimensions for the badge", () => {
    expect(initiativeActiveFilterCount(EMPTY_INITIATIVE_FILTERS)).toBe(0);
    const f: InitiativeFilters = {
      statuses: ["active", "archived"],
      q: "x",
      createdBy: "u1",
      createdAfter: "2026-10-01",
      createdBefore: "",
      projectId: "p1",
      sort: "name",
    };
    // statuses(1) + q(1) + createdBy(1) + created range(1) + projectId(1) + sort(1)
    expect(initiativeActiveFilterCount(f)).toBe(6);
  });
});

describe("Initiatives filter bar", () => {
  it("toggles status chips on and off, writing repeatable params", () => {
    renderPage({});
    openFilterPopover();
    const active = screen.getByRole("button", { name: "Active" });
    expect(active.getAttribute("aria-pressed")).toBe("false");
    fireEvent.click(active);
    expect(probeText()).toContain("status=active");
    fireEvent.click(screen.getByRole("button", { name: "Completed" }));
    expect(probeText()).toContain("status=active");
    expect(probeText()).toContain("status=completed");
    fireEvent.click(screen.getByRole("button", { name: "Active" }));
    expect(probeText()).not.toContain("status=active");
    expect(probeText()).toContain("status=completed");
  });

  it("restores the bar state from the URL (round-trip)", () => {
    renderPage({ entry: "/w/acme/initiatives?status=completed&q=platform" });
    expect(screen.getByLabelText("Search initiatives")).toHaveProperty(
      "value",
      "platform",
    );
    // Badge count on the Filters button (statuses + q = 2).
    expect(
      screen.getByRole("button", { name: /filters/i }).textContent,
    ).toContain("2");
    openFilterPopover();
    expect(
      screen.getByRole("button", { name: "Completed" }).getAttribute("aria-pressed"),
    ).toBe("true");
    expect(
      screen.getByRole("button", { name: "Active" }).getAttribute("aria-pressed"),
    ).toBe("false");
  });

  it("debounces the search input into q (~300ms)", () => {
    renderPage({});
    vi.useFakeTimers();
    fireEvent.change(screen.getByLabelText("Search initiatives"), {
      target: { value: "plat" },
    });
    // Not yet: the debounce hasn't fired.
    expect(probeText()).not.toContain("q=plat");
    act(() => {
      vi.advanceTimersByTime(300);
    });
    expect(probeText()).toContain("q=plat");
  });

  it("picks created-by from the member list", () => {
    renderPage({});
    openFilterPopover();
    fireEvent.click(screen.getByText("Ada"));
    expect(probeText()).toContain("created_by=u1");
    // Clicking again clears the single-select.
    fireEvent.click(screen.getByText("Ada"));
    expect(probeText()).not.toContain("created_by");
  });

  it("picks the member project, feeding project_id", () => {
    renderPage({});
    openFilterPopover();
    fireEvent.click(screen.getByText("Engineering"));
    expect(probeText()).toContain("project_id=p1");
  });

  it("sets the created date range as YYYY-MM-DD params", () => {
    renderPage({});
    openFilterPopover();
    fireEvent.change(screen.getByLabelText("Created from"), {
      target: { value: "2026-09-01" },
    });
    fireEvent.change(screen.getByLabelText("Created to"), {
      target: { value: "2026-10-01" },
    });
    expect(probeText()).toContain("created_from=2026-09-01");
    expect(probeText()).toContain("created_to=2026-10-01");
  });

  it("maps the sort dropdown to the sort param", () => {
    renderPage({});
    fireEvent.change(screen.getByLabelText("Sort initiatives"), {
      target: { value: "most_projects" },
    });
    expect(probeText()).toContain("sort=most_projects");
  });

  it("clear-all wipes every filter param but keeps the page usable", () => {
    renderPage({ entry: "/w/acme/initiatives?status=active&q=platform" });
    fireEvent.click(screen.getAllByRole("button", { name: /clear all/i })[0]);
    expect(probeText()).toBe("");
    expect(screen.getByLabelText("Search initiatives")).toHaveProperty("value", "");
    // The seeded unfiltered list is back.
    expect(screen.getByText("Q4 Platform")).toBeTruthy();
  });

  it("shows the token-style empty state when filters match nothing", () => {
    renderPage({
      entry: "/w/acme/initiatives?q=zzz",
      initiatives: [],
      initiativesKey: "q=zzz&sort=created_at&order=desc",
    });
    expect(
      screen.getByText("No initiatives match your filters"),
    ).toBeTruthy();
    expect(screen.getByTestId("initiatives-result-count").textContent).toBe(
      "0 results",
    );
    fireEvent.click(screen.getByRole("button", { name: "Clear filters" }));
    expect(probeText()).not.toContain("q=zzz");
  });

  it("shows the result count when filters are active", () => {
    renderPage({
      entry: "/w/acme/initiatives?status=active",
      initiativesKey: "status=active&sort=created_at&order=desc",
    });
    expect(screen.getByTestId("initiatives-result-count").textContent).toBe(
      "1 result",
    );
  });

  it("keeps the filter bar visible for guests", () => {
    const ws = { ...workspace, role: 5 };
    const seeds: [readonly unknown[], unknown][] = [
      [["initiatives", "acme", "sort=created_at&order=desc"], [initiative]],
      [["workspace", "acme"], ws],
      [["projects", "acme"], projects],
      [["members", "acme"], members],
      [["initiative-rollup", "acme", initiative.id], rollup],
      [workspacesKey, [ws]],
    ];
    const qc = makeQueryClient(seeds);
    render(
      <MemoryRouter initialEntries={["/w/acme/initiatives"]}>
        <QueryClientProvider client={qc}>
          <Routes>
            <Route
              path="/w/:slug/initiatives"
              element={
                <>
                  <SearchProbe />
                  <Initiatives />
                </>
              }
            />
          </Routes>
        </QueryClientProvider>
      </MemoryRouter>,
    );
    expect(screen.getByLabelText("Search initiatives")).toBeTruthy();
    expect(screen.getByRole("button", { name: /filters/i })).toBeTruthy();
    expect(screen.queryByText("New initiative")).toBeNull();
  });
});
