// @vitest-environment jsdom
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, describe, expect, it, vi } from "vitest";
import IssueBreakdown, { tiptapDoc } from "./IssueBreakdown";
import { ApiError, api } from "../../lib/api";
import { breakDownIssue } from "../../lib/ai";
import { toast } from "../ui/toast";
import { AI_NOT_CONFIGURED_TOOLTIP } from "./DraftWithAI";

// breakDownIssue is mocked; useAIAvailable stays real so the tests
// exercise the actual ["ai-available"] shared-state contract (seeded
// per test). api.post (sub-issue creates) and toast.add are mocked.
vi.mock("../../lib/ai", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../../lib/ai")>();
  return { ...actual, breakDownIssue: vi.fn() };
});
vi.mock("../../lib/api", () => {
  class ApiError extends Error {
    readonly status: number;
    readonly body: string;
    constructor(status: number, body: string) {
      super(body || `Request failed with status ${status}`);
      this.name = "ApiError";
      this.status = status;
      this.body = body;
    }
  }
  return { ApiError, api: { get: vi.fn(), post: vi.fn(), patch: vi.fn() } };
});
vi.mock("../ui/toast", () => ({ toast: { add: vi.fn() } }));

const mockBreakDown = vi.mocked(breakDownIssue);
const mockPost = api.post as unknown as ReturnType<typeof vi.fn>;
const mockToastAdd = vi.mocked(toast.add);

const suggestions = [
  { title: "Add provider config", description: "Wire the endpoints." },
  { title: "Swap session middleware", description: null },
  { title: "Update docs", description: "Document the new flow." },
];

function freshClient(aiAvailable = true) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  client.setQueryData(["ai-available"], aiAvailable);
  return client;
}

function renderBreakdown(opts: { aiAvailable?: boolean } = {}) {
  const client = freshClient(opts.aiAvailable ?? true);
  const ui = render(
    <QueryClientProvider client={client}>
      <IssueBreakdown
        slug="acme"
        identifier="ENG"
        issue={{ id: "issue-1", name: "Migrate auth" } as never}
      />
    </QueryClientProvider>,
  );
  return { client, ...ui };
}

afterEach(() => {
  vi.resetAllMocks();
});

