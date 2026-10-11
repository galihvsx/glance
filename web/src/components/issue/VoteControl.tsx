import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ArrowBigDown, ArrowBigUp } from "lucide-react";
import { api } from "../../lib/api";
import type { IssueVotes } from "../../lib/types";
import { cn } from "cn";

/**
 * Vote control for the issue detail toolbar (C19T3), spec-exact: 28px
 * tall, border, radius 5px, bg-surface; upvote 27x26px (icon 13px muted);
 * count in mono 9px/26px secondary with 1px side borders and 0 5px
 * padding; downvote 27x26px; downvote count.
 *
 * Wired to GET/POST/DELETE .../issues/{uuid}/votes with an optimistic
 * toggle. The backend has no downvote endpoint, so the downvote side is a
 * static 0 per the prototype (title says so honestly).
 */
export default function VoteControl({
  slug,
  identifier,
  uuid,
}: {
  slug: string;
  identifier: string;
  uuid: string;
}) {
  const queryClient = useQueryClient();
  const votesPath = `/api/v1/workspaces/${encodeURIComponent(slug)}/projects/${encodeURIComponent(identifier)}/issues/${encodeURIComponent(uuid)}/votes`;
  const votesKey = ["votes", slug, identifier, uuid];

  const votesQuery = useQuery({
    queryKey: votesKey,
    queryFn: () => api.get<IssueVotes>(votesPath),
  });
  const voted = votesQuery.data?.voted ?? false;
  const count = votesQuery.data?.count ?? 0;

  const voteMutation = useMutation({
    mutationFn: (next: boolean) =>
      next ? api.post<void>(votesPath) : api.del<void>(votesPath),
    onMutate: async (next) => {
      await queryClient.cancelQueries({ queryKey: votesKey });
      const prev = queryClient.getQueryData<IssueVotes>(votesKey);
      queryClient.setQueryData<IssueVotes>(votesKey, {
        count: Math.max(0, (prev?.count ?? 0) + (next ? 1 : -1)),
        voted: next,
      });
      return { prev };
    },
    onError: (_e, _next, ctx) => {
      if (ctx?.prev) queryClient.setQueryData(votesKey, ctx.prev);
    },
    onSettled: () => {
      void queryClient.invalidateQueries({ queryKey: votesKey });
    },
  });

  function toggleVote() {
    if (voteMutation.isPending) return;
    voteMutation.mutate(!voted);
  }

  return (
    <div
      className="flex h-7 items-center rounded-[5px] border border-border bg-surface"
      role="group"
      aria-label="Issue votes"
    >
      <button
        type="button"
        onClick={toggleVote}
        disabled={voteMutation.isPending}
        title={voted ? "Remove your vote" : "Upvote this issue"}
        aria-pressed={voted}
        className={cn(
          "flex h-[26px] w-[27px] items-center justify-center rounded-l-[4px] transition-colors",
          voted
            ? "bg-accent-blue/15 text-accent-blue"
            : "text-ink-muted hover:bg-wash-hover hover:text-ink",
        )}
      >
        <ArrowBigUp className="h-[13px] w-[13px]" strokeWidth={1.8} />
      </button>
      <span
        className="flex h-[26px] items-center border-x border-border px-[5px] font-mono text-[9px] leading-[26px] text-ink-secondary"
        aria-label={`${count} upvotes`}
      >
        {count}
      </span>
      <button
        type="button"
        title="Downvotes are not supported"
        aria-label="0 downvotes (not supported)"
        className="flex h-[26px] w-[27px] cursor-default items-center justify-center text-ink-muted/50"
      >
        <ArrowBigDown className="h-[13px] w-[13px]" strokeWidth={1.8} />
      </button>
      <span className="flex h-[26px] items-center pr-[5px] font-mono text-[9px] leading-[26px] text-ink-muted/70">
        0
      </span>
    </div>
  );
}
