// Command palette (Task 25): Cmd/Ctrl+K. Jump to an issue by display ID
// (ENG-123), create an issue, or switch views. Built on the shadcn Command
// primitives (cmdk).

import { useEffect, useState } from "react";
import { useLocation, useNavigate } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import {
  ArrowRight,
  Inbox,
  KanbanSquare,
  List,
  Loader2,
  Plus,
  RefreshCw,
} from "lucide-react";
import { api } from "../lib/api";
import { parseDisplayId } from "../lib/displayId";
import { onShortcutAction } from "../lib/shortcuts";
import type { Issue, IssueListResult } from "../lib/types";
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

interface PaletteProps {
  slug: string;
  identifier: string;
}

function projectPath(slug: string, identifier: string): string {
  return `/w/${encodeURIComponent(slug)}/p/${encodeURIComponent(identifier)}`;
}

function apiBase(slug: string, identifier: string): string {
  return `/api/v1/workspaces/${encodeURIComponent(slug)}/projects/${encodeURIComponent(identifier)}`;
}

// resolveDisplayId resolves an issue by display ID (e.g. "ENG-123") in one
// query: the backend's ?sequence_id= lookup finds the exact row, drafts
// included (a direct sequence lookup isn't a working-set listing).
async function resolveDisplayId(
  slug: string,
  identifier: string,
  displayId: string,
): Promise<Issue | null> {
  const parsed = parseDisplayId(displayId);
  if (!parsed || parsed.identifier !== identifier.toUpperCase()) return null;
  const res = await api.get<IssueListResult>(
    `${apiBase(slug, identifier)}/issues?sequence_id=${parsed.sequence}&per_page=1`,
  );
  const issue = res.results[0];
  if (!issue) return null;
  return issue.display_id.toUpperCase() === displayId.toUpperCase()
    ? issue
    : null;
}

export default function CommandPalette({ slug, identifier }: PaletteProps) {
  const [open, setOpen] = useState(false);
  const [input, setInput] = useState("");
  const [resolving, setResolving] = useState(false);
  const [resolveError, setResolveError] = useState<string | null>(null);
  const navigate = useNavigate();
  const location = useLocation();

  useEffect(() => onShortcutAction("toggle-palette", () => setOpen((o) => !o)), []);

  // Reset transient state whenever the palette opens.
  useEffect(() => {
    if (open) {
      setInput("");
      setResolving(false);
      setResolveError(null);
    }
  }, [open ]);

  const parsed = parseDisplayId(input);
  const scopedToThisProject =
    parsed !== null && parsed.identifier === identifier.toUpperCase();

  const recentQuery = useQuery({
    queryKey: ["palette-recent", slug, identifier],
    queryFn: () =>
      api.get<IssueListResult>(
        `${apiBase(slug, identifier)}/issues?per_page=8&order_by=-updated_at`,
      ),
    enabled: open && input.trim() === "",
    staleTime: 30_000,
  });

  function close(): void {
    setOpen(false);
  }

  async function goToDisplayId(): Promise<void> {
    if (!parsed) return;
    if (!scopedToThisProject) {
      setResolveError(
        `This palette is scoped to ${identifier.toUpperCase()} — open the ${parsed.identifier} project to jump there.`,
      );
      return;
    }
    setResolving(true);
    setResolveError(null);
    try {
      const issue = await resolveDisplayId(slug, identifier, input.trim());
      if (!issue) {
        setResolveError(`No issue ${input.trim().toUpperCase()} found.`);
        return;
      }
      close();
      navigate(`${projectPath(slug, identifier)}/i/${issue.id}`);
    } catch {
      setResolveError("Couldn't look up that issue — try again.");
    } finally {
      setResolving(false);
    }
  }

  function createIssue(): void {
    close();
    navigate(projectPath(slug, identifier), { state: { newIssue: true } });
  }

  const onBoard = location.pathname.endsWith("/board");
  function toggleView(): void {
    close();
    navigate(
      onBoard ? projectPath(slug, identifier) : `${projectPath(slug, identifier)}/board`,
    );
  }
  function goIntake(): void {
    close();
    navigate(`${projectPath(slug, identifier)}/intake`);
  }
  function goCycles(): void {
    close();
    navigate(`${projectPath(slug, identifier)}/cycles`);
  }

  return (
    <CommandDialog open={open} onOpenChange={setOpen}>
      <CommandInput
        placeholder="Type ENG-123 to jump to an issue, or pick a command…"
        value={input}
        onValueChange={setInput}
      />
      <CommandList>
        {resolveError && (
          <div className="px-3 py-2 text-sm text-destructive">{resolveError}</div>
        )}
        {parsed && (
          <CommandGroup heading="Go to issue">
            <CommandItem onSelect={() => void goToDisplayId()} disabled={resolving}>
              {resolving ? (
                <Loader2 className="animate-spin" />
              ) : (
                <ArrowRight />
              )}
              Go to {input.trim().toUpperCase()}
              {!scopedToThisProject && (
                <span className="text-xs text-muted-foreground">
                  (other project)
                </span>
              )}
            </CommandItem>
          </CommandGroup>
        )}
        <CommandEmpty>
          {parsed ? "Press Enter to jump to that issue." : "No results."}
        </CommandEmpty>
        <CommandGroup heading="Commands">
          <CommandItem onSelect={createIssue}>
            <Plus />
            Create issue
            <span className="ml-auto">
              <Kbd>C</Kbd>
            </span>
          </CommandItem>
          <CommandItem onSelect={toggleView}>
            {onBoard ? <List /> : <KanbanSquare />}
            {onBoard ? "Switch to list view" : "Switch to board view"}
          </CommandItem>
          <CommandItem onSelect={goIntake}>
            <Inbox />
            Go to intake inbox
          </CommandItem>
          <CommandItem onSelect={goCycles}>
            <RefreshCw />
            Go to cycles
          </CommandItem>
        </CommandGroup>
        {input.trim() === "" && (recentQuery.data?.results.length ?? 0) > 0 && (
          <>
            <CommandSeparator />
            <CommandGroup heading="Recent issues">
              {recentQuery.data!.results.map((issue) => (
                <CommandItem
                  key={issue.id}
                  value={`${issue.display_id} ${issue.name}`}
                  onSelect={() => {
                    close();
                    navigate(`${projectPath(slug, identifier)}/i/${issue.id}`);
                  }}
                >
                  <span className="font-mono text-xs text-muted-foreground">
                    {issue.display_id}
                  </span>
                  <span className="truncate">{issue.name}</span>
                </CommandItem>
              ))}
            </CommandGroup>
          </>
        )}
      </CommandList>
    </CommandDialog>
  );
}
