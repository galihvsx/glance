// Subtle PWA install affordance (C15T4).
//
// Captures the `beforeinstallprompt` event so the UI can surface an install
// button on its own terms. Dismissal persists in localStorage; an already
// installed app (display-mode: standalone) never shows the prompt.

import { useCallback, useEffect, useState } from "react";

export const INSTALL_DISMISS_KEY = "glance-pwa-install-dismissed";

interface BeforeInstallPromptEvent extends Event {
  prompt: () => Promise<void>;
  userChoice: Promise<{ outcome: "accepted" | "dismissed" }>;
}

function isStandalone(): boolean {
  try {
    return (
      window.matchMedia("(display-mode: standalone)").matches ||
      // iOS Safari
      (window.navigator as unknown as { standalone?: boolean }).standalone === true
    );
  } catch {
    return false;
  }
}

export function useInstallPrompt() {
  const [deferred, setDeferred] = useState<BeforeInstallPromptEvent | null>(null);
  const [dismissed, setDismissed] = useState<boolean>(() => {
    try {
      return localStorage.getItem(INSTALL_DISMISS_KEY) === "1";
    } catch {
      return false;
    }
  });
  const [installed] = useState<boolean>(() => isStandalone());

  useEffect(() => {
    const onPrompt = (e: Event) => {
      // Defer the browser's automatic prompt; we show our own subtle button.
      e.preventDefault();
      setDeferred(e as BeforeInstallPromptEvent);
    };
    window.addEventListener("beforeinstallprompt", onPrompt);
    return () => window.removeEventListener("beforeinstallprompt", onPrompt);
  }, []);

  const canInstall = deferred !== null && !dismissed && !installed;

  const install = useCallback(async () => {
    if (!deferred) return;
    await deferred.prompt();
    const { outcome } = await deferred.userChoice;
    if (outcome === "accepted") setDeferred(null);
  }, [deferred]);

  const dismiss = useCallback(() => {
    try {
      localStorage.setItem(INSTALL_DISMISS_KEY, "1");
    } catch {
      // Private mode etc. — dismissal just won't persist.
    }
    setDismissed(true);
  }, []);

  return { canInstall, install, dismiss };
}
