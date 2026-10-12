// @vitest-environment jsdom
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, describe, expect, it, vi } from "vitest";
import IssueSummary from "./IssueSummary";
import { ApiError } from "../../lib/api";
import { summarizeIssue } from "../../lib/ai";
import { AI_NOT_CONFIGURED_TOOLTIP } from "./DraftWithAI";

// Mock only the network call; useAIAvailable stays real so the tests
// exercise the actual ["ai-available"] shared-state contract (seeded
// per test below).
vi.mock("../../lib/ai", async (importOriginal) => {
  const actual =
    await importOriginal<typeof import("../../lib/ai")>();
  return {
    ...actual,
    summarizeIssue: vi.fn(),
  };
});

const mockSummarize = vi.mocked(summarizeIssue);

function freshClient(aiAvailable = true) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  client.setQueryData(["ai-available"], aiAvailable);
  return client;
}

function renderWidget(
  issueId = "issue-1",
  opts: { aiAvailable?: boolean; client?: QueryClient } = {},
) {
  const client = opts.client ?? freshClient(opts.aiAvailable ?? true);
  const ui = render(
    <QueryClientProvider client={client}>
      <IssueSummary slug="acme" identifier="ENG" issue={{ id: issueId }} />
    </QueryClientProvider>,
  );
  return { client, ...ui };
}

afterEach(() => {
  vi.resetAllMocks();
});

describe("IssueSummary", () => {
  it("fetches and shows the summary card on click", async () => {
    mockSummarize.mockResolvedValue("What: broken login button.");

    const user = userEvent.setup();
    renderWidget();
    await user.click(screen.getByRole("button", { name: "Summarize" }));

    await waitFor(() =>
      expect(
        screen.getByText("What: broken login button."),
      ).toBeInTheDocument(),
    );
    expect(mockSummarize).toHaveBeenCalledTimes(1);
    expect(mockSummarize).toHaveBeenCalledWith("acme", "ENG", "issue-1");
    // Regenerate affordance appears on the card.
    expect(
      screen.getByRole("button", { name: "Regenerate" }),
    ).toBeInTheDocument();
  });

  it("regenerate refetches the summary", async () => {
    mockSummarize
      .mockResolvedValueOnce("First summary.")
      .mockResolvedValueOnce("Second summary.");

    const user = userEvent.setup();
    renderWidget();
    await user.click(screen.getByRole("button", { name: "Summarize" }));
    await waitFor(() =>
      expect(screen.getByText("First summary.")).toBeInTheDocument(),
    );
    await user.click(screen.getByRole("button", { name: "Regenerate" }));
    await waitFor(() =>
      expect(screen.getByText("Second summary.")).toBeInTheDocument(),
    );
    expect(mockSummarize).toHaveBeenCalledTimes(2);
  });

  it("caches per issue: switching issues shows the button-only state", async () => {
    mockSummarize.mockResolvedValue("Summary for issue 1.");

    const user = userEvent.setup();
    const client = freshClient();
    const { rerender } = render(
      <QueryClientProvider client={client}>
        <IssueSummary slug="acme" identifier="ENG" issue={{ id: "issue-1" }} />
      </QueryClientProvider>,
    );
    await user.click(screen.getByRole("button", { name: "Summarize" }));
    await waitFor(() =>
      expect(screen.getByText("Summary for issue 1.")).toBeInTheDocument(),
    );

    rerender(
      <QueryClientProvider client={client}>
        <IssueSummary slug="acme" identifier="ENG" issue={{ id: "issue-2" }} />
      </QueryClientProvider>,
    );
    // New issue → fresh cache key → button-only state, no summary.
    expect(
      screen.getByRole("button", { name: "Summarize" }),
    ).toBeInTheDocument();
    expect(screen.queryByText("Summary for issue 1.")).not.toBeInTheDocument();
  });

  it("reuses the cached summary without refetching when remounted for the same issue", async () => {
    mockSummarize.mockResolvedValue("Cached summary.");

    const user = userEvent.setup();
    const client = freshClient();
    const { unmount } = render(
      <QueryClientProvider client={client}>
        <IssueSummary slug="acme" identifier="ENG" issue={{ id: "issue-1" }} />
      </QueryClientProvider>,
    );
    await user.click(screen.getByRole("button", { name: "Summarize" }));
    await waitFor(() =>
      expect(screen.getByText("Cached summary.")).toBeInTheDocument(),
    );
    unmount();

    render(
      <QueryClientProvider client={client}>
        <IssueSummary slug="acme" identifier="ENG" issue={{ id: "issue-1" }} />
      </QueryClientProvider>,
    );
    // Same issue → cached data renders without another fetch.
    expect(screen.getByText("Cached summary.")).toBeInTheDocument();
    expect(mockSummarize).toHaveBeenCalledTimes(1);
  });

  it("disables the button with the honest tooltip when AI is not configured", () => {
    const { container } = renderWidget("issue-1", { aiAvailable: false });
    const button = screen.getByRole("button", { name: "Summarize" });
    expect(button).toBeDisabled();
    const wrapper = container.querySelector(
      `span[title="${AI_NOT_CONFIGURED_TOOLTIP}"]`,
    );
    expect(wrapper).not.toBeNull();
    expect(wrapper).toContainElement(button);
  });

  it("keeps the cached summary visible but disables regenerate when AI is not configured", async () => {
    mockSummarize.mockResolvedValue("Cached summary.");

    const user = userEvent.setup();
    const client = freshClient();
    const { rerender } = render(
      <QueryClientProvider client={client}>
        <IssueSummary slug="acme" identifier="ENG" issue={{ id: "issue-1" }} />
      </QueryClientProvider>,
    );
    await user.click(screen.getByRole("button", { name: "Summarize" }));
    await waitFor(() =>
      expect(screen.getByText("Cached summary.")).toBeInTheDocument(),
    );

    // Server flipped to unconfigured mid-session.
    client.setQueryData(["ai-available"], false);
    rerender(
      <QueryClientProvider client={client}>
        <IssueSummary slug="acme" identifier="ENG" issue={{ id: "issue-1" }} />
      </QueryClientProvider>,
    );
    expect(screen.getByText("Cached summary.")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Regenerate" })).toBeDisabled();
  });

  it("shows an inline error on provider failure", async () => {
    mockSummarize.mockRejectedValue(new ApiError(502, "boom"));

    const user = userEvent.setup();
    renderWidget();
    await user.click(screen.getByRole("button", { name: "Summarize" }));

    await waitFor(() =>
      expect(screen.getByRole("alert")).toHaveTextContent(
        "The AI provider failed. Try again in a moment.",
      ),
    );
    expect(mockSummarize).toHaveBeenCalledTimes(1);
  });

  it("flips the button to the honest disabled state on 503", async () => {
    mockSummarize.mockRejectedValue(new ApiError(503, "not configured"));

    const user = userEvent.setup();
    const { container } = renderWidget();
    await user.click(screen.getByRole("button", { name: "Summarize" }));

    await waitFor(() =>
      expect(screen.getByRole("alert")).toHaveTextContent(
        "AI is not configured by the administrator.",
      ),
    );
    // The widget marked ai-available=false, so the real useAIAvailable
    // hook now reports unconfigured → disabled button with tooltip.
    await waitFor(() => {
      const wrapper = container.querySelector(
        `span[title="${AI_NOT_CONFIGURED_TOOLTIP}"]`,
      );
      expect(wrapper).not.toBeNull();
    });
  });
});
