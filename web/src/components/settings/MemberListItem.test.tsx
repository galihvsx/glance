// @vitest-environment jsdom
// C80T1: MemberListItem — the Activity affordance visibility (guests
// never see it) and the expandable per-member panel.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import MemberListItem from "./MemberListItem";
import { api } from "../../lib/api";
import type { WorkspaceMember } from "../../lib/types";

const member: WorkspaceMember = {
  id: "member-uuid-1",
  name: "Ada Member",
  email: "ada@example.com",
  role: 15,
};

const getSpy = vi.spyOn(api, "get");

function renderRow(opts: {
  viewerCanViewActivity?: boolean;
  isAdmin?: boolean;
  activityOpen?: boolean;
}) {
  const onToggleActivity = vi.fn();
  const onRoleChange = vi.fn();
  const onRemove = vi.fn();
  getSpy.mockImplementation((path: string) => {
    if (path.includes("/activity")) {
      return Promise.resolve({ activity: [], next_cursor: null });
    }
    return Promise.resolve({ states: [] });
  });
  render(
    <MemoryRouter>
      <ul>
        <MemberListItem
          slug="acme"
          member={member}
          isAdmin={opts.isAdmin ?? true}
          viewerCanViewActivity={opts.viewerCanViewActivity ?? true}
          actingId={null}
          activityOpen={opts.activityOpen ?? false}
          onToggleActivity={onToggleActivity}
          onRoleChange={onRoleChange}
          onRemove={onRemove}
        />
      </ul>
    </MemoryRouter>,
  );
  return { onToggleActivity, onRoleChange, onRemove };
}

// NOTE: reset, not restore — see MemberActivityPanel.test.tsx.
beforeEach(() => {
  getSpy.mockReset();
});

afterEach(() => {
  cleanup();
});

describe("MemberListItem", () => {
  it("shows the Activity affordance when the viewer can read activity", () => {
    renderRow({});
    expect(
      screen.getByRole("button", { name: "Activity for Ada Member" }),
    ).toBeInTheDocument();
  });

  it("hides the Activity affordance for guests/viewers (role < 15)", () => {
    renderRow({ viewerCanViewActivity: false });
    expect(
      screen.queryByRole("button", { name: "Activity for Ada Member" }),
    ).toBeNull();
    // The rest of the row still renders.
    expect(screen.getByText("Ada Member")).toBeInTheDocument();
  });

  it("calls onToggleActivity when the Activity button is clicked", () => {
    const { onToggleActivity } = renderRow({});
    fireEvent.click(
      screen.getByRole("button", { name: "Activity for Ada Member" }),
    );
    expect(onToggleActivity).toHaveBeenCalledTimes(1);
  });

  it("renders the expanded panel when activityOpen and the viewer may view", async () => {
    renderRow({ activityOpen: true });
    expect(await screen.findByText("No activity yet.")).toBeInTheDocument();
    expect(getSpy).toHaveBeenCalledWith(
      "/api/v1/workspaces/acme/activity?limit=20&actor=member-uuid-1",
    );
  });

  it("does not render the panel when activityOpen is false", () => {
    renderRow({ activityOpen: false });
    expect(screen.queryByText("No activity yet.")).toBeNull();
    expect(
      getSpy.mock.calls.some(([path]) => (path as string).includes("/activity")),
    ).toBe(false);
  });

  it("never renders the panel for a guest even if activityOpen", () => {
    renderRow({ viewerCanViewActivity: false, activityOpen: true });
    expect(screen.queryByText("No activity yet.")).toBeNull();
    expect(
      getSpy.mock.calls.some(([path]) => (path as string).includes("/activity")),
    ).toBe(false);
  });

  it("keeps the admin role controls alongside the affordance", () => {
    renderRow({ isAdmin: true });
    expect(
      screen.getByRole("combobox", { name: "Role for Ada Member" }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "Activity for Ada Member" }),
    ).toBeInTheDocument();
  });

  it("renders a role badge for non-admins", () => {
    renderRow({ isAdmin: false });
    expect(screen.getByText("Member")).toBeInTheDocument();
  });
});
