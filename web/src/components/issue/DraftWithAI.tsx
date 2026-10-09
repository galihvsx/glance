import { useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { Loader2, Sparkles } from "lucide-react";
import { Button } from "../ui/button";
import {
  draftDescription,
  isAINotConfigured,
  isAIProviderFailure,
  useAIAvailable,
} from "../../lib/ai";

export const AI_NOT_CONFIGURED_TOOLTIP = "AI not configured by administrator";

/**
 * "Draft with AI" button for issue description editors (C5T3).
 *
 * - AI unconfigured → button DISABLED with the honest tooltip
 *   "AI not configured by administrator" (never a fake error).
 * - Provider failure (502) → inline error via onError; the button stays.
 * - The drafted description is returned to the caller for the user to
 *   review and edit before saving — nothing is saved automatically.
 */
export default function DraftWithAI({
  slug,
  identifier,
  title,
  context,
  onDraft,
  onError,
  disabled,
}: {
  slug: string;
  identifier: string;
  title: string;
  context?: string;
  onDraft: (description: string) => void;
  onError: (message: string) => void;
  disabled?: boolean;
}) {
  const queryClient = useQueryClient();
  const availability = useAIAvailable();
  const [pending, setPending] = useState(false);

  /** Flip every AI button on the page to the honest disabled state. */
  function markNotConfigured() {
    queryClient.setQueryData(["ai-available"], false);
  }

  async function onClick() {
    if (!title.trim()) {
      onError("Enter a title before drafting with AI.");
      return;
    }
    setPending(true);
    try {
      const description = await draftDescription(
        slug,
        identifier,
        title.trim(),
        context,
      );
      onDraft(description);
    } catch (e) {
      if (isAINotConfigured(e)) {
        markNotConfigured();
        onError("AI is not configured by the administrator.");
      } else if (isAIProviderFailure(e)) {
        onError("The AI provider failed. Try again in a moment.");
      } else {
        onError(e instanceof Error ? e.message : "AI drafting failed.");
      }
    } finally {
      setPending(false);
    }
  }

  // Unknown availability (probe loading or failed ambiguously) leaves the
  // button enabled so the real call can surface the real error.
  const notConfigured = availability.data === false;

  const button = (
    <Button
      type="button"
      variant="outline"
      size="sm"
      className="gap-1.5"
      onClick={onClick}
      disabled={disabled || pending || notConfigured}
      title={
        notConfigured ? AI_NOT_CONFIGURED_TOOLTIP : "Draft a description with AI"
      }
    >
      {pending ? (
        <Loader2 className="h-3.5 w-3.5 animate-spin" aria-hidden />
      ) : (
        <Sparkles className="h-3.5 w-3.5" aria-hidden />
      )}
      {pending ? "Drafting…" : "Draft with AI"}
    </Button>
  );

  // Disabled buttons don't fire mouse events, so the tooltip needs a
  // wrapper to stay reachable.
  return notConfigured ? (
    <span title={AI_NOT_CONFIGURED_TOOLTIP}>{button}</span>
  ) : (
    button
  );
}
