// @vitest-environment jsdom
// Component tests for the page comment thread (C18T2): list + count
// badge rendering, composer visibility by role, post/edit/delete flows,
// and the null-author (deleted user) label.

import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClientProvider } from "@tanstack/react-query";
import { afterEach, describe, expect, it, vi, type Mock } from "vitest";
import PageComments from "./PageComments";
import { api } from "../../lib/api";
import { makeQueryClient } from "../shell/test-utils";
import type { Member, PageComment } from "../../lib/types";

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

function tiptapDoc(text: string): unknown {
  return {
    type: "doc",
    content: [
      { type: "paragraph", content: [{ type: "text", text }] },
    ],
  };
}

function comment(over: Partial<PageComment> = {}): PageComment {
  return {
    id: "c1",
    page_id: "p1",
    author: { id: "u-me", name: "Me", email: "me@example.com" },
    body: tiptapDoc("Hello page"),
    created_at: "2026-10-11T10:00:00.000Z",
    updated_at: "2026-10-11T10:00:00.000Z",
    ...over,
  };
}

function member(role: number): Member {
  return { id: "u-me", name: "Me", email: "me@example.com", role };
}

function seed(comments: PageComment[], role: number) {
  get().mockImplementation((path: string) => {
    if (path.endsWith("/comments")) return Promise.resolve({ comments });
    if (path.endsWith("/members")) return Promise.resolve({ members: [member(role)] });
    throw new Error(`unexpected GET ${path}`);
  });
}

function renderThread() {
  const qc = makeQueryClient();
  return render(
    <QueryClientProvider client={qc}>
      <PageComments slug="ws" identifier="ENG" pageId="p1" />
    </QueryClientProvider>,
  );
}

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

describe("PageComments", () => {
  it("renders the thread and the count badge", async () => {
    seed([comment(), comment({ id: "c2", author: { id: "u-x", name: "X", email: "x@example.com" }, body: tiptapDoc("Second") })], 15);
    renderThread();
    await waitFor(() => expect(screen.getByText("Hello page")).toBeTruthy());
    expect(screen.getByText("Second")).toBeTruthy();
    expect(screen.getByText("Me")).toBeTruthy();
    expect(screen.getByText("X")).toBeTruthy();
    // Count badge.
    expect(screen.getByTestId("page-comment-count").textContent).toBe("2");
  });

  it("shows a deleted-user label when the author is null", async () => {
    seed([comment({ author: null })], 15);
    renderThread();
    await waitFor(() => expect(screen.getByText("Deleted user")).toBeTruthy());
  });

  it("posts a comment as a TipTap doc for members", async () => {
    seed([], 15);
    post().mockResolvedValue(comment());
    renderThread();
    await waitFor(() =>
      expect(screen.getByPlaceholderText("Write a comment…")).toBeTruthy(),
    );
    fireEvent.change(screen.getByPlaceholderText("Write a comment…"), {
      target: { value: "Fresh thought" },
    });
    fireEvent.click(screen.getByText("Post comment"));
    await waitFor(() => expect(post()).toHaveBeenCalled());
    const [, payload] = post().mock.calls[0] as [string, { body: unknown }];
    expect(payload.body).toEqual(tiptapDoc("Fresh thought"));
  });

  it("hides the composer for viewers (role 10)", async () => {
    seed([comment()], 10);
    renderThread();
    await waitFor(() => expect(screen.getByText("Hello page")).toBeTruthy());
    expect(screen.queryByPlaceholderText("Write a comment…")).toBeNull();
  });

  it("shows edit/delete on my own comment but not on others'", async () => {
    seed(
      [
        comment(),
        comment({
          id: "c2",
          author: { id: "u-other", name: "Other", email: "o@example.com" },
          body: tiptapDoc("Theirs"),
        }),
      ],
      15,
    );
    renderThread();
    await waitFor(() => expect(screen.getByText("Theirs")).toBeTruthy());
    // Exactly one Edit and one Delete button (mine only).
    expect(screen.getAllByText("Edit")).toHaveLength(1);
    expect(screen.getAllByText("Delete")).toHaveLength(1);
  });

  it("saves an edit via PATCH", async () => {
    seed([comment()], 15);
    patch().mockResolvedValue(comment({ body: tiptapDoc("Edited") }));
    renderThread();
    await waitFor(() => expect(screen.getByText("Edit")).toBeTruthy());
    fireEvent.click(screen.getByText("Edit"));
    const editBox = screen.getByDisplayValue("Hello page");
    fireEvent.change(editBox, { target: { value: "Edited" } });
    fireEvent.click(screen.getByText("Save"));
    await waitFor(() => expect(patch()).toHaveBeenCalled());
    const [path, payload] = patch().mock.calls[0] as [string, { body: unknown }];
    expect(path).toContain("/comments/c1");
    expect(payload.body).toEqual(tiptapDoc("Edited"));
  });

  it("deletes via api.del after confirming", async () => {
    seed([comment()], 15);
    del().mockResolvedValue(undefined);
    vi.spyOn(window, "confirm").mockReturnValue(true);
    renderThread();
    await waitFor(() => expect(screen.getByText("Delete")).toBeTruthy());
    fireEvent.click(screen.getByText("Delete"));
    await waitFor(() => expect(del()).toHaveBeenCalled());
    expect(del().mock.calls[0][0]).toContain("/comments/c1");
  });

  it("skips the delete when the confirm is cancelled", async () => {
    seed([comment()], 15);
    vi.spyOn(window, "confirm").mockReturnValue(false);
    renderThread();
    await waitFor(() => expect(screen.getByText("Delete")).toBeTruthy());
    fireEvent.click(screen.getByText("Delete"));
    expect(del()).not.toHaveBeenCalled();
  });
});
