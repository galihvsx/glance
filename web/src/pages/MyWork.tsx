// "My work" view (C6T3): the caller's issues across a workspace's
// projects. Route: /w/:slug/my-work. Filter tabs (assigned / created /
// watched), grouped by state group (kanban order) with a project badge per
// row; click a row to open the issue detail.

import { useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
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
import { relativeTime } from "../lib/relativeTime";
import { useWorkspaces } from "../lib/useWorkspaces";
import { Badge } from "../components/ui/badge";
import { Card, CardContent } from "../components/ui/card";
import {
  NativeSelect,
  NativeSelectOption,
} from "../components/ui/native-select";
import { Skeleton } from "../components/ui/skeleton";
import { Tabs, TabsList, TabsTrigger } from "../components/ui/tabs";

function priorityLabel(p: number): string {
  switch (p) {
    case 0:
      return "No priority";
    case 1:
      return "Low";
    case 2:
      return "Medium";
    case 3:
      return "High";
    case 4:
      return "Urgent";
    default:
      return `P${p}`;
  }
}

export default function MyWork() {
  const { slug = "" } = useParams<{ slug: string }>();
  const navigate = useNavigate();
  const [filter, setFilter] = useState<MyWorkFilter>("assigned");

  const workspacesQuery = useWorkspaces();
  const itemsQuery = useQuery({
    queryKey: [...myWorkKeys(slug).all, filter],
    queryFn: () => fetchMyWork(slug, filter),
    enabled: slug !== "",
  });

  const grouped = groupMyWorkByState(itemsQuery.data ?? []);
  const workspaces = workspacesQuery.data ?? [];

  return (
    <div className="mx-auto max-w-5xl space-y-4 p-4">
      <div className="flex items-center gap-3">
          <h1 className="text-xl font-semibold">My work</h1>
          {workspaces.length > 1 && (
            <NativeSelect
              value={slug}
              onChange={(e) => navigate(`/w/${e.target.value}/my-work`)}
              aria-label="Workspace"
              className="w-44"
            >
              {workspaces.map((w) => (
                <NativeSelectOption key={w.id} value={w.slug}>
                  {w.name}
                </NativeSelectOption>
              ))}
            </NativeSelect>
          )}
      </div>

      <Tabs
        value={filter}
        onValueChange={(v) => setFilter(v as MyWorkFilter)}
      >
        <TabsList>
          {MY_WORK_FILTERS.map((f) => (
            <TabsTrigger key={f} value={f}>
              {filterLabel(f)}
            </TabsTrigger>
          ))}
        </TabsList>
      </Tabs>

      {itemsQuery.isLoading ? (
        <Skeleton className="h-64 w-full" />
      ) : itemsQuery.isError ? (
        <Card>
          <CardContent className="p-6 text-sm text-muted-foreground">
            Couldn't load your work. Try again in a moment.
          </CardContent>
        </Card>
      ) : grouped.length === 0 ? (
        <Card>
          <CardContent className="p-6 text-sm text-muted-foreground">
            {filter === "assigned" &&
              "Nothing assigned to you here. Enjoy the quiet."}
            {filter === "created" && "You haven't created any issues here yet."}
            {filter === "watched" && "You're not watching any issues here yet."}
          </CardContent>
        </Card>
      ) : (
        <div className="space-y-5">
          {grouped.map((g) => (
            <section key={g.group}>
              <h2 className="mb-2 text-xs font-semibold uppercase tracking-wide text-muted-foreground">
                {g.group === "other" ? "Other" : groupLabel(g.group)}
              </h2>
              {g.states.map((st) => (
                <div key={st.name} className="mb-3">
                  <h3 className="mb-1 text-sm font-medium">{st.name}</h3>
                  <ul className="divide-y rounded-md border">
                    {st.items.map((it) => (
                      <MyWorkRow key={it.id} item={it} slug={slug} />
                    ))}
                  </ul>
                </div>
              ))}
            </section>
          ))}
        </div>
      )}
    </div>
  );
}

function MyWorkRow({ item, slug }: { item: MyWorkItem; slug: string }) {
  const to = `/w/${slug}/p/${item.project_identifier}/i/${item.id}`;
  return (
    <li>
      <Link
        to={to}
        className="flex items-center gap-3 px-3 py-2 hover:bg-muted/50"
      >
        <span className="shrink-0 font-mono text-xs text-muted-foreground">
          {item.display_id}
        </span>
        <span className="flex-1 truncate text-sm font-medium">{item.name}</span>
        <Badge variant="outline" className="shrink-0">
          {item.project_name}
        </Badge>
        {item.priority > 0 && (
          <Badge variant="secondary" className="shrink-0">
            {priorityLabel(item.priority)}
          </Badge>
        )}
        <span className="shrink-0 text-xs text-muted-foreground">
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
