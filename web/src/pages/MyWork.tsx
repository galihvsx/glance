// "My work" view (C6T3, C19T9 polish): the caller's issues across a
// workspace's projects. Route: /w/:slug/my-work. Filter tabs (assigned /
// created / watched), an overview stat band (prototype SHELL CONTENT §3
// language), and dense 47px rows grouped by state group — the C19T5 list
// row language: display-id mono 10px muted, title 12px ink, state dot +
// 10px label, priority bars + 10px label. Group bars use the prototype
// "Recently updated" row language (35px, bottom hairline, 12px/600).

import { useState } from "react";
import { Link, useParams } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import { Inbox } from "lucide-react";
import { groupLabel } from "../lib/taxonomy";
import {
  MY_WORK_FILTERS,
  fetchMyWork,
  filterLabel,
  groupMyWorkByState,
  myWorkKeys,
  type MyWorkFilter,
  type MyWorkItem,
} from "../lib/mywork";
import { priorityLabel } from "../lib/types";
import { relativeTime } from "../lib/relativeTime";
import { stateDotClass } from "../lib/stateDot";
import PageHeader from "../components/shell/PageHeader";
import PriorityBars from "../components/issue/PriorityBars";
import EmptyState from "../components/personal/EmptyState";
import { SectionBar, StatBand } from "../components/personal/SectionBar";
import { Alert, AlertDescription } from "../components/ui/alert";
import { Skeleton } from "../components/ui/skeleton";
import { Tabs, TabsList, TabsTrigger } from "../components/ui/tabs";

const STARTED_GROUPS = new Set(["started"]);
const TODO_GROUPS = new Set(["unstarted", "backlog", "triage"]);

export default function MyWork() {
  const { slug = "" } = useParams<{ slug: string }>();
  const [filter, setFilter] = useState<MyWorkFilter>("assigned");

  const itemsQuery = useQuery({
    queryKey: [...myWorkKeys(slug).all, filter],
    queryFn: () => fetchMyWork(slug, filter),
    enabled: slug !== "",
  });

  const items = itemsQuery.data ?? [];
  const grouped = groupMyWorkByState(items);

  const projectCount = new Set(items.map((it) => it.project_id)).size;
  const startedCount = items.filter((it) =>
    STARTED_GROUPS.has(it.state_group),
  ).length;
  const todoCount = items.filter((it) =>
    TODO_GROUPS.has(it.state_group),
  ).length;

  return (
    <div className="w-full">
      {/* C19T4-style shared header (no project scope: no add/analytics). */}
      <PageHeader
        viewName="My work"
        count={items.length}
        addButton={null}
        showAnalytics={false}
      />
      <div className="mx-auto w-full max-w-5xl space-y-4 p-6">
        <Tabs value={filter} onValueChange={(v) => setFilter(v as MyWorkFilter)}>
          <TabsList>
            {MY_WORK_FILTERS.map((f) => (
              <TabsTrigger key={f} value={f}>
                {filterLabel(f)}
              </TabsTrigger>
            ))}
          </TabsList>
        </Tabs>

        {itemsQuery.isLoading ? (
          <div className="space-y-2">
            <Skeleton className="h-[47px] w-full" />
            <Skeleton className="h-[47px] w-full" />
            <Skeleton className="h-[47px] w-full" />
          </div>
        ) : itemsQuery.isError ? (
          <Alert variant="destructive">
            <AlertDescription>
              Couldn&apos;t load your work. Try again in a moment.
            </AlertDescription>
          </Alert>
        ) : grouped.length === 0 ? (
          <EmptyState
            icon={Inbox}
            title={
              filter === "assigned"
                ? "Nothing assigned to you"
                : filter === "created"
                  ? "You haven't created any issues here yet"
                  : "You're not watching any issues here yet"
            }
          >
            {filter === "assigned" && "Enjoy the quiet."}
          </EmptyState>
        ) : (
          <>
            <StatBand
              stats={[
                {
                  label: filterLabel(filter),
                  value: items.length,
                  note: `${projectCount} project${projectCount === 1 ? "" : "s"}`,
                },
                {
                  label: "In progress",
                  value: startedCount,
                  note: "started",
                },
                {
                  label: "To do",
                  value: todoCount,
                  note: "not started",
                },
              ]}
            />
            <div>
              {grouped.map((g) => {
                const groupTotal = g.states.reduce(
                  (n, st) => n + st.items.length,
                  0,
                );
                return (
                  <section key={g.group} className="mb-6 last:mb-0">
                    <SectionBar
                      label={g.group === "other" ? "Other" : groupLabel(g.group)}
                      count={groupTotal}
                    />
                    {g.states.map((st) => (
                      <div key={st.name} className="mt-4 first:mt-2">
                        <h3 className="mb-1 px-3 text-[11px] font-semibold text-ink-secondary">
                          {st.name}{" "}
                          <span className="font-normal text-ink-muted">
                            {st.items.length}
                          </span>
                        </h3>
                        <ul className="divide-y divide-border-hairline overflow-hidden rounded-md border border-border">
                          {st.items.map((it) => (
                            <MyWorkRow key={it.id} item={it} slug={slug} />
                          ))}
                        </ul>
                      </div>
                    ))}
                  </section>
                );
              })}
            </div>
          </>
        )}
      </div>
    </div>
  );
}

function MyWorkRow({ item, slug }: { item: MyWorkItem; slug: string }) {
  const to = myWorkRowPath(slug, item.project_identifier, item.id);
  return (
    <li>
      <Link
        to={to}
        className="grid min-h-[47px] grid-cols-[76px_minmax(0,1fr)_110px_90px_70px] items-center gap-[10px] px-3 py-2 transition-colors hover:bg-wash-hover"
      >
        <span className="truncate font-mono text-[10px] text-ink-muted">
          {item.display_id}
        </span>
        <span className="flex min-w-0 items-center gap-2">
          <span className="truncate text-[12px] text-ink">{item.name}</span>
          <span className="shrink-0 rounded border border-border px-1.5 py-0.5 text-[10px] leading-none text-ink-secondary">
            {item.project_name}
          </span>
        </span>
        <span className="flex min-w-0 items-center gap-[6px]">
          <span
            className={`state-dot ${stateDotClass(item.state_group)}`}
            aria-hidden="true"
          />
          <span className="truncate text-[10px] text-ink-secondary">
            {item.state_name}
          </span>
        </span>
        <span className="flex min-w-0 items-center gap-[5px]">
          {item.priority > 0 ? (
            <>
              <PriorityBars priority={item.priority} />
              <span className="truncate text-[10px] text-ink-secondary">
                {priorityLabel(item.priority)}
              </span>
            </>
          ) : null}
        </span>
        <span className="truncate text-[10px] text-ink-muted">
          {relativeTime(item.updated_at)}
        </span>
      </Link>
    </li>
  );
}

/** "View all" link target for dashboard sections. */
export function myWorkPath(slug: string): string {
  return `/w/${slug}/my-work`;
}

// Re-exported for tests: the row link shape is pinned here.
export function myWorkRowPath(
  slug: string,
  projectIdentifier: string,
  issueId: string,
): string {
  return `/w/${slug}/p/${projectIdentifier}/i/${issueId}`;
}
