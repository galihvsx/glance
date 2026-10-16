// @vitest-environment jsdom
// Component tests for the project status updates feed (C34T1): list +
// latest-status chip rendering, composer visibility by role, post /
// edit / delete flows, plain-text (not markdown) body rendering, and
// the null-author (deleted user) label.

import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClientProvider } from "@tanstack/react-query";
import { afterEach, describe, expect, it, vi, type Mock } from "vitest";
import ProjectUpdates from "./ProjectUpdates";
import { api } from "../../lib/api";
import { makeQueryClient } from "../shell/test-utils";
import type { ProjectUpdate, ProjectUpdateStatus } from "../../lib/types";

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

vi.mock("../../lib/auth", () => ({
  useAuth: () => ({
    user: {
      id: "u-me",
      email: "me@example.com",
      name: "Me",
      avatar_url: null,
      is_active: true,
      is_admin: false,
      last_login_at: null,
      created_at: "",
      updated_at: "",
    },
    loading: false,
    refresh: vi.fn(),
    logout: vi.fn(),
  }),
}));

const get = () => api.get as unknown as Mock;
const post = () => api.post as unknown as Mock;
const patch = () => api.patch as unknown as Mock;
const del = () => api.del as unknown as Mock;

function update(over: Partial<ProjectUpdate> = {}): ProjectUpdate {
  return {
    id: "u1",
    project_id: "p1",
    author: { id: "u-me", name: "Me", email: "me@example.com" },
    status: "on_track",
    body: "Shipping on schedule",
    created_at: "2026-10-16T10:00:00.000Z",
    updated_at: "2026-10-16T10:00:00.000Z",
    ...over,
  };
}

function seed(updates: ProjectUpdate[]) {
  get().mockImplementation((path: string) => {
    if (path.endsWith("/updates")) return Promise.resolve({ updates });
    throw new Error(`unexpected GET ${path}`);
  });
}

function renderFeed(role: number) {
  const qc = makeQueryClient();
  return render(
    <QueryClientProvider client={qc}>
      <ProjectUpdates slug="ws" identifier="ENG" role={role} />
    </QueryClientProvider>,
  );
}

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

