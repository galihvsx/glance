// @vitest-environment jsdom
// C90T1: teamspace Members tab — admin-only tab, roster list, add /
// remove flows, failed mutations keep the old roster with an honest
// error (no optimistic lie), empty-picker state.

import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it, vi, type Mock } from "vitest";
import TeamspaceManageDialog from "./TeamspaceManageDialog";
import { api, ApiError } from "../../lib/api";
import { makeQueryClient } from "./test-utils";
import type { Teamspace, TeamspaceMember, WorkspaceMember } from "../../lib/types";

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
const del = () => api.del as unknown as Mock;

const teamspace: Teamspace = {
  id: "ts-platform",
  workspace_id: "w1",
  name: "Platform",
  project_count: 2,
  member_count: 2,
  created_at: "",
  updated_at: "",
};

// Same teamspace with Rina as Lead (C92T1 fixture).
const teamspaceWithLead: Teamspace = { ...teamspace, lead_user_id: "u-rina" };

let roster: TeamspaceMember[];
let workspaceMembers: WorkspaceMember[];
// Caller id for GET /api/v1/me (null = unauthenticated-shaped {}).
let meId: string | null;
// Live Lead id, mutated by the lead POST/DELETE mocks.
let leadId: string | null;

function installApiMocks() {
  get().mockImplementation((url: string) => {
    if (url.endsWith("/me")) {
      return Promise.resolve(
        meId
          ? { id: meId, email: `${meId}@example.com`, name: null, avatar_url: null, is_active: true, is_admin: false, last_login_at: null, created_at: "", updated_at: "" }
          : {},
      );
    }
    // Roster URL contains /teamspaces/ AND /members; the workspace
    // members URL contains only /members — check teamspaces first.
    if (url.includes("/teamspaces/")) return Promise.resolve({ members: roster });
    if (url.includes("/members")) return Promise.resolve({ members: workspaceMembers });
    return Promise.resolve({});
  });
  post().mockImplementation((url: string, body: { user_id: string }) => {
    if (url.includes("/lead")) {
      leadId = body.user_id;
      return Promise.resolve({ ...teamspaceWithLead, lead_user_id: leadId });
    }
    if (url.includes("/members")) {
      const m = workspaceMembers.find((w) => w.id === body.user_id)!;
      const row: TeamspaceMember = { id: m.id, name: m.name, email: m.email };
      roster = [...roster, row];
      return Promise.resolve(row);
    }
    return Promise.resolve({});
  });
  del().mockImplementation((url: string) => {
    if (url.includes("/lead")) {
      leadId = null;
      return Promise.resolve({ ...teamspaceWithLead, lead_user_id: null });
    }
    if (url.includes("/members/")) {
      const id = url.split("/").pop()!;
      roster = roster.filter((m) => m.id !== id);
      return Promise.resolve({ ok: true });
    }
    return Promise.resolve({ ok: true });
  });
}

function renderDialog(
  isAdmin: boolean,
  opts: { teamspace?: Teamspace; me?: string | null } = {},
) {
  if (opts.me !== undefined) meId = opts.me;
  render(
    <QueryClientProvider client={makeQueryClient()}>
      <TeamspaceManageDialog
        slug="acme"
        state={{ mode: "rename", teamspace: opts.teamspace ?? teamspace }}
        projects={[]}
        onClose={() => {}}
        isAdmin={isAdmin}
      />
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  roster = [
    { id: "u-galih", name: "Galih Putro Aji", email: "galih@example.com" },
    { id: "u-rina", name: "Rina Dewi", email: "rina@example.com" },
  ];
  workspaceMembers = [
    { id: "u-galih", name: "Galih Putro Aji", email: "galih@example.com", role: 20 },
    { id: "u-rina", name: "Rina Dewi", email: "rina@example.com", role: 15 },
    { id: "u-budi", name: "Budi Santoso", email: "budi@example.com", role: 15 },
  ];
  meId = null;
  leadId = "u-rina";
  installApiMocks();
});

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

