// Activity feed (C5T7): /w/:slug/p/:identifier/activity.
//
// Project-wide, newest-first feed of the issue_activities field-level
// audit rows. Rows render "actor → issue → field change" with relative
// times; the search box filters by issue identifier, actor, or field.
// Honest footnote: only field changes that wrote audit rows appear —
// creation, per-field updates, and deletion are logged, but reads,
// comments, and assignment-only notification events are not.

import { useMemo, useState } from "react";
import { Link, useParams } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import { Search } from "lucide-react";
import ProjectNav from "../components/project/ProjectNav";
import { Avatar, AvatarFallback } from "../components/ui/avatar";
import { Input } from "../components/ui/input";
import { api } from "../lib/api";
import { relativeTime } from "../lib/relativeTime";
import {
  describeChange,
  matchesActivityFilter,
  type ActivityEntry,
} from "../lib/activity";
import type { IssueState } from "../lib/types";
import { cn } from "../lib/utils";

function actorInitial(name: string): string {
  return (name.trim()[0] ?? "?").toUpperCase();
}

function ChangeText({
  entry,
  stateById,
}: {
  entry: ActivityEntry;
  stateById: Map<string, string>;
}) {
  const d = describeChange(entry, stateById);
  switch (d.kind) {
    case "created":
      return <>created the issue</>;
    case "deleted":
      return <>deleted the issue</>;
    case "edited":
      return <>edited the description</>;
    case "set":
      return (
        <>
          set <ChangeField label={d.label} /> to{" "}
          <ChangeValue text={d.newText ?? ""} />
        </>
      );
    case "cleared":
      return (
        <>
          cleared <ChangeField label={d.label} />
        </>
      );
    default:
      return (
        <>
          changed <ChangeField label={d.label} /> from{" "}
          <ChangeValue text={d.oldText ?? ""} /> to{" "}
          <ChangeValue text={d.newText ?? ""} />
        </>
      );
  }
}

function ChangeField({ label }: { label: string }) {
  return <span className="font-medium">{label}</span>;
}

function ChangeValue({ text }: { text: string }) {
  return (
    <code className="rounded bg-muted px-1 py-0.5 font-mono text-xs">
      {text}
    </code>
  );
}

function ActivityRow({
  entry,
  stateById,
  issueBase,
}: {
  entry: ActivityEntry;
  stateById: Map<string, string>;
  issueBase: string;
}) {
  return (
    <div className="flex items-start gap-3 border-b px-4 py-3 last:border-b-0">
      <Avatar className="h-8 w-8 shrink-0">
        <AvatarFallback className="text-xs font-semibold">
          {actorInitial(entry.actor)}
        </AvatarFallback>
      </Avatar>
      <div className="min-w-0 flex-1">
        <p className="text-sm leading-snug">
          <span className="font-medium">{entry.actor}</span>{" "}
          <ChangeText entry={entry} stateById={stateById} />{" "}
          <Link
            to={`${issueBase}/i/${entry.issue_uuid}`}
            className="font-medium text-primary hover:underline"
          >
            {entry.issue_identifier}
          </Link>
        </p>
        <p className="mt-0.5 text-xs text-muted-foreground">
          {relativeTime(entry.at)}
        </p>
      </div>
    </div>
  );
}

export default function Activity() {
  const { slug = "", identifier = "" } = useParams<{
    slug: string;
    identifier: string;
  }>();
  const base = `/api/v1/workspaces/${slug}/projects/${identifier}`;
  const issueBase = `/w/${slug}/p/${identifier}`;
  const [query, setQuery] = useState("");

  const activityQuery = useQuery({
    queryKey: ["activity", slug, identifier],
    queryFn: () =>
      api
        .get<{ activity: ActivityEntry[] }>(`${base}/activity?limit=50`)
        .then((d) => d.activity),
  });
  const statesQuery = useQuery({
    queryKey: ["states", slug, identifier],
    queryFn: () =>
      api.get<{ states: IssueState[] }>(`${base}/states`).then((d) => d.states),
  });

  const stateById = useMemo(() => {
    const m = new Map<string, string>();
    for (const s of statesQuery.data ?? []) m.set(s.id, s.name);
    return m;
  }, [statesQuery.data]);

  const rows = useMemo(() => {
    const all = activityQuery.data ?? [];
    return all.filter((e) => matchesActivityFilter(e, query));
  }, [activityQuery.data, query]);

  return (
    <div className="flex h-full flex-col gap-4 overflow-y-auto p-4">
      <ProjectNav />
      <div className="flex items-center justify-between gap-4">
        <h1 className="text-lg font-semibold">Activity</h1>
        <div className="relative w-64">
          <Search className="pointer-events-none absolute left-2.5 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
          <Input
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder="Filter by issue, actor, or field…"
            className="pl-8"
            aria-label="Filter activity"
          />
        </div>
      </div>

      <div
        className={cn(
          "overflow-hidden rounded-lg border bg-card",
          rows.length === 0 && "border-dashed",
        )}
      >
        {activityQuery.isPending || statesQuery.isPending ? (
          <div className="py-8 text-center text-sm text-muted-foreground">
            Loading…
          </div>
        ) : activityQuery.isError ? (
          <div className="py-8 text-center text-sm text-destructive">
            Failed to load: {(activityQuery.error as Error).message}
          </div>
        ) : rows.length === 0 ? (
          <div className="py-8 text-center text-sm text-muted-foreground">
            {query.trim()
              ? "No activity matches this filter."
              : "No activity yet."}
          </div>
        ) : (
          rows.map((e, i) => (
            <ActivityRow
              key={`${e.at}-${e.issue_uuid}-${e.field}-${i}`}
              entry={e}
              stateById={stateById}
              issueBase={issueBase}
            />
          ))
        )}
      </div>

      <p className="text-xs text-muted-foreground">
        Shows only field changes that have audit rows — issue creation,
        per-field updates, and deletion are logged; views, comments, and
        other events are not part of this feed.
      </p>
    </div>
  );
}