describe("ProjectUpdates", () => {
  it("renders the timeline and the latest-status chip from the first item", async () => {
    seed([
      update({
        status: "at_risk",
        body: "Blocked on review",
        created_at: "2026-10-16T11:00:00.000Z",
        updated_at: "2026-10-16T11:00:00.000Z",
      }),
      update({
        id: "u2",
        author: { id: "u-x", name: "Xena", email: "x@example.com" },
        status: "on_track",
        body: "All green",
        created_at: "2026-10-16T10:00:00.000Z",
        updated_at: "2026-10-16T10:00:00.000Z",
      }),
    ]);
    renderFeed(15);
    await waitFor(() => expect(screen.getByText("Blocked on review")).toBeTruthy());
    expect(screen.getByText("All green")).toBeTruthy();
    expect(screen.getByText("Xena")).toBeTruthy();
    // Latest chip derives from the first (newest) list item.
    expect(screen.getByTestId("project-updates-latest").textContent).toBe("At Risk");
  });

  it("shows the No updates yet chip and empty state when the list is empty", async () => {
    seed([]);
    renderFeed(15);
    await waitFor(() =>
      expect(screen.getByTestId("project-updates-latest").textContent).toBe(
        "No updates yet",
      ),
    );
    await waitFor(() =>
      expect(screen.getByTestId("project-updates-empty").textContent).toContain(
        "No updates yet.",
      ),
    );
  });

  it("shows a deleted-user label when the author is null", async () => {
    seed([update({ author: null })]);
    renderFeed(15);
    await waitFor(() => expect(screen.getByText("Deleted user")).toBeTruthy());
  });

  it("renders the body as plain text, never markdown", async () => {
    seed([update({ body: "**not markdown**" })]);
    renderFeed(15);
    await waitFor(() => expect(screen.getByText("**not markdown**")).toBeTruthy());
    expect(screen.queryByText("not markdown", { exact: true })).toBeNull();
    expect(document.querySelector("strong")).toBeNull();
  });

  it("posts an update with the default On Track status for members", async () => {
    seed([]);
    post().mockResolvedValue(update());
    renderFeed(15);
    await waitFor(() =>
      expect(screen.getByPlaceholderText("Share a status update…")).toBeTruthy(),
    );
    fireEvent.change(screen.getByPlaceholderText("Share a status update…"), {
      target: { value: "Fresh status" },
    });
    fireEvent.click(screen.getByText("Post update"));
    await waitFor(() => expect(post()).toHaveBeenCalled());
    const [path, payload] = post().mock.calls[0] as [
      string,
      { status: ProjectUpdateStatus; body: string },
    ];
    expect(path).toContain("/updates");
    expect(payload).toEqual({ status: "on_track", body: "Fresh status" });
  });

  it("hides the composer for viewers (role 10) but still shows the feed", async () => {
    seed([update()]);
    renderFeed(10);
    await waitFor(() => expect(screen.getByText("Shipping on schedule")).toBeTruthy());
    expect(screen.queryByPlaceholderText("Share a status update…")).toBeNull();
  });

  it("shows edit/delete on my own update but not on others'", async () => {
    seed([
      update(),
      update({
        id: "u2",
        author: { id: "u-other", name: "Other", email: "o@example.com" },
        body: "Theirs",
      }),
    ]);
    renderFeed(15);
    await waitFor(() => expect(screen.getByText("Theirs")).toBeTruthy());
    // Exactly one Edit and one Delete button (mine only).
    expect(screen.getAllByText("Edit")).toHaveLength(1);
    expect(screen.getAllByText("Delete")).toHaveLength(1);
  });

  it("shows edit/delete on others' updates for workspace admins (role 20)", async () => {
    seed([
      update(),
      update({
        id: "u2",
        author: { id: "u-other", name: "Other", email: "o@example.com" },
        body: "Theirs",
      }),
    ]);
    renderFeed(20);
    await waitFor(() => expect(screen.getByText("Theirs")).toBeTruthy());
    expect(screen.getAllByText("Edit")).toHaveLength(2);
    expect(screen.getAllByText("Delete")).toHaveLength(2);
  });

  it("saves an edit via PATCH with status + body", async () => {
    seed([update({ status: "at_risk", body: "Blocked" })]);
    patch().mockResolvedValue(update({ status: "at_risk", body: "Unblocked" }));
    renderFeed(15);
    await waitFor(() => expect(screen.getByText("Edit")).toBeTruthy());
    fireEvent.click(screen.getByText("Edit"));
    const editBox = screen.getByDisplayValue("Blocked");
    fireEvent.change(editBox, { target: { value: "Unblocked" } });
    fireEvent.click(screen.getByText("Save"));
    await waitFor(() => expect(patch()).toHaveBeenCalled());
    const [path, payload] = patch().mock.calls[0] as [
      string,
      { status: ProjectUpdateStatus; body: string },
    ];
    expect(path).toContain("/updates/u1");
    expect(payload).toEqual({ status: "at_risk", body: "Unblocked" });
  });

  it("deletes via api.del after confirming", async () => {
    seed([update()]);
    del().mockResolvedValue(undefined);
    vi.spyOn(window, "confirm").mockReturnValue(true);
    renderFeed(15);
    await waitFor(() => expect(screen.getByText("Delete")).toBeTruthy());
    fireEvent.click(screen.getByText("Delete"));
    await waitFor(() => expect(del()).toHaveBeenCalled());
    expect(del().mock.calls[0][0]).toContain("/updates/u1");
  });

  it("skips the delete when the confirm is cancelled", async () => {
    seed([update()]);
    vi.spyOn(window, "confirm").mockReturnValue(false);
    renderFeed(15);
    await waitFor(() => expect(screen.getByText("Delete")).toBeTruthy());
    fireEvent.click(screen.getByText("Delete"));
    expect(del()).not.toHaveBeenCalled();
  });
});
