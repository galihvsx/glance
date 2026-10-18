// Issue-level reactions (C67T1): the reaction row on the issue
// detail, beside the title meta. Contract (verified against the
// backend this cycle):
//   GET    .../issues/{uuid}/reactions          -> {reactions: []}
//   POST   .../issues/{uuid}/reactions          JSON body {emoji}
//   DELETE .../issues/{uuid}/reactions?emoji=
// Reads are member (guest 5+); mutations require member 15+ — the UI
// only hides what would 403 (picker + toggles for member 15+), the
// server enforces it. Optimistic toggle via the shared lib/reactions
// helpers; the row + picker are the shared ReactionRow.

import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, ApiError } from "../../lib/api";
import { useAuth } from "../../lib/auth";
import { normalizeGroups, sortGroups, toggleGroup } from "../../lib/reactions";
import type { Member, ReactionGroup } from "../../lib/types";
import ReactionRow from "../reactions/ReactionRow";

const ROLE_MEMBER = 15;

export default function IssueReactions({
  slug,
  identifier,
  uuid,
}: {
  slug: string;
  identifier: string;
  uuid: string;
}) {
  const { user } = useAuth();
  const queryClient = useQueryClient();
  const base = `/api/v1/workspaces/${encodeURIComponent(slug)}/projects/${encodeURIComponent(identifier)}/issues/${encodeURIComponent(uuid)}`;
  const reactionsPath = `${base}/reactions`;
  const reactionsKey = ["issueReactions", slug, identifier, uuid];

  const reactionsQuery = useQuery({
    queryKey: reactionsKey,
    queryFn: () =>
      api
        .get<{ reactions: ReactionGroup[] }>(reactionsPath)
        .then((d) => d.reactions),
  });

  const membersQuery = useQuery({
    queryKey: ["members", slug],
    queryFn: () =>
      api
        .get<{ members: Member[] }>(
          `/api/v1/workspaces/${encodeURIComponent(slug)}/members`,
        )
        .then((d) => d.members),
  });

  const myRole = membersQuery.data?.find((m) => m.id === user?.id)?.role ?? 0;
  const canWrite = myRole >= ROLE_MEMBER;

  const [error, setError] = useState<string | null>(null);

  const groups = sortGroups(normalizeGroups(reactionsQuery.data));

  function optimisticToggle(emoji: string, add: boolean) {
    queryClient.setQueryData<ReactionGroup[]>(reactionsKey, (old) =>
      toggleGroup(normalizeGroups(old), emoji, add),
    );
  }

  const toggleMutation = useMutation({
    mutationFn: ({ emoji, add }: { emoji: string; add: boolean }) =>
      add
        ? api.post(reactionsPath, { emoji })
        : api.del(`${reactionsPath}?emoji=${encodeURIComponent(emoji)}`),
    onMutate: ({ emoji, add }) => {
      setError(null);
      optimisticToggle(emoji, add);
    },
    onError: (e) => {
      setError(e instanceof ApiError ? e.message : "Failed to update reaction");
      void queryClient.invalidateQueries({ queryKey: reactionsKey });
    },
    onSettled: () => {
      void queryClient.invalidateQueries({ queryKey: reactionsKey });
    },
  });

  function toggle(g: ReactionGroup) {
    if (!canWrite || toggleMutation.isPending) return;
    toggleMutation.mutate({ emoji: g.emoji, add: !g.reacted });
  }

  function pick(emoji: string) {
    if (!canWrite || toggleMutation.isPending) return;
    // Picking an already-reacted emoji is a no-op on the server too;
    // only add here so the picker never silently removes a reaction.
    const existing = groups.find((g) => g.emoji === emoji);
    if (existing?.reacted) return;
    toggleMutation.mutate({ emoji, add: true });
  }

  if (!reactionsQuery.data && reactionsQuery.isPending) return null;
  if (groups.length === 0 && !canWrite) return null;

  return (
    <ReactionRow
      groups={groups}
      canWrite={canWrite}
      pending={toggleMutation.isPending}
      error={error}
      onToggle={toggle}
      onPick={pick}
    />
  );
}
