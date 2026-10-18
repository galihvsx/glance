// Tests for the issue-level reaction row (C67T1): rendering, the
// picker, endpoint shapes (POST takes a JSON {emoji} body), optimistic
// updates, the member gate, and the error surface.
// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, describe, expect, it, vi } from "vitest";
import IssueReactions from "./IssueReactions";
import { api, ApiError } from "../../lib/api";
import type { ReactionGroup } from "../../lib/types";

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

vi.mock("../../lib/auth", () => ({
  useAuth: () => ({
    user: { id: "u-me", email: "me@example.com", name: "Me" },
  }),
}));

const mockGet = vi.mocked(api.get);
const mockPost = vi.mocked(api.post);
const mockDel = vi.mocked(api.del);

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

const SLUG = "acme";
const IDENT = "ENG";
const UUID = "issue-uuid-1";
const REACTIONS_PATH = `/api/v1/workspaces/${SLUG}/projects/${IDENT}/issues/${UUID}/reactions`;

const g = (emoji: string, count: number, reacted: boolean): ReactionGroup => ({
  emoji,
  count,
  reacted,
});

function memberGet(role: number) {
  return [{ id: "u-me", name: "Me", email: "me@example.com", role }];
}

function installGet(groups: ReactionGroup[], role = 15) {
  mockGet.mockImplementation((path: string) => {
    if (path.endsWith("/reactions")) return Promise.resolve({ reactions: groups });
    if (path.includes("/members")) return Promise.resolve({ members: memberGet(role) });
    return Promise.reject(new Error(`unexpected GET ${path}`));
  });
}

function renderRow() {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  render(
    <QueryClientProvider client={qc}>
      <IssueReactions slug={SLUG} identifier={IDENT} uuid={UUID} />
    </QueryClientProvider>,
  );
  return qc;
}

describe("IssueReactions", () => {
  it("renders group chips with counts and reacted state", async () => {
    installGet([g("🎉", 3, true), g("👍", 1, false)]);
    renderRow();
    const pressed = await screen.findByRole("button", { name: /🎉.*3/ });
    expect(pressed).toHaveAttribute("aria-pressed", "true");
    const plain = await screen.findByRole("button", { name: /👍.*1/ });
    expect(plain).toHaveAttribute("aria-pressed", "false");
    // Member sees the picker trigger.
    expect(await screen.findByRole("button", { name: "Add reaction" })).toBeInTheDocument();
  });

  it("clicking a reacted chip calls DELETE .../reactions?emoji= (optimistic first)", async () => {
    installGet([g("🎉", 3, true)]);
    let resolveDel!: () => void;
    mockDel.mockImplementation(() => new Promise<void>((r) => (resolveDel = r)));
    renderRow();
    const chip = await screen.findByRole("button", { name: /🎉.*3/ });
    fireEvent.click(chip);
    // Optimistic: count drops before the server answers.
    await screen.findByRole("button", { name: /🎉.*2/ });
    expect(mockDel).toHaveBeenCalledTimes(1);
    expect(mockDel.mock.calls[0][0]).toBe(
      `${REACTIONS_PATH}?emoji=${encodeURIComponent("🎉")}`,
    );
    resolveDel();
    await waitFor(() =>
      expect(mockGet).toHaveBeenCalledWith(REACTIONS_PATH),
    );
  });

  it("picking an emoji from the picker POSTs a JSON {emoji} body", async () => {
    installGet([]);
    let resolvePost!: () => void;
    mockPost.mockImplementation(() => new Promise<void>((r) => (resolvePost = r)));
    renderRow();
    fireEvent.click(await screen.findByRole("button", { name: "Add reaction" }));
    const search = await screen.findByLabelText("Search emoji");
    fireEvent.change(search, { target: { value: "rocket" } });
    const rocket = await screen.findByRole("option", { name: "rocket" });
    fireEvent.click(rocket);
    // The POST fires a microtask after mutate() (React Query v5).
    await waitFor(() => expect(mockPost).toHaveBeenCalledTimes(1));
    expect(mockPost.mock.calls[0][0]).toBe(REACTIONS_PATH);
    expect(mockPost.mock.calls[0][1]).toEqual({ emoji: "🚀" });
    // Optimistic: the new group appears immediately.
    await screen.findByRole("button", { name: /🚀.*1/ });
    resolvePost();
    await waitFor(() => expect(mockGet).toHaveBeenCalledWith(REACTIONS_PATH));
  });

  it("the picker search filters the emoji grid", async () => {
    installGet([]);
    renderRow();
    fireEvent.click(await screen.findByRole("button", { name: "Add reaction" }));
    const search = await screen.findByLabelText("Search emoji");
    fireEvent.change(search, { target: { value: "zzz-no-such-emoji" } });
    expect(await screen.findByText("No emoji match.")).toBeInTheDocument();
    fireEvent.change(search, { target: { value: "trophy" } });
    expect(await screen.findByRole("option", { name: "trophy" })).toBeInTheDocument();
  });

  it("non-members see groups but no picker", async () => {
    installGet([g("🎉", 2, false)], 5); // role 5 < member 15
    renderRow();
    await screen.findByRole("button", { name: /🎉.*2/ });
    expect(screen.queryByRole("button", { name: "Add reaction" })).not.toBeInTheDocument();
  });

  it("renders nothing when empty and the viewer cannot write", async () => {
    installGet([], 5);
    const { container } = render(
      <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
        <IssueReactions slug={SLUG} identifier={IDENT} uuid={UUID} />
      </QueryClientProvider>,
    );
    await waitFor(() => expect(mockGet).toHaveBeenCalled());
    await waitFor(() => expect(container.firstChild).toBeNull());
  });

  it("surfaces an error Alert when the toggle fails", async () => {
    installGet([g("🎉", 1, false)]);
    mockPost.mockRejectedValue(new ApiError(400, "invalid emoji"));
    renderRow();
    const chip = await screen.findByRole("button", { name: /🎉.*1/ });
    fireEvent.click(chip);
    expect(await screen.findByRole("alert")).toHaveTextContent("invalid emoji");
  });

  it("defends against malformed reaction payloads", async () => {
    installGet([{ emoji: "🎉", count: 2 }] as unknown as ReactionGroup[]); // missing reacted
    renderRow();
    const chip = await screen.findByRole("button", { name: /🎉.*2/ });
    expect(chip).toHaveAttribute("aria-pressed", "false");
  });

  it("picking an already-reacted emoji is a no-op (no POST)", async () => {
    installGet([g("🎉", 3, true)]);
    renderRow();
    fireEvent.click(await screen.findByRole("button", { name: "Add reaction" }));
    const search = await screen.findByLabelText("Search emoji");
    fireEvent.change(search, { target: { value: "party popper" } });
    fireEvent.click(await screen.findByRole("option", { name: "party popper" }));
    // The pick guard runs synchronously; flush the task queue so a
    // missed guard (an erroneous mutate) would have fired by now.
    await new Promise((r) => setTimeout(r, 50));
    expect(mockPost).not.toHaveBeenCalled();
    expect(mockDel).not.toHaveBeenCalled();
  });
});
