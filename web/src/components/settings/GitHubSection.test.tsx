// @vitest-environment jsdom
// Component tests for the GitHub integration settings section (C62T0):
// configured/unconfigured badge states, save flow, clear flow (with and
// without confirmation), client-side secret validation, save-failure
// message, copy-webhook-URL, and the never-echo-secret contract (the
// backend only returns github_webhook_configured, so the stored secret
// must never appear in the DOM).

import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi, type Mock } from "vitest";
import GitHubSection from "./GitHubSection";
import { api, ApiError } from "../../lib/api";
import { copyText } from "../../lib/clipboard";

vi.mock("../../lib/api", async (importOriginal) => {
  const mod = await importOriginal<typeof import("../../lib/api")>();
  return {
    ...mod,
    api: {
      ...mod.api,
      patch: vi.fn(),
    },
  };
});
vi.mock("../../lib/clipboard", () => ({ copyText: vi.fn() }));
vi.mock("../ui/toast", () => ({ toast: { add: vi.fn() } }));

const patchMock = api.patch as unknown as Mock;
const copyMock = copyText as unknown as Mock;

const WS_PATH = "/api/v1/workspaces/acme";
const WEBHOOK_URL = `${window.location.origin}/api/v1/integrations/github/webhook`;

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  patchMock.mockReset();
  copyMock.mockReset();
});

function renderSection(initialConfigured: boolean) {
  return render(<GitHubSection slug="acme" initialConfigured={initialConfigured} />);
}

describe("GitHubSection", () => {
  it("renders the not-configured state with setup instructions and the webhook URL", () => {
    renderSection(false);

    expect(screen.getByText("Not configured")).toBeInTheDocument();
    expect(screen.getByLabelText(/webhook secret/i)).toBeInTheDocument();
    // The inbound URL the repo admin pastes into GitHub.
    expect(screen.getByText(WEBHOOK_URL)).toBeInTheDocument();
    // Setup instructions mention the events to subscribe to.
    expect(screen.getByText("Pull requests")).toBeInTheDocument();
    // Honest scope: auto-link only, no issue state sync on PR close.
    expect(
      screen.getByText(/never moves issues between states/i),
    ).toBeInTheDocument();
  });

  it("renders the configured badge and starts with an empty secret input (never echoes)", () => {
    renderSection(true);

    expect(screen.getByText("Configured")).toBeInTheDocument();
    const input = screen.getByLabelText(/webhook secret/i) as HTMLInputElement;
    expect(input.value).toBe("");
    expect(input.type).toBe("password");
    expect(screen.getByText("Remove")).toBeInTheDocument();
  });

  it("save flow patches the secret, flips the badge, and never echoes the secret", async () => {
    patchMock.mockResolvedValue({ github_webhook_configured: true });
    renderSection(false);

    fireEvent.change(screen.getByLabelText(/webhook secret/i), {
      target: { value: "s3cret-token-abc" },
    });
    fireEvent.click(screen.getByRole("button", { name: /^save$/i }));

    await waitFor(() =>
      expect(patchMock).toHaveBeenCalledWith(WS_PATH, {
        github_webhook_secret: "s3cret-token-abc",
      }),
    );
    expect(await screen.findByText("Configured")).toBeInTheDocument();
    // The input is cleared on success — the secret must not be echoed back.
    expect(screen.queryByDisplayValue("s3cret-token-abc")).toBeNull();
    expect(document.body.textContent).not.toContain("s3cret-token-abc");
  });

  it("save flow rejects an empty secret client-side without calling the API", () => {
    renderSection(false);

    fireEvent.click(screen.getByRole("button", { name: /^save$/i }));

    expect(screen.getByText(/paste the webhook secret/i)).toBeInTheDocument();
    expect(patchMock).not.toHaveBeenCalled();
  });

  it("save flow rejects a secret with whitespace client-side (matches the backend contract)", () => {
    renderSection(false);

    fireEvent.change(screen.getByLabelText(/webhook secret/i), {
      target: { value: "has a space" },
    });
    fireEvent.click(screen.getByRole("button", { name: /^save$/i }));

    expect(screen.getByText(/no spaces/i)).toBeInTheDocument();
    expect(patchMock).not.toHaveBeenCalled();
  });

  it("save flow surfaces backend failures", async () => {
    patchMock.mockRejectedValue(
      new ApiError(
        400,
        "github_webhook_secret must be 1-256 printable ASCII characters without whitespace",
      ),
    );
    renderSection(false);

    fireEvent.change(screen.getByLabelText(/webhook secret/i), {
      target: { value: "server-rejects-me" },
    });
    fireEvent.click(screen.getByRole("button", { name: /^save$/i }));

    expect(
      await screen.findByText(/printable ASCII characters without whitespace/i),
    ).toBeInTheDocument();
  });

  it("clear flow confirms, patches null, and flips the badge back", async () => {
    vi.spyOn(window, "confirm").mockReturnValue(true);
    patchMock.mockResolvedValue({ github_webhook_configured: false });
    renderSection(true);

    fireEvent.click(screen.getByRole("button", { name: /remove/i }));

    await waitFor(() =>
      expect(patchMock).toHaveBeenCalledWith(WS_PATH, {
        github_webhook_secret: null,
      }),
    );
    expect(await screen.findByText("Not configured")).toBeInTheDocument();
  });

  it("clear flow does nothing when the confirmation is cancelled", () => {
    vi.spyOn(window, "confirm").mockReturnValue(false);
    renderSection(true);

    fireEvent.click(screen.getByRole("button", { name: /remove/i }));

    expect(patchMock).not.toHaveBeenCalled();
    expect(screen.getByText("Configured")).toBeInTheDocument();
  });

  it("copy button copies the inbound webhook URL", async () => {
    copyMock.mockResolvedValue(true);
    renderSection(false);

    fireEvent.click(screen.getByRole("button", { name: /^copy$/i }));

    await waitFor(() => expect(copyMock).toHaveBeenCalledWith(WEBHOOK_URL));
    expect(await screen.findByText("Copied")).toBeInTheDocument();
  });
});
