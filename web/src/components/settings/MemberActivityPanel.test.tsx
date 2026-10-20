// @vitest-environment jsdom
// C80T1: per-member activity panel — actor fetch, cursor pagination,
// empty and error states.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import MemberActivityPanel from "./MemberActivityPanel";
import { api, ApiError } from "../../lib/api";
import type { ActivityEntry } from "../../lib/activity";
import type { WorkspaceMember } from "../../lib/types";

const member: WorkspaceMember = {
  id: "member-uuid-1",
  name: "Ada Member",
  email: "ada@example.com",
  role: 15,
};

function entry(over: Partial<ActivityEntry> = {}): ActivityEntry {
  return {
    at: "2026-10-20T08:00:00Z",
    actor: "Ada Member",
    issue_uuid: "issue-uuid-1",
    issue_identifier: "ACME-1",
    field: "_created",
    old: null,
    new: null,
    project_identifier: "ACME",
    project_name: "Acme",
    ...over,
  };
}

const getSpy = vi.spyOn(api, "get");

/** Route api.get by path: feed pages for /activity, states for /states. */
function mockFeed(pages: { activity: ActivityEntry[]; next_cursor: string | null }[]) {
  let n = 0;
  getSpy.mockImplementation((path: string) => {
    if (path.includes("/activity")) {
      const page = pages[Math.min(n, pages.length - 1)];
      n++;
      return Promise.resolve(page);
    }
    if (path.includes("/states")) {
      return Promise.resolve({ states: [] });
    }
    return Promise.reject(new Error("unexpected path: " + path));
  });
  return () => n;
}

function renderPanel() {
  render(
    <MemoryRouter>
      <MemberActivityPanel slug="acme" member={member} />
    </MemoryRouter>,
  );
}

// NOTE: the module-level spy is reset (not restored) between tests —
// vi.restoreAllMocks() would disconnect it from the api object and
// later mockImplementation calls would silently not apply.
beforeEach(() => {
  getSpy.mockReset();
});

afterEach(() => {
  cleanup();
});

describe("MemberActivityPanel", () => {
  it("fetches the first page with the member's actor id", async () => {
    mockFeed([{ activity: [], next_cursor: null }]);
    renderPanel();
    await screen.findByText("No activity yet.");
    expect(getSpy).toHaveBeenCalledWith(
      "/api/v1/workspaces/acme/activity?limit=20&actor=member-uuid-1",
    );
  });

  it("renders the member's activity rows with the shared feed renderer", async () => {
    mockFeed([{ activity: [entry()], next_cursor: null }]);
    renderPanel();
    expect(await screen.findByText("Ada Member")).toBeInTheDocument();
    expect(screen.getByText("created the issue")).toBeInTheDocument();
    expect(screen.getByText("ACME-1")).toBeInTheDocument();
    expect(screen.getByText("Acme")).toBeInTheDocument();
  });

  it("shows Load more with a cursor and appends the next page", async () => {
    mockFeed([
      { activity: [entry({ issue_identifier: "ACME-1" })], next_cursor: "cursor-1" },
      { activity: [entry({ issue_identifier: "ACME-2", at: "2026-10-20T07:00:00Z" })], next_cursor: null },
    ]);
    renderPanel();
    await screen.findByText("ACME-1");
    fireEvent.click(screen.getByRole("button", { name: "Load more" }));
    await screen.findByText("ACME-2");
    expect(getSpy).toHaveBeenCalledWith(
      "/api/v1/workspaces/acme/activity?limit=20&actor=member-uuid-1&before=cursor-1",
    );
    // First page rows are kept.
    expect(screen.getByText("ACME-1")).toBeInTheDocument();
    // Feed exhausted: no more Load more.
    expect(screen.queryByRole("button", { name: "Load more" })).toBeNull();
  });

  it("hides Load more when the feed is exhausted on the first page", async () => {
    mockFeed([{ activity: [entry()], next_cursor: null }]);
    renderPanel();
    await screen.findByText("ACME-1");
    expect(screen.queryByRole("button", { name: "Load more" })).toBeNull();
  });

  it("shows the empty state when the member has no activity", async () => {
    mockFeed([{ activity: [], next_cursor: null }]);
    renderPanel();
    expect(await screen.findByText("No activity yet.")).toBeInTheDocument();
  });

  it("shows a graceful message on a 403 (role lost feed access)", async () => {
    getSpy.mockImplementation((path: string) => {
      if (path.includes("/activity")) {
        return Promise.reject(new ApiError(403, "forbidden", "forbidden"));
      }
      return Promise.resolve({ states: [] });
    });
    renderPanel();
    expect(
      await screen.findByText("Activity history isn't available at your role."),
    ).toBeInTheDocument();
  });

  it("shows the backend message on a generic failure", async () => {
    getSpy.mockImplementation((path: string) => {
      if (path.includes("/activity")) {
        return Promise.reject(new Error("network down"));
      }
      return Promise.resolve({ states: [] });
    });
    renderPanel();
    expect(await screen.findByText("network down")).toBeInTheDocument();
  });

  it("resolves state names per project like the home feed", async () => {
    const stateId = "state-uuid-9";
    getSpy.mockImplementation((path: string) => {
      if (path.includes("/activity")) {
        return Promise.resolve({
          activity: [
            entry({ field: "state_id", old: null, new: `"${stateId}"` }),
          ],
          next_cursor: null,
        });
      }
      if (path.includes("/states")) {
        expect(path).toContain("/projects/ACME/states");
        return Promise.resolve({
          states: [{ id: stateId, name: "In Progress" }],
        });
      }
      return Promise.reject(new Error("unexpected path: " + path));
    });
    renderPanel();
    // "set state to In Progress" — the resolved name, not the raw UUID.
    expect(await screen.findByText("In Progress")).toBeInTheDocument();
  });
});
