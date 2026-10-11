// Global command palette (C18T0): Cmd/Ctrl+K, mounted once at the app
// shell level. Workspace-global like Plane's: debounced full-text search
// across every project the caller can see (GET /api/v1/search), results
// grouped by project; a display-ID fast path (ENG-123 jumps straight to
// the issue, resolved against the current workspace); workspace-scoped
// recent issues from localStorage when idle; commands (create issue with
// an in-palette project picker, go to Home / My Work / intake / analytics,
// toggle sidebar, open the shortcuts cheatsheet). Built on the shadcn
// Command primitives (cmdk). UX shape per the agy/gemini-3.8-flash round.

import { useEffect, useMemo, useState } from "react";
import { useMatch, useNavigate } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import {
  ArrowLeft,
  ArrowRight,
  BarChart3,
  Briefcase,
  FolderKanban,
  Home,
  Inbox,
  Keyboard,
  Loader2,
  PanelLeft,
  Plus,
} from "lucide-react";
import { api, ApiError } from "../lib/api";
import { parseDisplayId } from "../lib/displayId";
// Recents: the shared issue/recents module already records every opened
// issue (full page + peek drawer); the palette reads the workspace-scoped
// slice of that same list.
import {
  readRecentIssues,
  recordRecentIssue,
  type RecentIssue,
} from "./issue/recents";
import { emitShortcutAction, onShortcutAction } from "../lib/shortcuts";
import { useDebouncedValue } from "../lib/useDebouncedValue";
import { useShell } from "./shell/shell-context";
import type {
  IssueListResult,
  Project,
  SearchIssueItem,
  SearchIssuesResult,
} from "../lib/types";
import {
  CommandDialog,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
  CommandSeparator,
} from "./ui/command";
import { Kbd } from "./ui/kbd";

type PalettePage = "root" | "create";

const SEARCH_DEBOUNCE_MS = 250;
const SEARCH_PER_PAGE = 25;
const GROUP_ISSUE_CAP = 5;
const RECENT_CAP = 5;

function wsProjectPath(slug: string, identifier: string): string {
  return `/w/${encodeURIComponent(slug)}/p/${encodeURIComponent(identifier)}`;
}

function issuePath(slug: string, identifier: string, id: string): string {
  return `${wsProjectPath(slug, identifier)}/i/${encodeURIComponent(id)}`;
}

/** Project icon when served (C18T4), initials fallback. Defensive: the
 *  icon column may not exist yet (T4 merges before T0, but the code must
 *  not assume it). */
function ProjectGlyph({ project }: { project: Project }) {
  if (project.icon) {
    return <span aria-hidden="true">{project.icon}</span>;
  }
  return (
    <span
      aria-hidden="true"
      className="flex size-5 items-center justify-center rounded bg-muted text-[10px] font-semibold text-muted-foreground"
    >
      {project.identifier.slice(0, 2).toUpperCase()}
    </span>
  );
}

/** Defensive archived check: the projects payload gains `archived_at`
 *  with C18T3 (which also excludes archived projects server-side from
 *  search). Until then the field is absent and nothing is skipped. */
function isProjectArchived(p: Project): boolean {
  return Boolean((p as { archived_at?: string | null }).archived_at);
}

interface SearchGroup {
  key: string;
  workspaceSlug: string;
  identifier: string;
  name: string;
  icon: string | null;
  foreignWorkspace: boolean;
  items: SearchIssueItem[];
}

