// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from "vitest";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import Epics from "./Epics";
import { api } from "../lib/api";
import type { Epic } from "../lib/types";
import { makeQueryClient } from "../components/shell/test-utils";

const BASE = "/api/v1/workspaces/acme/projects/ENG/epics";

const epicActive: Epic = {
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

const epicArchived: Epic = {
  id: "epic-2",
  project_id: "p1",
  name: "Legacy import tools",
  description: "CSV importers",
  archived_at: "2026-10-02T00:00:00Z",
  issue_count: 5,
  progress: {
    total: 5,
    by_group: { backlog: 0, unstarted: 1, started: 1, completed: 2, cancelled: 1 },
  },
  created_at: "2026-09-01T00:00:00Z",
  updated_at: "2026-10-02T00:00:00Z",
};

function renderList(opts: {
  epics?: Epic[];
  includeArchived?: boolean;
  detailById?: Record<string, Epic>;
} = {}) {
  const epics = opts.epics ?? [epicActive];
  const detailById = opts.detailById ?? { "epic-1": epicActive };
  const get = vi.spyOn(api, "get").mockImplementation((path: string) => {
    if (path.includes("/epics?") || path.endsWith("/epics")) {
      return Promise.resolve({
        epics: path.includes("include_archived") ? [epicActive, epicArchived] : epics,
      });
    }
    const m = /\/epics\/([^/?]+)$/.exec(path);
    if (m && detailById[m[1]]) return Promise.resolve(detailById[m[1]]);
    return Promise.reject(new Error(`unexpected GET ${path}`));
  });
  const seeds: [readonly unknown[], unknown][] = [
    [["epics", "acme", "ENG", false], epics],
  ];
  for (const [id, e] of Object.entries(detailById)) {
    seeds.push([["epic-detail", "acme", "ENG", id], e]);
  }
  const qc = makeQueryClient(seeds);
  render(
    <MemoryRouter initialEntries={["/w/acme/p/ENG/epics"]}>
      <QueryClientProvider client={qc}>
        <Routes>
          <Route path="/w/:slug/p/:identifier/epics" element={<Epics />} />
          <Route
            path="/w/:slug/p/:identifier/epics/:epicID"
            element={<div>epic detail</div>}
          />
        </Routes>
      </QueryClientProvider>
    </MemoryRouter>,
  );
  return { get };
}

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

describe("Epics list", () => {
  it("renders epic cards with names and issue counts", () => {
    renderList();
    expect(screen.getByText("Auth revamp")).toBeTruthy();
    expect(document.body.textContent).toContain("12 issues");
  });

  it("shows a progress skeleton while the per-card detail loads", () => {
    // Seed the list but not the detail queries: the card fetches
    // GET .../epics/:id for by_group with a skeleton while pending.
    const get = vi
      .spyOn(api, "get")
      .mockImplementation((path: string) =>
        path.endsWith("/epics")
          ? Promise.resolve({ epics: [epicActive] })
          : new Promise(() => {}),
      );
    const qc = makeQueryClient([[["epics", "acme", "ENG", false], [epicActive]]]);
    render(
      <MemoryRouter initialEntries={["/w/acme/p/ENG/epics"]}>
        <QueryClientProvider client={qc}>
          <Routes>
            <Route path="/w/:slug/p/:identifier/epics" element={<Epics />} />
          </Routes>
        </QueryClientProvider>
      </MemoryRouter>,
    );
    expect(
      document.querySelector('[data-testid="epic-progress-skeleton"]'),
    ).toBeTruthy();
    get.mockRestore();
  });

  it("renders the by_group progress once the detail resolves", () => {
    renderList();
    // Legend chips from the C71 spec: fixed 5-group order.
    expect(screen.getByText("Backlog")).toBeTruthy();
    expect(screen.getByText("Cancelled")).toBeTruthy();
    expect(
      screen.getByRole("progressbar", { name: "Auth revamp progress" }),
    ).toBeTruthy();
  });

  it("hides archived epics until the toggle is on, then refetches with the flag", async () => {
    const { get } = renderList();
    expect(screen.queryByText("Legacy import tools")).toBeNull();

    fireEvent.click(screen.getByText("Show archived"));

    await waitFor(() =>
      expect(screen.getByText("Legacy import tools")).toBeTruthy(),
    );
    expect(get).toHaveBeenCalledWith(`${BASE}?include_archived=true`);
    // Archived card carries the muted chip and is dimmed.
    const card = screen.getByRole("button", {
      name: /open epic legacy import tools/i,
    });
    expect(within(card).getByText("Archived")).toBeTruthy();
    expect(card.className).toContain("opacity-55");
  });

  it("filters epics by search", () => {
    renderList({ epics: [epicActive, epicArchived] });
    expect(screen.getByText("Legacy import tools")).toBeTruthy();
    fireEvent.change(screen.getByLabelText("Search epics"), {
      target: { value: "auth" },
    });
    expect(screen.getByText("Auth revamp")).toBeTruthy();
    expect(screen.queryByText("Legacy import tools")).toBeNull();
  });

  it("navigates to the detail route when a card is clicked", () => {
    renderList();
    fireEvent.click(
      screen.getByRole("button", { name: /open epic auth revamp/i }),
    );
    expect(screen.getByText("epic detail")).toBeTruthy();
  });
});

describe("Epics create flow", () => {
  it("posts name + description and navigates to the new epic", async () => {
    const post = vi.spyOn(api, "post").mockResolvedValue({ ...epicActive, id: "epic-9" });
    renderList();

    fireEvent.click(screen.getByText("New epic"));
    fireEvent.change(screen.getByLabelText("Name"), {
      target: { value: "Mobile offline" },
    });
    fireEvent.change(screen.getByLabelText("Description"), {
      target: { value: "Queue mutations offline" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Create epic" }));

    await waitFor(() => expect(post).toHaveBeenCalledTimes(1));
    expect(post).toHaveBeenCalledWith(BASE, {
      name: "Mobile offline",
      description: "Queue mutations offline",
    });
    await waitFor(() => expect(screen.getByText("epic detail")).toBeTruthy());
  });

  it("omits the description when left empty", async () => {
    const post = vi.spyOn(api, "post").mockResolvedValue({ ...epicActive, id: "epic-9" });
    renderList();

    fireEvent.click(screen.getByText("New epic"));
    fireEvent.change(screen.getByLabelText("Name"), { target: { value: "Solo" } });
    fireEvent.click(screen.getByRole("button", { name: "Create epic" }));

    await waitFor(() => expect(post).toHaveBeenCalledTimes(1));
    expect(post).toHaveBeenCalledWith(BASE, { name: "Solo" });
  });

  it("keeps the create button disabled until a name is entered", () => {
    renderList();
    fireEvent.click(screen.getByText("New epic"));
    expect(screen.getByRole("button", { name: "Create epic" })).toHaveProperty(
      "disabled",
      true,
    );
  });
});
