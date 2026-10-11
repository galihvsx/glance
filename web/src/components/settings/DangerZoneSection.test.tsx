// @vitest-environment jsdom
// Component tests for the project danger zone (C18T3): archive/restore
// buttons, the type-to-confirm delete dialog, the 409 blocker →
// force-delete path, and post-action navigation.

import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it, vi, type Mock } from "vitest";
import DangerZoneSection from "./DangerZoneSection";
import { api, ApiError } from "../../lib/api";
import { makeQueryClient } from "../shell/test-utils";
import type { Project } from "../../lib/types";

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

const navigateMock = vi.fn();
vi.mock("react-router-dom", async (importOriginal) => {
  const mod = await importOriginal<typeof import("react-router-dom")>();
  return { ...mod, useNavigate: () => navigateMock };
});

const get = () => api.get as unknown as Mock;
const post = () => api.post as unknown as Mock;
const del = () => api.del as unknown as Mock;

const liveProject: Project = {
  id: "p-eng",
  workspace_id: "w1",
  identifier: "ENG",
  name: "Engineering",
  description: "",
  created_at: "",
  updated_at: "",
};

function renderZone(project: Project) {
  get().mockResolvedValue(project);
  const qc = makeQueryClient();
  render(
    <QueryClientProvider client={qc}>
      <DangerZoneSection slug="acme" identifier="ENG" />
    </QueryClientProvider>,
  );
  return qc;
}

beforeEach(() => {
  navigateMock.mockReset();
  get().mockReset();
  post().mockReset();
  del().mockReset();
});

afterEach(cleanup);

describe("DangerZoneSection", () => {
  it("archives a live project and navigates to the projects page", async () => {
    renderZone(liveProject);
    const btn = await screen.findByRole("button", { name: "Archive project" });
    post().mockResolvedValue({ ...liveProject, archived_at: "2026-10-11T00:00:00Z" });
    fireEvent.click(btn);
    await waitFor(() => {
      expect(post()).toHaveBeenCalledWith(
        "/api/v1/workspaces/acme/projects/ENG/archive",
      );
    });
    await waitFor(() => {
      expect(navigateMock).toHaveBeenCalledWith("/w/acme/projects");
    });
  });

  it("offers restore (not archive) for an archived project", async () => {
    renderZone({ ...liveProject, archived_at: "2026-10-11T00:00:00Z" });
    const btn = await screen.findByRole("button", { name: "Restore project" });
    post().mockResolvedValue(liveProject);
    fireEvent.click(btn);
    await waitFor(() => {
      expect(post()).toHaveBeenCalledWith(
        "/api/v1/workspaces/acme/projects/ENG/unarchive",
      );
    });
    // Restore stays on the settings page (the project is live again).
    expect(navigateMock).not.toHaveBeenCalled();
  });

  it("delete requires typing the identifier and sends ?confirm=", async () => {
    renderZone(liveProject);
    fireEvent.click(await screen.findByRole("button", { name: "Delete project" }));
    const dialog = await screen.findByRole("dialog");
    const input = await screen.findByLabelText("Project identifier");
    const submit = dialog.querySelector(
      'button[type="submit"]',
    ) as HTMLButtonElement;
    // Mismatched text keeps the submit disabled; the match is
    // case-insensitive (the server normalizes identifiers the same way).
    fireEvent.change(input, { target: { value: "NOPE" } });
    expect(submit).toBeDisabled();
    fireEvent.change(input, { target: { value: "eng" } });
    expect(submit).not.toBeDisabled();
    fireEvent.change(input, { target: { value: "ENG" } });
    expect(submit).not.toBeDisabled();
    del().mockResolvedValue(undefined);
    fireEvent.click(submit);
    await waitFor(() => {
      expect(del()).toHaveBeenCalledWith(
        "/api/v1/workspaces/acme/projects/ENG?confirm=ENG",
      );
    });
    await waitFor(() => {
      expect(navigateMock).toHaveBeenCalledWith("/w/acme/projects", {
        replace: true,
      });
    });
  });

  it("surfaces the 409 blocker and offers force delete", async () => {
    renderZone(liveProject);
    // There are two "Delete project" buttons once the dialog opens; scope
    // to the dialog by opening it first.
    fireEvent.click(await screen.findByRole("button", { name: "Delete project" }));
    const input = await screen.findByLabelText("Project identifier");
    fireEvent.change(input, { target: { value: "ENG" } });
    const err = new ApiError(
      409,
      "project has non-archived issues; archive them first or retry with ?force=true",
      "conflict",
      { non_archived_issues: 3 },
    );
    del().mockRejectedValueOnce(err).mockResolvedValueOnce(undefined);
    const dialog = await screen.findByRole("dialog");
    const submit = dialog.querySelector('button[type="submit"]') as HTMLButtonElement;
    fireEvent.click(submit);
    const forceBtn = await screen.findByRole("button", {
      name: "Force delete anyway",
    });
    expect(
      screen.getByText(/3 non-archived issue/),
    ).toBeInTheDocument();
    fireEvent.click(forceBtn);
    await waitFor(() => {
      expect(del()).toHaveBeenLastCalledWith(
        "/api/v1/workspaces/acme/projects/ENG?confirm=ENG&force=true",
      );
    });
  });

  it("shows the archive error when the request fails", async () => {
    renderZone(liveProject);
    const btn = await screen.findByRole("button", { name: "Archive project" });
    post().mockRejectedValue(new ApiError(403, "forbidden", "forbidden"));
    fireEvent.click(btn);
    await waitFor(() => {
      expect(screen.getByText("forbidden")).toBeInTheDocument();
    });
  });
});