export default function CommandPalette() {
  const [open, setOpen] = useState(false);
  const [input, setInput] = useState("");
  const [page, setPage] = useState<PalettePage>("root");
  const [resolving, setResolving] = useState(false);
  const [resolveError, setResolveError] = useState<string | null>(null);
  const [recents, setRecents] = useState<RecentIssue[]>([]);
  const navigate = useNavigate();

  const wsMatch = useMatch("/w/:slug/*");
  const projectMatch = useMatch("/w/:slug/p/:identifier/*");
  const { lastWorkspaceSlug } = useShell();
  const slug = wsMatch?.params.slug ?? lastWorkspaceSlug ?? null;
  const currentIdentifier = projectMatch?.params.identifier ?? null;

  useEffect(() => onShortcutAction("toggle-palette", () => setOpen((o) => !o)), []);

  // Reset transient state whenever the palette opens. Recents are the
  // workspace-scoped slice of the shared last-opened list.
  useEffect(() => {
    if (open) {
      setInput("");
      setPage("root");
      setResolving(false);
      setResolveError(null);
      setRecents(
        readRecentIssues()
          .filter((r) => r.slug === slug)
          .slice(0, RECENT_CAP),
      );
    }
  }, [open, slug ]);

  // Shared cache key with the sidebar's project list — no extra fetch.
  const projectsQuery = useQuery({
    queryKey: ["projects", slug],
    queryFn: () =>
      api
        .get<{ projects: Project[] }>(
          `/api/v1/workspaces/${encodeURIComponent(slug ?? "")}/projects`,
        )
        .then((d) => d.projects),
    enabled: !!slug,
  });
  const projects = useMemo(
    () => projectsQuery.data ?? [],
    [projectsQuery.data],
  );
  const visibleProjects = useMemo(
    () => projects.filter((p) => !isProjectArchived(p)),
    [projects],
  );
  const projectByIdentifier = useMemo(() => {
    const m = new Map<string, Project>();
    for (const p of visibleProjects) m.set(p.identifier.toUpperCase(), p);
    return m;
  }, [visibleProjects]);
  const archivedKeys = useMemo(() => {
    const s = new Set<string>();
    for (const p of projects) {
      if (isProjectArchived(p))
        s.add(`${slug}/${p.identifier.toUpperCase()}`);
    }
    return s;
  }, [projects, slug]);

  const trimmed = input.trim();
  const parsed = parseDisplayId(trimmed);
  const searching = page === "root" && trimmed.length > 0;
  const debounced = useDebouncedValue(searching ? trimmed : "", SEARCH_DEBOUNCE_MS);

  const searchQuery = useQuery({
    queryKey: ["palette-search", debounced],
    queryFn: () =>
      api.get<SearchIssuesResult>(
        `/api/v1/search?q=${encodeURIComponent(debounced)}&per_page=${SEARCH_PER_PAGE}`,
      ),
    enabled: open && debounced.length > 0,
    staleTime: 30_000,
  });

  const fastPathKey = parsed
    ? `${parsed.identifier}-${parsed.sequence}`
    : null;

  const groups: SearchGroup[] = useMemo(() => {
    const results = searchQuery.data?.results ?? [];
    const byKey = new Map<string, SearchIssueItem[]>();
    for (const hit of results) {
      // Defensive: skip hits in archived projects (the backend excludes
      // them server-side from C18T3 on; the payload marks them then).
      if (archivedKeys.has(`${hit.workspace_slug}/${hit.project_identifier.toUpperCase()}`))
        continue;
      // Dedupe the fast-path target — it already has its own pinned row.
      if (
        fastPathKey &&
        hit.display_id.toUpperCase() === fastPathKey.toUpperCase()
      )
        continue;
      const key = `${hit.workspace_slug}/${hit.project_identifier.toUpperCase()}`;
      const arr = byKey.get(key) ?? [];
      arr.push(hit);
      byKey.set(key, arr);
    }
    const list: SearchGroup[] = [];
    for (const [key, items] of byKey) {
      const [ws, ident] = key.split("/", 2);
      const meta = ws === slug ? projectByIdentifier.get(ident) : undefined;
      list.push({
        key,
        workspaceSlug: ws,
        identifier: ident,
        name: meta?.name ?? ident,
        icon: meta?.icon ?? null,
        foreignWorkspace: ws !== slug,
        items,
      });
    }
    // Current project first, then result density, then identifier.
    const current = currentIdentifier?.toUpperCase();
    list.sort((a, b) => {
      const aCur = a.identifier === current && !a.foreignWorkspace;
      const bCur = b.identifier === current && !b.foreignWorkspace;
      if (aCur !== bCur) return aCur ? -1 : 1;
      if (a.items.length !== b.items.length)
        return b.items.length - a.items.length;
      return a.identifier.localeCompare(b.identifier);
    });
    return list;
  }, [
    searchQuery.data,
    archivedKeys,
    fastPathKey,
    slug,
    projectByIdentifier,
    currentIdentifier,
  ]);

  const matchingProjects = useMemo(() => {
    if (!searching) return [];
    const q = trimmed.toLowerCase();
    return visibleProjects.filter(
      (p) =>
        p.name.toLowerCase().includes(q) ||
        p.identifier.toLowerCase().includes(q),
    );
  }, [searching, trimmed, visibleProjects]);

  function close(): void {
    setOpen(false);
  }

  function go(path: string, state?: Record<string, unknown>): void {
    close();
    navigate(path, state ? { state } : undefined);
  }

  function openIssue(
    wsSlug: string,
    identifier: string,
    issue: { id: string; display_id: string; name: string },
  ): void {
    // The detail view records the visit too; recording here as well keeps
    // the recents list fresh even if the detail load fails.
    recordRecentIssue({
      id: issue.id,
      display_id: issue.display_id,
      name: issue.name,
      slug: wsSlug,
      identifier,
    });
    go(issuePath(wsSlug, identifier, issue.id));
  }

  // Display-ID fast path: resolve against the project parsed from the
  // input, inside the current workspace (slug from the route, else the
  // last-visited workspace). No waiting on the debounced search.
  async function goToDisplayId(): Promise<void> {
    if (!parsed) return;
    if (!slug) {
      setResolveError("Open a workspace first, then jump by issue ID.");
      return;
    }
    setResolving(true);
    setResolveError(null);
    const target = `${parsed.identifier}-${parsed.sequence}`;
    try {
      const res = await api.get<IssueListResult>(
        `/api/v1/workspaces/${encodeURIComponent(slug)}/projects/${encodeURIComponent(parsed.identifier)}/issues?sequence_id=${parsed.sequence}&per_page=1`,
      );
      const issue = res.results[0];
      if (!issue || issue.display_id.toUpperCase() !== target.toUpperCase()) {
        setResolveError(`No issue ${target} in this workspace.`);
        return;
      }
      openIssue(slug, parsed.identifier, issue);
    } catch (e) {
      if (e instanceof ApiError && e.status === 404) {
        // Unknown project identifier (or non-member): same answer as a
        // missing issue — no enumeration.
        setResolveError(`No issue ${target} in this workspace.`);
      } else {
        setResolveError("Couldn't look up that issue — try again.");
      }
    } finally {
      setResolving(false);
    }
  }

  function createIssueInProject(p: Project): void {
    if (!slug) return;
    go(wsProjectPath(slug, p.identifier), { newIssue: true });
  }

  const myWorkPath = slug
    ? `/w/${encodeURIComponent(slug)}/my-work`
    : null;
  const projectBase = slug && currentIdentifier
    ? wsProjectPath(slug, currentIdentifier)
    : null;

  const showLoading =
    searching && debounced.length > 0 && searchQuery.isFetching;
  const hasResults =
    groups.length > 0 || matchingProjects.length > 0 || parsed !== null;

  return (
    <CommandDialog open={open} onOpenChange={setOpen}>
      <CommandInput
        placeholder={
          page === "create"
            ? "Select a project for the new issue…"
            : "Search issues across projects, or type ENG-123 to jump…"
        }
        value={input}
        onValueChange={setInput}
        onKeyDown={(e) => {
          // Linear-style: Backspace on an empty create-page input pops
          // back to the root page instead of closing the palette.
          if (page === "create" && input === "" && e.key === "Backspace") {
            e.preventDefault();
            setPage("root");
          }
        }}
      />
      <CommandList>
        {resolveError && (
          <div className="px-3 py-2 text-sm text-destructive">{resolveError}</div>
        )}

        {page === "create" ? (
          <CommandGroup heading="Select project">
            <CommandItem
              value="back to commands"
              onSelect={() => {
                setPage("root");
                setInput("");
              }}
            >
              <ArrowLeft />
              Back to commands
            </CommandItem>
            {[...visibleProjects]
              .sort((a, b) => {
                // Current project pinned first, then alphabetical.
                const cur = currentIdentifier?.toUpperCase();
                const aCur = a.identifier.toUpperCase() === cur;
                const bCur = b.identifier.toUpperCase() === cur;
                if (aCur !== bCur) return aCur ? -1 : 1;
                return a.name.localeCompare(b.name);
              })
              .map((p) => (
                <CommandItem
                  key={p.id}
                  value={`${p.name} ${p.identifier}`}
                  onSelect={() => createIssueInProject(p)}
                >
                  <ProjectGlyph project={p} />
                  <span className="truncate">{p.name}</span>
                  <span className="ml-auto font-mono text-xs text-muted-foreground">
                    {p.identifier.toUpperCase()}
                  </span>
                </CommandItem>
              ))}
          </CommandGroup>
        ) : (
          <>
            {parsed && (
              <CommandGroup heading="Jump to issue">
                <CommandItem
                  value={fastPathKey ?? ""}
                  onSelect={() => void goToDisplayId()}
                  disabled={resolving}
                >
                  {resolving ? (
                    <Loader2 className="animate-spin" />
                  ) : (
                    <ArrowRight />
                  )}
                  <span className="rounded bg-primary/10 px-1.5 py-0.5 font-mono text-xs font-medium text-primary">
                    {fastPathKey}
                  </span>
                  <span className="truncate text-muted-foreground">
                    Jump directly to this issue
                  </span>
                  <span className="ml-auto">
                    <Kbd>↵</Kbd>
                  </span>
                </CommandItem>
              </CommandGroup>
            )}

            {searching &&
              groups.map((g) => (
                <CommandGroup
                  key={g.key}
                  heading={
                    g.foreignWorkspace
                      ? `${g.name} · ${g.workspaceSlug}`
                      : g.name
                  }
                >
                  {g.items.slice(0, GROUP_ISSUE_CAP).map((hit) => (
                    <CommandItem
                      key={hit.id}
                      value={`${hit.display_id} ${hit.name}`}
                      onSelect={() =>
                        openIssue(g.workspaceSlug, g.identifier, hit)
                      }
                    >
                      {g.icon ? (
                        <span aria-hidden="true">{g.icon}</span>
                      ) : (
                        <span className="font-mono text-xs text-muted-foreground">
                          {hit.display_id}
                        </span>
                      )}
                      <span className="truncate">{hit.name}</span>
                      {g.icon && (
                        <span className="ml-auto shrink-0 font-mono text-xs text-muted-foreground">
                          {hit.display_id}
                        </span>
                      )}
                    </CommandItem>
                  ))}
                </CommandGroup>
              ))}

            {searching && matchingProjects.length > 0 && (
              <CommandGroup heading="Projects">
                {matchingProjects.map((p) => (
                  <CommandItem
                    key={p.id}
                    value={`project ${p.name} ${p.identifier}`}
                    onSelect={() => slug && go(wsProjectPath(slug, p.identifier))}
                  >
                    <FolderKanban className="text-muted-foreground" />
                    <span className="truncate">{p.name}</span>
                    <span className="ml-auto font-mono text-xs text-muted-foreground">
                      {p.identifier.toUpperCase()}
                    </span>
                  </CommandItem>
                ))}
              </CommandGroup>
            )}

            {!searching && recents.length > 0 && (
              <>
                <CommandGroup heading="Recent issues">
                  {recents.map((r) => (
                    <CommandItem
                      key={r.id}
                      value={`${r.display_id} ${r.name}`}
                      onSelect={() => openIssue(r.slug, r.identifier, r)}
                    >
                      <span className="font-mono text-xs text-muted-foreground">
                        {r.display_id}
                      </span>
                      <span className="truncate">{r.name}</span>
                      <span className="ml-auto shrink-0 rounded bg-muted px-1.5 py-0.5 font-mono text-[10px] text-muted-foreground">
                        {r.identifier.toUpperCase()}
                      </span>
                    </CommandItem>
                  ))}
                </CommandGroup>
                <CommandSeparator />
              </>
            )}

            <CommandGroup heading="Commands">
              <CommandItem
                value="create issue new"
                onSelect={() => {
                  setPage("create");
                  setInput("");
                  setResolveError(null);
                }}
              >
                <Plus />
                Create issue
                <span className="ml-auto">
                  <Kbd>C</Kbd>
                </span>
              </CommandItem>
              <CommandItem value="go home" onSelect={() => go("/")}>
                <Home />
                Go to Home
              </CommandItem>
              {myWorkPath && (
                <CommandItem
                  value="go my work assigned to me"
                  onSelect={() => go(myWorkPath)}
                >
                  <Briefcase />
                  Go to My Work
                </CommandItem>
              )}
              {projectBase && (
                <CommandItem
                  value="go intake inbox triage"
                  onSelect={() => go(`${projectBase}/intake`)}
                >
                  <Inbox />
                  Go to intake inbox
                </CommandItem>
              )}
              {projectBase && (
                <CommandItem
                  value="go analytics reports"
                  onSelect={() => go(`${projectBase}/analytics`)}
                >
                  <BarChart3 />
                  Go to analytics
                </CommandItem>
              )}
              <CommandItem
                value="toggle sidebar collapse"
                onSelect={() => {
                  close();
                  emitShortcutAction("toggle-sidebar");
                }}
              >
                <PanelLeft />
                Toggle sidebar
                <span className="ml-auto flex gap-1">
                  <Kbd>⌘</Kbd>
                  <Kbd>B</Kbd>
                </span>
              </CommandItem>
              <CommandItem
                value="keyboard shortcuts cheatsheet help"
                onSelect={() => {
                  close();
                  emitShortcutAction("open-cheatsheet");
                }}
              >
                <Keyboard />
                Open keyboard shortcuts
                <span className="ml-auto">
                  <Kbd>?</Kbd>
                </span>
              </CommandItem>
            </CommandGroup>

            <CommandEmpty>
              {showLoading
                ? "Searching…"
                : searching && !hasResults
                  ? `No results for “${debounced}”.`
                  : "No results."}
            </CommandEmpty>
          </>
        )}
      </CommandList>
    </CommandDialog>
  );
}
