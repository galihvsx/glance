// C92T1: project members client — URL shapes + envelope unwrapping
// against the C92T0 contract. The server is the gate; this client
// only builds the right requests and never invents a roster.

import { afterEach, beforeEach, describe, expect, it, vi, type Mock } from "vitest";
import { api } from "./api";
import {
  addProjectMember,
  changeProjectMemberRole,
  fetchProjectMembers,
  projectMemberKeys,
  removeProjectMember,
} from "./projectMembers";
import type { ProjectMember } from "./types";

vi.mock("./api", async (importOriginal) => {
  const mod = await importOriginal<typeof import("./api")>();
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

const BASE = "/api/v1/workspaces/acme/projects/ENG/members";

afterEach(() => {
  vi.clearAllMocks();
});

beforeEach(() => {
  get().mockResolvedValue({ members: [] });
});

describe("project members client (C92T0 contract)", () => {
  it("lists the direct roster, unwrapping the envelope", async () => {
    const roster: ProjectMember[] = [
      { user_id: "u-rani", name: "Rani", email: "rani@example.com", role: 20 },
    ];
    get().mockResolvedValue({ members: roster });
    await expect(fetchProjectMembers("acme", "ENG")).resolves.toEqual(roster);
    expect(get()).toHaveBeenCalledWith(BASE);
  });

  it("treats a null members envelope as an empty roster", async () => {
    get().mockResolvedValue({ members: null });
    await expect(fetchProjectMembers("acme", "ENG")).resolves.toEqual([]);
  });

  it("adds a member with user_id + role, no role overwrite path", async () => {
    const member: ProjectMember = {
      user_id: "u-budi",
      name: "Budi",
      email: "budi@example.com",
      role: 15,
    };
    post().mockResolvedValue(member);
    await expect(addProjectMember("acme", "ENG", "u-budi", 15)).resolves.toEqual(
      member,
    );
    expect(post()).toHaveBeenCalledWith(BASE, { user_id: "u-budi", role: 15 });
  });

  it("changes a role via PATCH on the member URL", async () => {
    patch().mockResolvedValue({ user_id: "u-budi", email: "budi@example.com", role: 5 });
    await changeProjectMemberRole("acme", "ENG", "u-budi", 5);
    expect(patch()).toHaveBeenCalledWith(`${BASE}/u-budi`, { role: 5 });
  });

  it("removes a member via DELETE on the member URL", async () => {
    del().mockResolvedValue({ ok: true });
    await expect(removeProjectMember("acme", "ENG", "u-budi")).resolves.toEqual({
      ok: true,
    });
    expect(del()).toHaveBeenCalledWith(`${BASE}/u-budi`);
  });

  it("keys the roster under projects/slug/identifier/members", () => {
    expect(projectMemberKeys("acme", "ENG").list).toEqual([
      "projects",
      "acme",
      "ENG",
      "members",
    ]);
  });

  it("URL-encodes slug and identifier", async () => {
    await fetchProjectMembers("my ws", "E NG");
    expect(get()).toHaveBeenCalledWith(
      "/api/v1/workspaces/my%20ws/projects/E%20NG/members",
    );
  });
});
