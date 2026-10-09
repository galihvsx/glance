// mywork: "My work" view client (C6T3).
//
// Contract (verified 2026-10-09):
//   GET /api/v1/workspaces/:slug/my-issues?filter=assigned|created|watched
//     &limit=1..200 -> {issues: [MyWorkItem]}
//   filter defaults to assigned; limit defaults to 100 (cap 200).
//   400 on unknown filter / bad limit; 404 for non-members.

import { api } from "./api";
import { STATE_GROUPS } from "./taxonomy";

/** One row of the "My work" view (backend MyWorkItem shape). */
export interface MyWorkItem {
  id: string;
  display_id: string;
  name: string;
  priority: number;
  state_id: string;
  state_name: string;
  state_group: string;
  project_id: string;
  project_identifier: string;
  project_name: string;
  updated_at: string;
}

export const MY_WORK_FILTERS = ["assigned", "created", "watched"] as const;
export type MyWorkFilter = (typeof MY_WORK_FILTERS)[number];

export function filterLabel(filter: MyWorkFilter): string {
  switch (filter) {
    case "assigned":
      return "Assigned to me";
    case "created":
      return "Created by me";
    case "watched":
      return "Watched";
  }
}

export function fetchMyWork(
  slug: string,
  filter: MyWorkFilter,
  limit = 100,
): Promise<MyWorkItem[]> {
  const q = new URLSearchParams({ filter, limit: String(limit) });
  return api
    .get<{ issues: MyWorkItem[] }>(
      `/api/v1/workspaces/${encodeURIComponent(slug)}/my-issues?${q}`,
    )
    .then((r) => r.issues ?? []);
}

export function myWorkKeys(slug: string) {
  return {
    all: ["mywork", slug] as const,
  };
}

/**
 * Group items by state group (kanban order), then by state name within the
 * group. Unknown groups land in a trailing "Other" bucket.
 */
export function groupMyWorkByState(
  items: MyWorkItem[],
): { group: string; states: { name: string; items: MyWorkItem[] }[] }[] {
  const byGroup = new Map<string, Map<string, MyWorkItem[]>>();
  for (const it of items) {
    const g = (STATE_GROUPS as readonly string[]).includes(it.state_group)
      ? it.state_group
      : "other";
    let states = byGroup.get(g);
    if (!states) {
      states = new Map();
      byGroup.set(g, states);
    }
    const list = states.get(it.state_name) ?? [];
    list.push(it);
    states.set(it.state_name, list);
  }
  const ordered = [...STATE_GROUPS.map(String), "other"];
  return ordered
    .filter((g) => byGroup.has(g))
    .map((g) => ({
      group: g,
      states: [...byGroup.get(g)!.entries()].map(([name, items]) => ({
        name,
        items,
      })),
    }));
}
