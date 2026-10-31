// @vitest-environment jsdom
// C92T1: Project Settings visibility toggle — Admin-gated,
// public/private with the plain-language note, failed saves keep the
// old value with an honest error (no optimistic lie), non-admins see
// the current visibility read-only.

import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it, vi, type Mock } from "vitest";
import VisibilitySection from "./VisibilitySection";
import { api, ApiError } from "../../lib/api";
import { makeQueryClient } from "../shell/test-utils";
import type { ProjectMember } from "../../lib/types";

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

let visibility: string;
let roster: ProjectMember[];

function installApiMocks() {
  get().mockImplementation((url: string) => {
    if (url.includes("/members")) return Promise.resolve({ members: roster });
    return Promise.resolve({
      id: "p-eng",
      workspace_id: "w1",
      identifier: "ENG",
      name: "Engineering",
      visibility,
      parallel_cycles: false,
      created_at: "",
      updated_at: "",
    });
  });
  patch().mockImplementation((_url: string, body: { visibility: string }) => {
    visibility = body.visibility;
    return Promise.resolve({
      id: "p-eng",
      workspace_id: "w1",
      identifier: "ENG",
      name: "Engineering",
      visibility,
      parallel_cycles: false,
      created_at: "",
      updated_at: "",
    });
  });
}

function renderSection(props: { isWorkspaceAdmin: boolean; currentUserId?: string | null }) {
  render(
    <QueryClientProvider client={makeQueryClient()}>
      <VisibilitySection slug="acme" identifier="ENG" {...props} />
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  visibility = "public";
  roster = [
    { user_id: "u-rani", name: "Rani", email: "rani@example.com", role: 20 },
    { user_id: "u-budi", name: "Budi", email: "budi@example.com", role: 15 },
  ];
  installApiMocks();
});

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

describe("visibility toggle (C92T1)", () => {
  it("admin flips public → private via PATCH", async () => {
    renderSection({ isWorkspaceAdmin: true, currentUserId: "u-rani" });
    await screen.findByText(/Public: every workspace member/);
    fireEvent.click(screen.getByText("Private"));
    await waitFor(() =>
      expect(patch()).toHaveBeenCalledWith(
        "/api/v1/workspaces/acme/projects/ENG",
        { visibility: "private" },
      ),
    );
    await screen.findByText("Saved.");
  });

  it("a direct project Admin (not workspace Admin) can flip it", async () => {
    renderSection({ isWorkspaceAdmin: false, currentUserId: "u-rani" });
    await screen.findByText(/Public: every workspace member/);
    expect(screen.getByText("Private")).toBeTruthy();
  });

  it("non-admin sees the current visibility read-only, never a toggle", async () => {
    visibility = "private";
    renderSection({ isWorkspaceAdmin: false, currentUserId: "u-budi" });
    await screen.findByText(/only project admins can change visibility/);
    expect(screen.queryByRole("button", { name: "Public" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Private" })).toBeNull();
  });

  it("a failed save keeps the old value and shows an honest error", async () => {
    patch().mockRejectedValueOnce(new ApiError(403, "forbidden"));
    renderSection({ isWorkspaceAdmin: true, currentUserId: "u-rani" });
    await screen.findByText(/Public: every workspace member/);
    fireEvent.click(screen.getByText("Private"));
    await screen.findByText("forbidden");
    expect(visibility).toBe("public");
  });

  it("the note warns privatizing permanently revokes share links (C93T2)", async () => {
    renderSection({ isWorkspaceAdmin: true, currentUserId: "u-rani" });
    await screen.findByText(
      /Warning: making a public project private permanently revokes its outstanding share links/,
    );
    expect(
      screen.getByText(/re-publicizing later does not restore them/),
    ).toBeTruthy();
  });

  it("the note explains private in plain language", async () => {
    renderSection({ isWorkspaceAdmin: true, currentUserId: "u-rani" });
    await screen.findByText(
      /Private: only project members and members of the linked teamspace/,
    );
    expect(
      screen.getByText(/its direct URL shows the same not-found state/),
    ).toBeTruthy();
  });
});
