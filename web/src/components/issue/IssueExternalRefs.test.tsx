// @vitest-environment jsdom
// Component tests for the issue-detail "Linked pull requests / commits"
// section (C62T1): list render (repo, number/short SHA, URL link, state
// badge), empty state, role gating (members 15+ only), link-dialog
// validation, honest 409/400 handling, and unlink confirm.

import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router-dom";
import { afterEach, describe, expect, it, vi, type Mock } from "vitest";
import IssueExternalRefs from "./IssueExternalRefs";
import { api, ApiError } from "../../lib/api";
import { makeQueryClient } from "../shell/test-utils";
import type { ExternalRef, Member } from "../../lib/types";

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
const del = () => api.del as unknown as Mock;

let mockRole = 15;
let refs: ExternalRef[] = [];

const issueKey = ["issue", "ws", "ENG", "iss-1"] as const;

function setup() {
  get().mockImplementation((url: string) => {
    if (url.includes("/members")) {
      const members: Member[] = [
        { id: "u-me", email: "me@example.com", role: mockRole },
      ];
      return Promise.resolve({ members });
    }
    return Promise.resolve({});
  });
  post().mockResolvedValue({
    id: "ref-new",
    issue_id: "iss-1",
    provider: "github",
    repo: "acme/repo",
    ref_type: "pr",
    ref_id: "42",
    url: "https://github.com/acme/repo/pull/42",
    state: "open",
    created_at: "2026-10-18T00:00:00Z",
  });
  del().mockResolvedValue({});
  const qc = makeQueryClient();
  const invalidateSpy = vi.spyOn(qc, "invalidateQueries");
  render(
    <MemoryRouter initialEntries={["/w/ws/p/ENG/i/iss-1"]}>
      <QueryClientProvider client={qc}>
        <IssueExternalRefs
          slug="ws"
          identifier="ENG"
          uuid="iss-1"
          refs={refs}
          issueKey={[...issueKey]}
          onError={vi.fn()}
        />
      </QueryClientProvider>
    </MemoryRouter>,
  );
  return { qc, invalidateSpy };
}

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
  vi.restoreAllMocks();
  mockRole = 15;
  refs = [];
});

const prRef: ExternalRef = {
  id: "ref-1",
  issue_id: "iss-1",
  provider: "github",
  repo: "acme/repo",
  ref_type: "pr",
  ref_id: "42",
  url: "https://github.com/acme/repo/pull/42",
  state: "merged",
  created_at: "2026-10-18T00:00:00Z",
};

const commitRef: ExternalRef = {
  id: "ref-2",
  issue_id: "iss-1",
  provider: "github",
  repo: "acme/repo",
  ref_type: "commit",
  ref_id: "abc1234def5678901234567890abcdef12345678",
  url: "https://github.com/acme/repo/commit/abc1234def5678901234567890abcdef12345678",
  state: "open",
  created_at: "2026-10-18T00:00:00Z",
};

