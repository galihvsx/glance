// @vitest-environment jsdom
// C74T1: filter-param serialization + filter bar tests for the Epics
// page, mirroring the C73 InitiativesFilters.test.tsx anatomy.
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
import Epics from "./Epics";
import {
  EMPTY_EPIC_FILTERS,
  epicActiveFilterCount,
  parseEpicFilters,
  serializeEpicFilters,
  toEpicApiParams,
  type EpicFilters,
} from "../lib/epicFilters";
import type {
  Epic,
  Initiative,
  Member,
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

const epic: Epic = {
  id: "epic-1",
  project_id: "p1",
  name: "Auth revamp",
  description: "Passwordless OTP + OAuth",
  archived_at: null,
  issue_count: 12,
  progress: {
    total: 12,
    by_group: { backlog: 2, unstarted: 3, started: 4, completed: 3, cancelled: 0 },
  },
  created_at: "2026-10-10T00:00:00Z",
  updated_at: "2026-10-10T00:00:00Z",
};

const members: Member[] = [
  { id: "u1", name: "Ada", email: "ada@example.com", role: 15 },
  { id: "u2", name: null, email: "bo@example.com", role: 15 },
];

const initiatives: Initiative[] = [
  {
    id: "init-1",
    workspace_id: "w1",
    name: "Q4 Platform",
    description: "",
    status: "active",
    project_count: 2,
    created_at: "2026-10-01T00:00:00Z",
    updated_at: "2026-10-01T00:00:00Z",
  },
  {
    id: "init-2",
    workspace_id: "w1",
    name: "Mobile",
    description: "",
    status: "archived",
    project_count: 1,
    created_at: "2026-09-01T00:00:00Z",
    updated_at: "2026-09-01T00:00:00Z",
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
  epics?: Epic[];
  epicsKey?: string;
  workspaceRole?: number;
}) {
  const entry = opts.entry ?? "/w/acme/p/ENG/epics";
  const epics = opts.epics ?? [epic];
  const epicsKey = opts.epicsKey ?? "sort=created_at&order=desc";
  const ws = { ...workspace, role: opts.workspaceRole ?? 15 };
  const seeds: [readonly unknown[], unknown][] = [
    [["epics", "acme", "ENG", epicsKey], epics],
    [["workspace", "acme"], ws],
    [["members", "acme"], members],
    [["initiatives", "acme"], initiatives],
    [["epic-detail", "acme", "ENG", epic.id], epic],
    [workspacesKey, [ws]],
  ];
  const qc = makeQueryClient(seeds);
  return render(
    <MemoryRouter initialEntries={[entry]}>
      <QueryClientProvider client={qc}>
        <Routes>
          <Route
            path="/w/:slug/p/:identifier/epics"
            element={
              <>
                <SearchProbe />
                <Epics />
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

describe("epicFilters serialization", () => {
  it("serializes statuses as repeatable status params", () => {
    const f: EpicFilters = {
      ...EMPTY_EPIC_FILTERS,
      statuses: ["active", "archived"],
    };
    const p = serializeEpicFilters(f);
    expect(p.getAll("status")).toEqual(["active", "archived"]);
  });

  it("omits empty dimensions and trims q", () => {
    const p = serializeEpicFilters({
      ...EMPTY_EPIC_FILTERS,
      q: "  auth  ",
    });
    expect(p.toString()).toBe("q=auth");
    expect(serializeEpicFilters(EMPTY_EPIC_FILTERS).toString()).toBe("");
  });

  it("drops unknown statuses on parse, keeps vocabulary order", () => {
    const f = parseEpicFilters(
      new URLSearchParams("status=bogus&status=archived&status=active&status=active"),
    );
    expect(f.statuses).toEqual(["active", "archived"]);
  });

  it("falls back to newest on an unknown sort value", () => {
    const f = parseEpicFilters(new URLSearchParams("sort=bogus"));
    expect(f.sort).toBe("newest");
  });

  it("drops impossible dates on parse", () => {
    const f = parseEpicFilters(
      new URLSearchParams("created_from=2026-13-99&created_to=2026-10-05"),
    );
    expect(f.createdAfter).toBe("");
    expect(f.createdBefore).toBe("2026-10-05");
  });

  it("round-trips through serialize/parse", () => {
    const f: EpicFilters = {
      statuses: ["archived", "active"],
      q: "auth",
      createdBy: "u1",
      createdAfter: "2026-09-01",
      createdBefore: "2026-10-01",
      initiativeId: "init-1",
      sort: "most_issues",
    };
    const back = parseEpicFilters(serializeEpicFilters(f));
    expect(back).toEqual({ ...f, statuses: ["active", "archived"] });
  });

  it("maps UI sort options to backend sort/order params", () => {
    const cases: [EpicFilters["sort"], string, string][] = [
      ["newest", "created_at", "desc"],
      ["oldest", "created_at", "asc"],
      ["name", "name", "asc"],
      ["most_issues", "issue_count", "desc"],
    ];
    for (const [ui, sort, order] of cases) {
      const p = toEpicApiParams({ ...EMPTY_EPIC_FILTERS, sort: ui });
      expect(p.get("sort")).toBe(sort);
      expect(p.get("order")).toBe(order);
    }
  });

  it("sends dates as RFC3339, with created_to covering the whole day", () => {
    const p = toEpicApiParams({
      ...EMPTY_EPIC_FILTERS,
      createdAfter: "2026-10-01",
      createdBefore: "2026-10-05",
    });
    expect(p.get("created_from")).toBe("2026-10-01T00:00:00Z");
    expect(p.get("created_to")).toBe("2026-10-06T00:00:00Z");
  });

  it("counts active filter dimensions for the badge", () => {
    expect(epicActiveFilterCount(EMPTY_EPIC_FILTERS)).toBe(0);
    const f: EpicFilters = {
      statuses: ["active", "archived"],
      q: "x",
      createdBy: "u1",
      createdAfter: "2026-10-01",
      createdBefore: "",
      initiativeId: "init-1",
      sort: "name",
    };
    // statuses(1) + q(1) + createdBy(1) + created range(1) +
    // initiativeId(1) + sort(1)
    expect(epicActiveFilterCount(f)).toBe(6);
  });
});

describe("Epics filter bar", () => {
  it("toggles status chips on and off, writing repeatable params", () => {
    renderPage({});
    openFilterPopover();
    const archived = screen.getByRole("button", { name: "Archived" });
    expect(archived.getAttribute("aria-pressed")).toBe("false");
    fireEvent.click(archived);
    expect(probeText()).toContain("status=archived");
    fireEvent.click(screen.getByRole("button", { name: "Active" }));
    expect(probeText()).toContain("status=active");
    expect(probeText()).toContain("status=archived");
    fireEvent.click(screen.getByRole("button", { name: "Archived" }));
    expect(probeText()).not.toContain("status=archived");
    expect(probeText()).toContain("status=active");
  });

  it("restores the bar state from the URL (round-trip)", () => {
    renderPage({ entry: "/w/acme/p/ENG/epics?status=archived&q=auth" });
    expect(screen.getByLabelText("Search epics")).toHaveProperty(
      "value",
      "auth",
    );
    // Badge count on the Filters button (statuses + q = 2).
    expect(
      screen.getByRole("button", { name: /filters/i }).textContent,
    ).toContain("2");
    openFilterPopover();
    expect(
      screen.getByRole("button", { name: "Archived" }).getAttribute("aria-pressed"),
    ).toBe("true");
    expect(
      screen.getByRole("button", { name: "Active" }).getAttribute("aria-pressed"),
    ).toBe("false");
  });

  it("debounces the search input into q (~300ms)", () => {
    renderPage({});
    vi.useFakeTimers();
    fireEvent.change(screen.getByLabelText("Search epics"), {
      target: { value: "auth" },
    });
    // Not yet: the debounce hasn't fired.
    expect(probeText()).not.toContain("q=auth");
    act(() => {
      vi.advanceTimersByTime(300);
    });
    expect(probeText()).toContain("q=auth");
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

  it("picks the linked initiative, feeding initiative_id", () => {
    renderPage({});
    openFilterPopover();
    fireEvent.click(screen.getByText("Q4 Platform"));
    expect(probeText()).toContain("initiative_id=init-1");
    // Clicking again clears the single-select.
    fireEvent.click(screen.getByText("Q4 Platform"));
    expect(probeText()).not.toContain("initiative_id");
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
    fireEvent.change(screen.getByLabelText("Sort epics"), {
      target: { value: "most_issues" },
    });
    expect(probeText()).toContain("sort=most_issues");
  });

  it("clear-all wipes every filter param but keeps the page usable", () => {
    renderPage({ entry: "/w/acme/p/ENG/epics?status=active&q=auth" });
    fireEvent.click(screen.getAllByRole("button", { name: /clear all/i })[0]);
    expect(probeText()).toBe("");
    expect(screen.getByLabelText("Search epics")).toHaveProperty("value", "");
    // The seeded unfiltered list is back.
    expect(screen.getByText("Auth revamp")).toBeTruthy();
  });

  it("shows the filter-aware empty state when filters match nothing", () => {
    renderPage({
      entry: "/w/acme/p/ENG/epics?q=zzz",
      epics: [],
      epicsKey: "q=zzz&sort=created_at&order=desc",
    });
    expect(
      screen.getByText("No epics match your filters"),
    ).toBeTruthy();
    expect(screen.getByTestId("epics-result-count").textContent).toBe(
      "0 results",
    );
    fireEvent.click(screen.getByRole("button", { name: "Clear filters" }));
    expect(probeText()).not.toContain("q=zzz");
  });

  it("shows the result count when filters are active", () => {
    renderPage({
      entry: "/w/acme/p/ENG/epics?status=active",
      epicsKey: "status=active&sort=created_at&order=desc",
    });
    expect(screen.getByTestId("epics-result-count").textContent).toBe(
      "1 result",
    );
  });

  it("keeps the filter bar visible for guests but hides create", () => {
    renderPage({ workspaceRole: 5 });
    expect(screen.getByLabelText("Search epics")).toBeTruthy();
    expect(screen.getByRole("button", { name: /filters/i })).toBeTruthy();
    expect(screen.queryByText("New epic")).toBeNull();
  });
});
