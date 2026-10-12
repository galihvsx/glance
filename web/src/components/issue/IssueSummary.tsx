import { useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Loader2, RefreshCw, Sparkles } from "lucide-react";
import { Button } from "../ui/button";
import {
  isAINotConfigured,
  isAIProviderFailure,
  summarizeIssue,
  useAIAvailable,
} from "../../lib/ai";
import type { Issue } from "../../lib/types";
import { AI_NOT_CONFIGURED_TOOLTIP } from "./DraftWithAI";

/**
 * "Summarize" for the issue detail (C25T0), rendered just below the
 * description block.
 *
 * - Clicking Summarize fetches POST /api/v1/ai/summarize once and shows
 *   the inline summary card; Regenerate refetches.
 * - The result is cached per issue via react-query
 *   (queryKey ["ai-summary", slug, identifier, issue.id], staleTime
 *   Infinity), so switching issues keeps each summary without refetch.
 * - AI unconfigured → buttons DISABLED with the honest tooltip
 *   "AI not configured by administrator" (shared with DraftWithAI).
 * - Provider failure (502) → inline error in the card; cached summaries
 *   stay visible.
 */
export default function IssueSummary({
  slug,
  identifier,
  issue,
}: {
  slug: string;
  identifier: string;
  issue: Pick<Issue, "id">;
}) {
  const queryClient = useQueryClient();
  const availability = useAIAvailable();
  const [error, setError] = useState<string | null>(null);

  // Lazy fetch: the first Summarize click fills the per-issue cache;
  // regenerate just refetches the same key.
  const summaryQuery = useQuery({
    queryKey: ["ai-summary", slug, identifier, issue.id],
    queryFn: () => summarizeIssue(slug, identifier, issue.id),
    enabled: false,
    staleTime: Infinity,
    gcTime: 30 * 60 * 1000,
    retry: false,
    refetchOnWindowFocus: false,
  });

  const notConfigured = availability.data === false;
  const pending = summaryQuery.isFetching;
  const summary = summaryQuery.data;

  async function generate() {
    setError(null);
    const result = await summaryQuery.refetch();
    const e = result.error;
    if (!e) return;
    if (isAINotConfigured(e)) {
      // Flip every AI button on the page to the honest disabled state.
      queryClient.setQueryData(["ai-available"], false);
      setError("AI is not configured by the administrator.");
    } else if (isAIProviderFailure(e)) {
      setError("The AI provider failed. Try again in a moment.");
    } else {
      setError(e instanceof Error ? e.message : "Summarization failed.");
    }
  }

  function summarizeButton(label: string) {
    const button = (
      <Button
        type="button"
        variant="outline"
        size="sm"
        className="gap-1.5"
        onClick={generate}
        disabled={pending || notConfigured}
        title={
          notConfigured
            ? AI_NOT_CONFIGURED_TOOLTIP
            : "Summarize this issue with AI"
        }
      >
        {pending ? (
          <Loader2 className="h-3.5 w-3.5 animate-spin" aria-hidden />
        ) : label === "Regenerate" ? (
          <RefreshCw className="h-3.5 w-3.5" aria-hidden />
        ) : (
          <Sparkles className="h-3.5 w-3.5" aria-hidden />
        )}
        {pending ? "Summarizing…" : label}
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

  return (
    <section aria-label="AI summary" className="mt-[6px]">
      {!summary && (
        <div className="flex items-center gap-2">
          {summarizeButton("Summarize")}
          <span className="text-[10px] text-ink-muted">
            AI TL;DR from title, description, comments and activity
          </span>
        </div>
      )}
      {error && (
        <p role="alert" className="mt-2 text-[11px] text-accent-red">
          {error}
        </p>
      )}
      {summary && (
        <div className="rounded-[7px] border border-border bg-wash p-3">
          <div className="mb-1.5 flex items-center justify-between gap-2">
            <h3 className="flex items-center gap-1.5 text-[11px] font-semibold text-ink">
              <Sparkles className="h-3.5 w-3.5" aria-hidden />
              AI summary
            </h3>
            {summarizeButton("Regenerate")}
          </div>
          <p className="text-[12px] leading-[20px] whitespace-pre-wrap text-ink-secondary">
            {summary}
          </p>
        </div>
      )}
    </section>
  );
}