describe("IssueBreakdown", () => {
  it("opens the dialog with all suggestions checked", async () => {
    mockBreakDown.mockResolvedValue(suggestions);

    const user = userEvent.setup();
    renderBreakdown();
    await user.click(screen.getByRole("button", { name: "Break down" }));

    await waitFor(() =>
      expect(
        screen.getByRole("dialog"),
      ).toBeInTheDocument(),
    );
    expect(mockBreakDown).toHaveBeenCalledWith("acme", "ENG", "issue-1");
    for (const s of suggestions) {
      expect(screen.getByDisplayValue(s.title)).toBeInTheDocument();
    }
    // All checkboxes checked by default.
    const checkboxes = screen.getAllByRole("checkbox");
    expect(checkboxes).toHaveLength(3);
    for (const cb of checkboxes) {
      expect(cb).toBeChecked();
    }
    expect(
      screen.getByRole("button", { name: "Create 3 sub-issues" }),
    ).toBeInTheDocument();
  });

  it("creates the selected sub-issues via the existing create endpoint and toasts the count", async () => {
    mockBreakDown.mockResolvedValue(suggestions);
    mockPost.mockResolvedValue({ id: "new-issue" });

    const user = userEvent.setup();
    const { client } = renderBreakdown();
    const invalidate = vi.spyOn(client, "invalidateQueries");

    await user.click(screen.getByRole("button", { name: "Break down" }));
    await waitFor(() =>
      expect(
        screen.getByRole("dialog"),
      ).toBeInTheDocument(),
    );

    // Uncheck the second suggestion → count drops to 2.
    const checkboxes = screen.getAllByRole("checkbox");
    await user.click(checkboxes[1]);
    expect(
      screen.getByRole("button", { name: "Create 2 sub-issues" }),
    ).toBeInTheDocument();

    // Edit a title inline.
    const titleInput = screen.getByDisplayValue("Add provider config");
    await user.clear(titleInput);
    await user.type(titleInput, "Add provider config v2");

    await user.click(screen.getByRole("button", { name: "Create 2 sub-issues" }));

    await waitFor(() =>
      expect(
        screen.queryByRole("dialog"),
      ).not.toBeInTheDocument(),
    );
    const base = "/api/v1/workspaces/acme/projects/ENG";
    expect(mockPost).toHaveBeenCalledTimes(2);
    expect(mockPost).toHaveBeenCalledWith(`${base}/issues`, {
      name: "Add provider config v2",
      parent_id: "issue-1",
      description: tiptapDoc("Wire the endpoints."),
    });
    expect(mockPost).toHaveBeenCalledWith(`${base}/issues`, {
      name: "Update docs",
      parent_id: "issue-1",
      description: tiptapDoc("Document the new flow."),
    });
    expect(mockToastAdd).toHaveBeenCalledWith({
      title: "Created 2 sub-issues",
      type: "success",
    });
    expect(invalidate).toHaveBeenCalledWith({
      queryKey: ["issue", "acme", "ENG"],
    });
    expect(invalidate).toHaveBeenCalledWith({
      queryKey: ["issues", "acme", "ENG"],
    });
  });

  it("omits the description field when the suggestion has none", async () => {
    mockBreakDown.mockResolvedValue([
      { title: "No description here", description: null },
    ]);
    mockPost.mockResolvedValue({ id: "new-issue" });

    const user = userEvent.setup();
    renderBreakdown();
    await user.click(screen.getByRole("button", { name: "Break down" }));
    await waitFor(() =>
      expect(
        screen.getByRole("dialog"),
      ).toBeInTheDocument(),
    );
    await user.click(screen.getByRole("button", { name: "Create 1 sub-issue" }));

    await waitFor(() => expect(mockPost).toHaveBeenCalledTimes(1));
    const [, body] = mockPost.mock.calls[0] as [string, Record<string, unknown>];
    expect(body).toEqual({ name: "No description here", parent_id: "issue-1" });
  });

  it("disables the button with the honest tooltip when AI is not configured", () => {
    const { container } = renderBreakdown({ aiAvailable: false });
    const button = screen.getByRole("button", { name: "Break down" });
    expect(button).toBeDisabled();
    const wrapper = container.querySelector(
      `span[title="${AI_NOT_CONFIGURED_TOOLTIP}"]`,
    );
    expect(wrapper).not.toBeNull();
    expect(wrapper).toContainElement(button);
    expect(mockBreakDown).not.toHaveBeenCalled();
  });

  it("shows an inline error on provider failure", async () => {
    mockBreakDown.mockRejectedValue(new ApiError(502, "boom"));

    const user = userEvent.setup();
    renderBreakdown();
    await user.click(screen.getByRole("button", { name: "Break down" }));

    await waitFor(() =>
      expect(screen.getByRole("alert")).toHaveTextContent(
        "The AI provider failed. Try again in a moment.",
      ),
    );
    // No dialog on failure — the suggestions never arrived.
    expect(
      screen.queryByRole("dialog"),
    ).not.toBeInTheDocument();
  });

  it("flips the button to the honest disabled state on 503", async () => {
    mockBreakDown.mockRejectedValue(new ApiError(503, "not configured"));

    const user = userEvent.setup();
    const { container } = renderBreakdown();
    await user.click(screen.getByRole("button", { name: "Break down" }));

    await waitFor(() => {
      const wrapper = container.querySelector(
        `span[title="${AI_NOT_CONFIGURED_TOOLTIP}"]`,
      );
      expect(wrapper).not.toBeNull();
    });
    expect(screen.getByRole("button", { name: "Break down" })).toBeDisabled();
  });
});

describe("tiptapDoc", () => {
  it("wraps text in a minimal TipTap doc", () => {
    expect(tiptapDoc("hello")).toEqual({
      type: "doc",
      content: [
        { type: "paragraph", content: [{ type: "text", text: "hello" }] },
      ],
    });
  });
});
