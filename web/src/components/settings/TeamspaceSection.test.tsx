// @vitest-environment jsdom
// C89T1: the Project settings teamspace picker — moving a project
// shows the new assignment, and a failed save keeps the old value
// with an honest error (no optimistic lie).

import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it, vi, type Mock } from "vitest";
import TeamspaceSection from "./TeamspaceSection";
import { api, ApiError } from "../../lib/api";
import { makeQueryClient } from "../shell/test-utils";
import type { Project, Teamspace } from "../../lib/types";

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
const patch = () => api.patch as unknown as Mock;

const teamspaces: Teamspace[] = [
  { id: "ts-platform", workspace_id: "w1", name: "Platform", project_count: 1, created_at: "", updated_at: "" },
  { id: "ts-mobile", workspace_id: "w1", name: "Mobile", project_count: 0, created_at: "", updated_at: "" },
];

let projectTeamspace: string | null;

function project(): Project {
  return {
    id: "p-eng",
    workspace_id: "w1",
    identifier: "ENG",
    name: "Engineering",
    description: "",
    labels: [],
    parallel_cycles: false,
    teamspace_id: projectTeamspace,
    created_at: "",
    updated_at: "",
  };
}

function renderSection(canEdit = true, currentUserId?: string | null) {
  get().mockImplementation((url: string) => {
    if (url.includes("/teamspaces")) return Promise.resolve({ teamspaces });
    return Promise.resolve(project());
  });
  render(
    <QueryClientProvider client={makeQueryClient()}>
      <TeamspaceSection
        slug="acme"
        identifier="ENG"
        canEdit={canEdit}
        currentUserId={currentUserId}
      />
    </QueryClientProvider>,
  );
}

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

beforeEach(() => {
  projectTeamspace = null;
});