describe("Members tab (C90T1)", () => {
  it("is hidden for non-admins — the rename form is unchanged", async () => {
    renderDialog(false);
    expect(screen.queryByText(/Members/)).toBeNull();
    expect(screen.getByLabelText("Name")).toBeTruthy();
  });

  it("lists members + count for admins", async () => {
    renderDialog(true);
    fireEvent.click(screen.getByText("Members (2)"));
    await screen.findByText("Galih Putro Aji");
    expect(screen.getByText("Rina Dewi")).toBeTruthy();
    // Rows show email as the meta line — never a workspace role.
    expect(screen.getByText("galih@example.com")).toBeTruthy();
    expect(screen.queryByText(/Admin/)).toBeNull();
  });

  it("add member updates the roster", async () => {
    renderDialog(true);
    fireEvent.click(screen.getByText("Members (2)"));
    await screen.findByText("Galih Putro Aji");
    fireEvent.change(screen.getByLabelText("Add a workspace member"), {
      target: { value: "u-budi" },
    });
    fireEvent.click(screen.getByText("Add"));
    await waitFor(() =>
      expect(screen.getByText("Budi Santoso")).toBeTruthy(),
    );
    expect(post()).toHaveBeenCalledWith(
      expect.stringContaining("/teamspaces/ts-platform/members"),
      { user_id: "u-budi" },
    );
  });

  it("remove member updates the roster", async () => {
    renderDialog(true);
    fireEvent.click(screen.getByText("Members (2)"));
    await screen.findByText("Rina Dewi");
    const removeButtons = screen.getAllByText("Remove");
    fireEvent.click(removeButtons[1]);
    await waitFor(() =>
      expect(screen.queryByText("Rina Dewi")).toBeNull(),
    );
    expect(screen.getByText("Galih Putro Aji")).toBeTruthy();
  });

  it("failed add keeps the old roster and shows an error", async () => {
    post().mockRejectedValueOnce(new ApiError(403, "forbidden"));
    renderDialog(true);
    fireEvent.click(screen.getByText("Members (2)"));
    await screen.findByText("Galih Putro Aji");
    fireEvent.change(screen.getByLabelText("Add a workspace member"), {
      target: { value: "u-budi" },
    });
    fireEvent.click(screen.getByText("Add"));
    await screen.findByText("forbidden");
    expect(screen.getByText("Galih Putro Aji")).toBeTruthy();
    expect(screen.getByText("Rina Dewi")).toBeTruthy();
    expect(screen.queryByText("Budi Santoso")).toBeNull();
  });

  it("failed remove keeps the old roster and shows an error", async () => {
    del().mockRejectedValueOnce(new ApiError(403, "forbidden"));
    renderDialog(true);
    fireEvent.click(screen.getByText("Members (2)"));
    await screen.findByText("Rina Dewi");
    fireEvent.click(screen.getAllByText("Remove")[1]);
    await screen.findByText("forbidden");
    expect(screen.getByText("Rina Dewi")).toBeTruthy();
  });

  it("shows the empty-picker state when everyone is already a member", async () => {
    roster = [...workspaceMembers.map((m) => ({ id: m.id, name: m.name, email: m.email }))];
    renderDialog(true);
    fireEvent.click(screen.getByText("Members (2)"));
    await screen.findByText("Budi Santoso");
    expect(
      screen.getByText("All workspace members are already in this teamspace."),
    ).toBeTruthy();
  });
});

describe("Lead tab + Lead affordances (C92T1)", () => {
  it("admin sees the Lead tab and the Lead name in the header", async () => {
    renderDialog(true, { teamspace: teamspaceWithLead });
    expect(screen.getByRole("tab", { name: "Lead" })).toBeTruthy();
    await screen.findByText(/Lead: Rina Dewi/);
  });

  it("admin sets a new Lead from the member list", async () => {
    renderDialog(true, { teamspace: teamspaceWithLead });
    fireEvent.click(screen.getByRole("tab", { name: "Lead" }));
    const select = (await screen.findByLabelText(
      "Teamspace lead",
    )) as HTMLSelectElement;
    await screen.findByRole("option", { name: "Rina Dewi (current lead)" });
    fireEvent.change(select, { target: { value: "u-galih" } });
    fireEvent.click(screen.getByText("Save"));
    await waitFor(() =>
      expect(post()).toHaveBeenCalledWith(
        expect.stringContaining("/teamspaces/ts-platform/lead"),
        { user_id: "u-galih" },
      ),
    );
    await screen.findByText(/Lead: Galih Putro Aji/);
  });

  it("admin clears the Lead", async () => {
    renderDialog(true, { teamspace: teamspaceWithLead });
    fireEvent.click(screen.getByRole("tab", { name: "Lead" }));
    const select = (await screen.findByLabelText(
      "Teamspace lead",
    )) as HTMLSelectElement;
    await screen.findByRole("option", { name: "Rina Dewi (current lead)" });
    fireEvent.change(select, { target: { value: "" } });
    fireEvent.click(screen.getByText("Save"));
    await waitFor(() =>
      expect(del()).toHaveBeenCalledWith(
        expect.stringContaining("/teamspaces/ts-platform/lead"),
      ),
    );
    await waitFor(() =>
      expect(screen.queryByText(/Lead:/)).toBeNull(),
    );
  });

  it("a failed Lead save keeps the old Lead and shows an error", async () => {
    post().mockRejectedValueOnce(new ApiError(403, "forbidden"));
    renderDialog(true, { teamspace: teamspaceWithLead });
    fireEvent.click(screen.getByRole("tab", { name: "Lead" }));
    const select = (await screen.findByLabelText(
      "Teamspace lead",
    )) as HTMLSelectElement;
    await screen.findByRole("option", { name: "Rina Dewi (current lead)" });
    fireEvent.change(select, { target: { value: "u-galih" } });
    fireEvent.click(screen.getByText("Save"));
    await screen.findByText("forbidden");
    expect(leadId).toBe("u-rina");
  });

  it("the Lead (not a workspace Admin) sees Details + Members, never the Lead tab", async () => {
    renderDialog(false, { teamspace: teamspaceWithLead, me: "u-rina" });
    await screen.findByText(/Lead: Rina Dewi/);
    expect(screen.getByRole("tab", { name: "Details" })).toBeTruthy();
    expect(screen.getByRole("tab", { name: "Members (2)" })).toBeTruthy();
    expect(screen.queryByRole("tab", { name: "Lead" })).toBeNull();
    fireEvent.click(screen.getByRole("tab", { name: "Members (2)" }));
    await screen.findByText("Galih Putro Aji");
  });

  it("a non-Lead non-admin still gets the plain rename form", async () => {
    renderDialog(false, { teamspace: teamspaceWithLead, me: "u-budi" });
    expect(screen.queryByRole("tab", { name: "Members (2)" })).toBeNull();
    expect(screen.getByLabelText("Name")).toBeTruthy();
  });
});
