// @vitest-environment jsdom
// C92T1: Project Settings Members tab — direct roster with editable
// roles, teamspace-derived rows read-only, non-admin read-only view,
// add / role-change / remove flows, last-admin guard, and the
// no-oracle error state (a 404 renders a generic load error, never a
// "private" hint or a member leak).

import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it, vi, type Mock } from "vitest";
import ProjectMembersSection from "./ProjectMembersSection";
import { api, ApiError } from "../../lib/api";
import { makeQueryClient } from "../shell/test-utils";
import type { ProjectMember, Teamspace, TeamspaceMember, WorkspaceMember } from "../../lib/types";

vi.mock("../../lib/api", async (importOriginal) => {
  const mod = await importOriginal<typeof import("../../lib/api")>();
  return {
    ...mod,
    api: {
      get: vi.fn(),
      post: vi.fn(),
      put: vi.fn(),
      patch: vi.fn(),
      del: vi.fn(),
    },
  };
});

const get = () => api.get as unknown as Mock;
const post = () => api.post as unknown as Mock;
const patch = () => api.patch as unknown as Mock;
const del = () => api.del as unknown as Mock;

let roster: ProjectMember[];
let teamspaceRoster: TeamspaceMember[];
let workspaceMembers: WorkspaceMember[];
let rosterError: Error | null;

const teamspaces: Teamspace[] = [
  { id: "ts-mobile", workspace_id: "w1", name: "Mobile", project_count: 1, created_at: "", updated_at: "" },
];

function installApiMocks() {
  get().mockImplementation((url: string) => {
    // Teamspace roster URL contains /teamspaces/ AND /members —
    // check it before the workspace-members URL.
    if (url.includes("/teamspaces/") && url.includes("/members")) {
      return Promise.resolve({ members: teamspaceRoster });
    }
    if (url.includes("/teamspaces")) return Promise.resolve({ teamspaces });
    if (url.includes("/projects/") && url.includes("/members")) {
      if (rosterError) return Promise.reject(rosterError);
      return Promise.resolve({ members: roster });
    }
    if (url.includes("/projects/")) {
      return Promise.resolve({
        id: "p-eng",
        workspace_id: "w1",
        identifier: "ENG",
        name: "Engineering",
        teamspace_id: "ts-mobile",
        parallel_cycles: false,
        created_at: "",
        updated_at: "",
      });
    }
    if (url.includes("/members")) return Promise.resolve({ members: workspaceMembers });
    return Promise.resolve({});
  });
  post().mockImplementation((_url: string, body: { user_id: string; role: number }) => {
    const m = workspaceMembers.find((w) => w.id === body.user_id)!;
    const row: ProjectMember = { user_id: m.id, name: m.name, email: m.email, role: body.role };
    roster = [...roster, row];
    return Promise.resolve(row);
  });
  patch().mockImplementation((url: string, body: { role: number }) => {
    const id = url.split("/").pop()!;
    roster = roster.map((m) => (m.user_id === id ? { ...m, role: body.role } : m));
    return Promise.resolve(roster.find((m) => m.user_id === id));
  });
  del().mockImplementation((url: string) => {
    const id = url.split("/").pop()!;
    roster = roster.filter((m) => m.user_id !== id);
    return Promise.resolve({ ok: true });
  });
}

function renderSection(props: { isWorkspaceAdmin: boolean; currentUserId?: string | null }) {
  render(
    <QueryClientProvider client={makeQueryClient()}>
      <ProjectMembersSection slug="acme" identifier="ENG" {...props} />
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  roster = [
    { user_id: "u-rani", name: "Rani", email: "rani@example.com", role: 20 },
    { user_id: "u-budi", name: "Budi", email: "budi@example.com", role: 15 },
  ];
  teamspaceRoster = [
    { id: "u-budi", name: "Budi", email: "budi@example.com" },
    { id: "u-sari", name: "Sari", email: "sari@example.com" },
  ];
  workspaceMembers = [
    { id: "u-rani", name: "Rani", email: "rani@example.com", role: 20 },
    { id: "u-budi", name: "Budi", email: "budi@example.com", role: 15 },
    { id: "u-sari", name: "Sari", email: "sari@example.com", role: 15 },
    { id: "u-dedi", name: "Dedi", email: "dedi@example.com", role: 15 },
  ];
  rosterError = null;
  installApiMocks();
});

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