describe("teamspace picker", () => {
  it("shows the current assignment and moves the project on change", async () => {
    patch().mockImplementation((_url: string, body: { teamspace_id: string | null }) => {
      projectTeamspace = body.teamspace_id;
      return Promise.resolve(project());
    });
    renderSection();

    const select = (await screen.findByRole("combobox", {
      name: "Teamspace",
    })) as HTMLSelectElement;
    expect(select.value).toBe("");
    expect(screen.getByText("Currently ungrouped.")).toBeTruthy();
    // Wait for the teamspace options to load — changing to an option
    // that is not rendered yet leaves the value unchanged.
    await screen.findByRole("option", { name: "Platform" });

    fireEvent.change(select, { target: { value: "ts-platform" } });
    await waitFor(() => {
      expect(patch()).toHaveBeenCalledWith(
        "/api/v1/workspaces/acme/projects/ENG",
        { teamspace_id: "ts-platform" },
      );
    });
    await waitFor(() => {
      expect(screen.getByText("Currently in Platform.")).toBeTruthy();
    });
    expect(
      (screen.getByRole("combobox", { name: "Teamspace" }) as HTMLSelectElement)
        .value,
    ).toBe("ts-platform");
  });

  it("a failed save keeps the old value and shows an honest error", async () => {
    patch().mockRejectedValue(new ApiError(400, "invalid teamspace"));
    renderSection();

    const select = (await screen.findByRole("combobox", {
      name: "Teamspace",
    })) as HTMLSelectElement;
    await screen.findByRole("option", { name: "Mobile" });
    fireEvent.change(select, { target: { value: "ts-mobile" } });
    await waitFor(() => {
      expect(screen.getByText("invalid teamspace")).toBeTruthy();
    });
    expect(
      (screen.getByRole("combobox", { name: "Teamspace" }) as HTMLSelectElement)
        .value,
    ).toBe("");
    expect(screen.getByText("Currently ungrouped.")).toBeTruthy();
  });

  // C90T1: the picker renders exactly the server-filtered teamspaces
  // query — no separate picker endpoint, no client-side expansion. A
  // member's query returns only their teamspaces; an admin's returns
  // all of them. The component must not add options the server did
  // not return, nor hide ones it did.
  it("move picker lists only the visible teamspaces for a member", async () => {
    get().mockImplementation((url: string) => {
      if (url.includes("/teamspaces")) {
        return Promise.resolve({ teamspaces: [teamspaces[0]] });
      }
      return Promise.resolve(project());
    });
    render(
      <QueryClientProvider client={makeQueryClient()}>
        <TeamspaceSection slug="acme" identifier="ENG" canEdit />
      </QueryClientProvider>,
    );
    await screen.findByRole("option", { name: "Platform" });
    expect(screen.queryByRole("option", { name: "Mobile" })).toBeNull();
  });

  it("move picker lists all teamspaces for an admin", async () => {
    get().mockImplementation((url: string) => {
      if (url.includes("/teamspaces")) return Promise.resolve({ teamspaces });
      return Promise.resolve(project());
    });
    render(
      <QueryClientProvider client={makeQueryClient()}>
        <TeamspaceSection slug="acme" identifier="ENG" canEdit />
      </QueryClientProvider>,
    );
    await screen.findByRole("option", { name: "Platform" });
    expect(screen.getByRole("option", { name: "Mobile" })).toBeTruthy();
  });

  // C92T1: a below-member Lead moves a project into/out of THEIR
  // teamspace; the server stays the per-teamspace gate and 403s a
  // move into another team's teamspace, which surfaces as the honest
  // error + revert, same as any failed save.
  it("a guest-role Lead can move a project into their own teamspace", async () => {
    teamspaces[1] = { ...teamspaces[1], lead_user_id: "u-lead" };
    patch().mockImplementation((_url: string, body: { teamspace_id: string | null }) => {
      projectTeamspace = body.teamspace_id;
      return Promise.resolve(project());
    });
    renderSection(false, "u-lead");
    const select = (await screen.findByRole("combobox", {
      name: "Teamspace",
    })) as HTMLSelectElement;
    expect(select.disabled).toBe(false);
    await screen.findByRole("option", { name: "Mobile" });
    fireEvent.change(select, { target: { value: "ts-mobile" } });
    await waitFor(() => {
      expect(patch()).toHaveBeenCalledWith(
        "/api/v1/workspaces/acme/projects/ENG",
        { teamspace_id: "ts-mobile" },
      );
    });
    await waitFor(() => {
      expect(screen.getByText("Currently in Mobile.")).toBeTruthy();
    });
  });

  it("a Lead moving into another team's teamspace gets the honest 403 + revert", async () => {
    teamspaces[1] = { ...teamspaces[1], lead_user_id: "u-lead" };
    patch().mockRejectedValue(new ApiError(403, "forbidden"));
    renderSection(false, "u-lead");
    const select = (await screen.findByRole("combobox", {
      name: "Teamspace",
    })) as HTMLSelectElement;
    await screen.findByRole("option", { name: "Platform" });
    fireEvent.change(select, { target: { value: "ts-platform" } });
    await waitFor(() => {
      expect(screen.getByText("forbidden")).toBeTruthy();
    });
    expect(select.value).toBe("");
  });

  it("moving back to Ungrouped sends an explicit null", async () => {
    projectTeamspace = "ts-platform";
    patch().mockImplementation((_url: string, body: { teamspace_id: string | null }) => {
      projectTeamspace = body.teamspace_id;
      return Promise.resolve(project());
    });
    renderSection();
    const select = (await screen.findByRole("combobox", {
      name: "Teamspace",
    })) as HTMLSelectElement;
    expect(select.value).toBe("ts-platform");
    await screen.findByRole("option", { name: "Platform" });
    fireEvent.change(select, { target: { value: "" } });
    await waitFor(() => {
      expect(patch()).toHaveBeenCalledWith(
        "/api/v1/workspaces/acme/projects/ENG",
        { teamspace_id: null },
      );
    });
    await waitFor(() => {
      expect(screen.getByText("Currently ungrouped.")).toBeTruthy();
    });
  });
});