describe("IssueExternalRefs", () => {
  it("renders linked PRs and commits with repo, number/short SHA, URL link and state", async () => {
    refs = [prRef, commitRef];
    setup();
    const prLink = await screen.findByRole("link", {
      name: /acme\/repo#42/,
    });
    expect(prLink).toHaveAttribute(
      "href",
      "https://github.com/acme/repo/pull/42",
    );
    expect(screen.getByText("Merged")).toBeInTheDocument();
    const commitLink = screen.getByRole("link", {
      name: /acme\/repo@abc1234/,
    });
    expect(commitLink).toHaveAttribute(
      "href",
      "https://github.com/acme/repo/commit/abc1234def5678901234567890abcdef12345678",
    );
    expect(screen.getByText("Open")).toBeInTheDocument();
  });

  it("shows the empty state when nothing is linked", async () => {
    setup();
    expect(
      await screen.findByText("No linked pull requests or commits yet."),
    ).toBeInTheDocument();
  });

  it("hides link/unlink controls for guests (role < 15)", async () => {
    mockRole = 5;
    refs = [prRef];
    setup();
    await screen.findByRole("link", { name: /acme\/repo#42/ });
    expect(
      screen.queryByRole("button", { name: "Link pull request or commit" }),
    ).toBeNull();
    expect(
      screen.queryByRole("button", { name: /Unlink pull request/ }),
    ).toBeNull();
  });

  it("links a pull request from the dialog", async () => {
    setup();
    fireEvent.click(
      await screen.findByRole("button", {
        name: "Link pull request or commit",
      }),
    );
    const dialog = await screen.findByRole("dialog");
    fireEvent.change(within(dialog).getByLabelText("Repository"), {
      target: { value: "acme/repo" },
    });
    fireEvent.change(within(dialog).getByLabelText("PR number or commit SHA"), {
      target: { value: "42" },
    });
    fireEvent.change(within(dialog).getByLabelText("URL"), {
      target: { value: "https://github.com/acme/repo/pull/42" },
    });
    fireEvent.click(within(dialog).getByRole("button", { name: "Link" }));
    await waitFor(() =>
      expect(post()).toHaveBeenCalledWith(
        expect.stringContaining("/issues/iss-1/refs"),
        {
          provider: "github",
          repo: "acme/repo",
          ref_type: "pr",
          ref_id: "42",
          url: "https://github.com/acme/repo/pull/42",
        },
      ),
    );
    // Dialog closes on success.
    await waitFor(() =>
      expect(screen.queryByRole("dialog")).toBeNull(),
    );
  });

  it("validates required fields before posting", async () => {
    setup();
    fireEvent.click(
      await screen.findByRole("button", {
        name: "Link pull request or commit",
      }),
    );
    const dialog = await screen.findByRole("dialog");
    fireEvent.click(within(dialog).getByRole("button", { name: "Link" }));
    expect(
      await within(dialog).findByText("Repository is required."),
    ).toBeInTheDocument();
    expect(post()).not.toHaveBeenCalled();
  });

  it("rejects a non-http(s) URL before posting", async () => {
    setup();
    fireEvent.click(
      await screen.findByRole("button", {
        name: "Link pull request or commit",
      }),
    );
    const dialog = await screen.findByRole("dialog");
    fireEvent.change(within(dialog).getByLabelText("Repository"), {
      target: { value: "acme/repo" },
    });
    fireEvent.change(within(dialog).getByLabelText("PR number or commit SHA"), {
      target: { value: "42" },
    });
    fireEvent.change(within(dialog).getByLabelText("URL"), {
      target: { value: "ftp://example.com/x" },
    });
    fireEvent.click(within(dialog).getByRole("button", { name: "Link" }));
    expect(
      await within(dialog).findByText("URL must start with http(s)://."),
    ).toBeInTheDocument();
    expect(post()).not.toHaveBeenCalled();
  });

  it("shows an honest message on 409 (already linked)", async () => {
    setup();
    post().mockRejectedValue(
      new ApiError(
        409,
        "external ref already linked to this issue",
        "conflict",
      ),
    );
    fireEvent.click(
      await screen.findByRole("button", {
        name: "Link pull request or commit",
      }),
    );
    const dialog = await screen.findByRole("dialog");
    fireEvent.change(within(dialog).getByLabelText("Repository"), {
      target: { value: "acme/repo" },
    });
    fireEvent.change(within(dialog).getByLabelText("PR number or commit SHA"), {
      target: { value: "42" },
    });
    fireEvent.change(within(dialog).getByLabelText("URL"), {
      target: { value: "https://github.com/acme/repo/pull/42" },
    });
    fireEvent.click(within(dialog).getByRole("button", { name: "Link" }));
    expect(
      await within(dialog).findByText(
        "This pull request or commit is already linked to this issue.",
      ),
    ).toBeInTheDocument();
    // The dialog stays open so the user can adjust and retry.
    expect(screen.getByRole("dialog")).toBeInTheDocument();
  });

  it("surfaces the backend's invalid-field message on 400", async () => {
    setup();
    post().mockRejectedValue(
      new ApiError(
        400,
        "invalid external ref: ref_id must be a PR number or commit SHA",
        "bad_request",
      ),
    );
    fireEvent.click(
      await screen.findByRole("button", {
        name: "Link pull request or commit",
      }),
    );
    const dialog = await screen.findByRole("dialog");
    fireEvent.change(within(dialog).getByLabelText("Repository"), {
      target: { value: "acme/repo" },
    });
    fireEvent.change(within(dialog).getByLabelText("PR number or commit SHA"), {
      target: { value: "not-a-sha!!" },
    });
    fireEvent.change(within(dialog).getByLabelText("URL"), {
      target: { value: "https://github.com/acme/repo/pull/42" },
    });
    fireEvent.click(within(dialog).getByRole("button", { name: "Link" }));
    expect(
      await within(dialog).findByText(
        "invalid external ref: ref_id must be a PR number or commit SHA",
      ),
    ).toBeInTheDocument();
  });

  it("unlinks with confirm and invalidates the issue query", async () => {
    refs = [prRef];
    const confirm = vi.spyOn(window, "confirm").mockReturnValue(true);
    const { invalidateSpy } = setup();
    fireEvent.click(
      await screen.findByRole("button", {
        name: 'Unlink pull request "acme/repo#42"',
      }),
    );
    expect(confirm).toHaveBeenCalled();
    await waitFor(() =>
      expect(del()).toHaveBeenCalledWith(
        expect.stringContaining("/issues/iss-1/refs/ref-1"),
      ),
    );
    await waitFor(() =>
      expect(invalidateSpy).toHaveBeenCalledWith({
        queryKey: [...issueKey],
      }),
    );
  });
});