describe("Members tab (C92T1)", () => {
  it("admin sees direct rows editable + derived rows read-only", async () => {
    renderSection({ isWorkspaceAdmin: true, currentUserId: "u-rani" });
    await screen.findByText("Rani");
    expect(screen.getByText("Budi")).toBeTruthy();
    // Sari is a teamspace member without a direct row: derived,
    // marked with the teamspace name, no role select / Remove.
    await screen.findByText(/via teamspace “Mobile”/);
    expect(screen.getByText("Derived access — manage in the teamspace")).toBeTruthy();
    expect(screen.getAllByText("Remove")).toHaveLength(2);
    expect(screen.getByLabelText("Add member")).toBeTruthy();
  });

  it("non-admin sees the roster read-only", async () => {
    renderSection({ isWorkspaceAdmin: false, currentUserId: "u-budi" });
    await screen.findByText("Rani");
    expect(screen.queryByText("Remove")).toBeNull();
    expect(screen.queryByLabelText("Add member")).toBeNull();
    expect(screen.queryByLabelText(/Role for/)).toBeNull();
    expect(
      screen.getByText("Only project admins can add, remove, or change member roles."),
    ).toBeTruthy();
  });

  it("a direct project Admin (not workspace Admin) can manage", async () => {
    renderSection({ isWorkspaceAdmin: false, currentUserId: "u-rani" });
    await screen.findByText("Rani");
    expect(screen.getAllByText("Remove")).toHaveLength(2);
    expect(screen.getByLabelText("Add member")).toBeTruthy();
  });

  it("add member posts user_id + role and refreshes the roster", async () => {
    renderSection({ isWorkspaceAdmin: true, currentUserId: "u-rani" });
    await screen.findByText("Rani");
    fireEvent.change(screen.getByLabelText("Add member"), {
      target: { value: "u-dedi" },
    });
    fireEvent.change(screen.getByLabelText("Role for new member"), {
      target: { value: "5" },
    });
    fireEvent.click(screen.getByText("Add"));
    await waitFor(() =>
      expect(post()).toHaveBeenCalledWith(
        expect.stringContaining("/projects/ENG/members"),
        { user_id: "u-dedi", role: 5 },
      ),
    );
    await screen.findByText("Dedi");
  });

  it("role change patches the member URL", async () => {
    renderSection({ isWorkspaceAdmin: true, currentUserId: "u-rani" });
    await screen.findByText("Rani");
    fireEvent.change(screen.getByLabelText("Role for Budi"), {
      target: { value: "5" },
    });
    await waitFor(() =>
      expect(patch()).toHaveBeenCalledWith(
        expect.stringContaining("/projects/ENG/members/u-budi"),
        { role: 5 },
      ),
    );
  });

  it("remove drops the row", async () => {
    renderSection({ isWorkspaceAdmin: true, currentUserId: "u-rani" });
    await screen.findByText("Rani");
    const removeButtons = screen.getAllByText("Remove");
    fireEvent.click(removeButtons[1]);
    await waitFor(() =>
      expect(del()).toHaveBeenCalledWith(
        expect.stringContaining("/projects/ENG/members/u-budi"),
      ),
    );
  });

  it("the last direct admin cannot be removed or demoted", async () => {
    roster = [{ user_id: "u-rani", name: "Rani", email: "rani@example.com", role: 20 }];
    renderSection({ isWorkspaceAdmin: true, currentUserId: "u-rani" });
    await screen.findByText("Rani");
    expect(
      screen.getByText(/Last project admin — promote another admin/),
    ).toBeTruthy();
    const removeButton = screen.getByText("Remove") as HTMLButtonElement;
    expect(removeButton.disabled).toBe(true);
    const roleSelect = screen.getByLabelText("Role for Rani") as HTMLSelectElement;
    expect(roleSelect.disabled).toBe(true);
  });

  it("a 404 renders a generic load error — no private hint, no leak", async () => {
    rosterError = new ApiError(404, "project not found");
    renderSection({ isWorkspaceAdmin: false, currentUserId: "u-budi" });
    await screen.findByText("project not found");
    expect(screen.queryByText(/private/i)).toBeNull();
    expect(screen.queryByText("Rani")).toBeNull();
  });
});
