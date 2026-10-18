// Tests for comment-level reactions (C67T1): chips + a small picker
// trigger on a comment node, endpoint shapes under
// .../comments/{commentID}/reactions, optimistic updates against the
// comments query, the member gate, and malformed payload defense.
// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, describe, expect, it, vi } from "vitest";
import CommentReactions from "./CommentReactions";
import { api, ApiError } from "../../lib/api";
import type { CommentNode, ReactionGroup } from "../../lib/types";

vi.mock("../../lib/api", () => ({
  api: { get: vi.fn(), post: vi.fn(), del: vi.fn() },
  ApiError: class ApiError extends Error {
    status: number;
    constructor(status: number, message: string) {
      super(message);
      this.name = "ApiError";
      this.status = status;
    }
  },
}));

const mockPost = vi.mocked(api.post);
const mockDel = vi.mocked(api.del);

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

const SLUG = "acme";
const IDENT = "ENG";
const UUID = "issue-uuid-1";
const CID = "comment-1";
const REACTIONS_PATH = `/api/v1/workspaces/${SLUG}/projects/${IDENT}/issues/${UUID}/comments/${CID}/reactions`;

const g = (emoji: string, count: number, reacted: boolean): ReactionGroup => ({
  emoji,
  count,
  reacted,
});

function comment(reactions: ReactionGroup[]): CommentNode {
  return {
    id: CID,
    issue_id: UUID,
    actor: { id: "u-bo", name: "Bo", email: "bo@example.com" },
    content: null,
    created_at: "2026-10-19T00:00:00Z",
    updated_at: "2026-10-19T00:00:00Z",
    reactions,
    replies: [],
  };
}

function renderReactions(c: CommentNode, canWrite: boolean) {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  render(
    <QueryClientProvider client={qc}>
      <CommentReactions
        slug={SLUG}
        identifier={IDENT}
        uuid={UUID}
        comment={c}
        canWrite={canWrite}
      />
    </QueryClientProvider>,
  );
  return qc;
}

describe("CommentReactions", () => {
  it("renders group chips and a small picker trigger", async () => {
    renderReactions(comment([g("👍", 2, true)]), true);
    const chip = await screen.findByRole("button", { name: /👍.*2/ });
    expect(chip).toHaveAttribute("aria-pressed", "true");
    expect(await screen.findByRole("button", { name: "Add reaction" })).toBeInTheDocument();
  });

  it("clicking a reacted chip calls DELETE .../comments/{id}/reactions?emoji=", async () => {
    renderReactions(comment([g("👍", 2, true)]), true);
    const chip = await screen.findByRole("button", { name: /👍.*2/ });
    fireEvent.click(chip);
    // React Query v5 runs the mutationFn a microtask after mutate(), so
    // the DELETE lands asynchronously — wait for it.
    await waitFor(() => expect(mockDel).toHaveBeenCalledTimes(1));
    expect(mockDel.mock.calls[0][0]).toBe(
      `${REACTIONS_PATH}?emoji=${encodeURIComponent("👍")}`,
    );
  });

  it("picking an emoji POSTs a JSON {emoji} body to the comment reactions endpoint", async () => {
    renderReactions(comment([]), true);
    fireEvent.click(await screen.findByRole("button", { name: "Add reaction" }));
    const search = await screen.findByLabelText("Search emoji");
    fireEvent.change(search, { target: { value: "party popper" } });
    fireEvent.click(await screen.findByRole("option", { name: "party popper" }));
    // The POST fires a microtask after mutate() (React Query v5).
    await waitFor(() => expect(mockPost).toHaveBeenCalledTimes(1));
    expect(mockPost.mock.calls[0][0]).toBe(REACTIONS_PATH);
    expect(mockPost.mock.calls[0][1]).toEqual({ emoji: "🎉" });
    // Optimistic: the chip appears immediately.
    await screen.findByRole("button", { name: /🎉.*1/ });
  });

  it("non-members see chips but no picker trigger", async () => {
    renderReactions(comment([g("👍", 1, false)]), false);
    await screen.findByRole("button", { name: /👍.*1/ });
    expect(screen.queryByRole("button", { name: "Add reaction" })).not.toBeInTheDocument();
  });

  it("renders nothing when empty and the viewer cannot write", async () => {
    const { container } = render(
      <QueryClientProvider client={new QueryClient()}>
        <CommentReactions
          slug={SLUG}
          identifier={IDENT}
          uuid={UUID}
          comment={comment([])}
          canWrite={false}
        />
      </QueryClientProvider>,
    );
    await waitFor(() => expect(container.firstChild).toBeNull());
  });

  it("defends against malformed reaction groups on the comment", async () => {
    const c = comment([
      { emoji: "🎉", count: 2 },
      null,
      { emoji: "", count: 1 },
    ] as unknown as ReactionGroup[]);
    renderReactions(c, true);
    const chip = await screen.findByRole("button", { name: /🎉.*2/ });
    expect(chip).toHaveAttribute("aria-pressed", "false");
    // Only the one valid group renders.
    expect(screen.getAllByRole("button", { name: /🎉|👍/ })).toHaveLength(1);
  });

  it("surfaces an error Alert when the toggle fails", async () => {
    renderReactions(comment([g("👍", 1, false)]), true);
    mockPost.mockRejectedValue(new ApiError(403, "forbidden"));
    fireEvent.click(await screen.findByRole("button", { name: /👍.*1/ }));
    expect(await screen.findByRole("alert")).toHaveTextContent("forbidden");
  });

  it("reverts the optimistic chip when the toggle fails", async () => {
    renderReactions(comment([]), true);
    mockPost.mockRejectedValue(new ApiError(500, "boom"));
    fireEvent.click(await screen.findByRole("button", { name: "Add reaction" }));
    const search = await screen.findByLabelText("Search emoji");
    fireEvent.change(search, { target: { value: "rocket" } });
    fireEvent.click(await screen.findByRole("option", { name: "rocket" }));
    // Optimistic chip appears, then the failure reverts to the prop truth.
    await screen.findByRole("button", { name: /🚀.*1/ });
    await waitFor(() =>
      expect(screen.queryByRole("button", { name: /🚀.*1/ })).not.toBeInTheDocument(),
    );
    expect(await screen.findByRole("alert")).toHaveTextContent("boom");
  });
});
