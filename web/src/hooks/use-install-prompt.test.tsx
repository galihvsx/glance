// @vitest-environment jsdom
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { INSTALL_DISMISS_KEY, useInstallPrompt } from "./use-install-prompt";

function Probe() {
  const { canInstall, install, dismiss } = useInstallPrompt();
  return (
    <div>
      <span data-testid="can-install">{String(canInstall)}</span>
      <button type="button" data-testid="install" onClick={() => void install()}>
        install
      </button>
      <button type="button" data-testid="dismiss" onClick={dismiss}>
        dismiss
      </button>
    </div>
  );
}

function fireBeforeInstallPrompt(userChoiceOutcome: "accepted" | "dismissed") {
  const event = new Event("beforeinstallprompt");
  (event as unknown as { prompt: () => Promise<void> }).prompt = vi.fn(
    () => Promise.resolve(),
  );
  (event as unknown as { userChoice: Promise<{ outcome: string }> }).userChoice =
    Promise.resolve({ outcome: userChoiceOutcome });
  // beforeinstallprompt is cancelable and must be prevented; our handler calls preventDefault.
  const prevented = vi.spyOn(event, "preventDefault");
  window.dispatchEvent(event);
  return { event, prevented };
}

describe("useInstallPrompt", () => {
  it("is not installable until beforeinstallprompt fires", () => {
    render(<Probe />);
    expect(screen.getByTestId("can-install")).toHaveTextContent("false");
  });

  it("becomes installable when beforeinstallprompt fires and prevents the default prompt", async () => {
    render(<Probe />);
    const { prevented } = fireBeforeInstallPrompt("accepted");
    await waitFor(() => {
      expect(screen.getByTestId("can-install")).toHaveTextContent("true");
    });
    expect(prevented).toHaveBeenCalled();
  });

  it("install() triggers the deferred prompt and hides after acceptance", async () => {
    render(<Probe />);
    fireBeforeInstallPrompt("accepted");
    await waitFor(() => {
      expect(screen.getByTestId("can-install")).toHaveTextContent("true");
    });
    fireEvent.click(screen.getByTestId("install"));
    await waitFor(() => {
      expect(screen.getByTestId("can-install")).toHaveTextContent("false");
    });
  });

  it("dismiss() hides the affordance and persists the dismissal", async () => {
    render(<Probe />);
    fireBeforeInstallPrompt("accepted");
    await waitFor(() => {
      expect(screen.getByTestId("can-install")).toHaveTextContent("true");
    });
    fireEvent.click(screen.getByTestId("dismiss"));
    expect(localStorage.getItem(INSTALL_DISMISS_KEY)).toBe("1");
    await waitFor(() => {
      expect(screen.getByTestId("can-install")).toHaveTextContent("false");
    });
  });

  it("stays hidden when previously dismissed, even on a new mount", async () => {
    localStorage.setItem(INSTALL_DISMISS_KEY, "1");
    render(<Probe />);
    fireBeforeInstallPrompt("accepted");
    // Give the event a tick to be captured; the hook must still say false.
    await new Promise((r) => setTimeout(r, 20));
    expect(screen.getByTestId("can-install")).toHaveTextContent("false");
  });
});
