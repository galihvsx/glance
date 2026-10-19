import { useState, type ReactNode } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Check, ChevronDown, Clock, Hourglass, Plus, X } from "lucide-react";
import { api, ApiError } from "../../lib/api";
import { formatDateTime } from "../../lib/format";
import { formatStateDuration } from "../../lib/timeState";
import type {
  Cycle,
  Issue,
  IssueProjectStructure,
  Label as ProjectLabel,
  Module,
  Release,
} from "../../lib/types";
import { formatDuration } from "./TimeTracker";
import { Popover, PopoverContent, PopoverTrigger } from "../ui/popover";
import { cn } from "cn";
import ParentPicker from "./ParentPicker";
import LabelPicker from "./LabelPicker";

function Cell({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="min-w-0">
      <span className="mb-[5px] block text-[9px] text-ink-muted">{label}</span>
      <div className="flex min-h-[23px] flex-wrap items-center gap-[6px] text-[10px] text-ink-secondary">
        {children}
      </div>
    </div>
  );
}

function TagChip({ children, onRemove, title }: { children: ReactNode; onRemove?: () => void; title?: string }) {
  return (
    <span
      title={title}
      className="inline-flex h-[20px] items-center gap-1 rounded-[4px] border border-border px-[6px] text-[10px] text-ink-secondary"
    >
      {children}
      {onRemove && (
        <button
          type="button"
          onClick={(e) => {
            e.stopPropagation();
            onRemove();
          }}
          aria-label="Remove"
          className="flex h-4 w-4 items-center justify-center rounded text-ink-muted hover:bg-wash-hover hover:text-ink"
        >
          <X className="h-3 w-3" />
        </button>
      )}
    </span>
  );
}

function Group({
  title,
  children,
  defaultOpen = true,
}: {
  title: string;
  children: ReactNode;
  defaultOpen?: boolean;
}) {
  const [open, setOpen] = useState(defaultOpen);
  return (
    <div className="border-t border-border">
      <button
        type="button"
        onClick={() => setOpen((o) => !o)}
        aria-expanded={open}
        className="flex h-[36px] w-full items-center gap-[7px] text-[11px] text-ink-secondary transition-colors hover:text-ink"
      >
        <ChevronDown
          className={cn("h-3 w-3 text-ink-muted transition-transform", !open && "-rotate-90")}
        />
        {title}
      </button>
      {open && (
        <div className="grid grid-cols-3 gap-x-[18px] gap-y-[7px] px-0 pt-[2px] pb-[13px] max-sm:grid-cols-2">
          {children}
        </div>
      )}
    </div>
  );
}

function OptionRow({
  selected,
  onClick,
  children,
}: {
  selected?: boolean;
  onClick: () => void;
  children: ReactNode;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className="flex w-full items-center gap-2 rounded-[4px] px-2 py-1.5 text-left text-xs text-ink-secondary hover:bg-wash-hover hover:text-ink"
    >
      <span className="flex-1 truncate">{children}</span>
      {selected && <Check className="h-3.5 w-3.5 shrink-0 text-ink-muted" />}
    </button>
  );
}

/**
 * The Properties section (C19T3): 12px/620 heading, collapsible "Details"
 * group (Parent / Labels / Tracked time) and "Project structure" group
 * (Cycle / Modules / Milestone / Releases) in a 3-col grid. Milestone
 * maps to Releases — glance's backend calls releases "project-scoped
 * milestones", so both cells read the issue's release_id.
 */
export default function IssueProperties({
  slug,
  identifier,
  issue,
  parentIssue,
  labels,
  structure,
  issueKey,
  onPatch,
  onToggleLabel,
  trackedSeconds,
  onOpenWorklogs,
  onError,
  disabled,
}: {
  slug: string;
  identifier: string;
  issue: Issue;
  parentIssue: Issue | null;
  labels: ProjectLabel[];
  structure?: IssueProjectStructure;
  /** The detail react-query key (invalidated after membership changes). */
  issueKey: unknown[];
  onPatch: (body: Record<string, unknown>) => void;
  onToggleLabel: (id: string, currently: boolean) => void;
  trackedSeconds: number;
  onOpenWorklogs: () => void;
  onError: (msg: string | null) => void;
  disabled?: boolean;
}) {
  const queryClient = useQueryClient();
  const base = `/api/v1/workspaces/${encodeURIComponent(slug)}/projects/${encodeURIComponent(identifier)}`;
  const [cycleOpen, setCycleOpen] = useState(false);
  const [moduleOpen, setModuleOpen] = useState(false);
  const [releaseOpen, setReleaseOpen] = useState(false);

  const cyclesQuery = useQuery({
    queryKey: ["cycles", slug, identifier],
    queryFn: () => api.get<{ cycles: Cycle[] }>(`${base}/cycles`).then((d) => d.cycles),
  });
  const modulesQuery = useQuery({
    queryKey: ["modules", slug, identifier],
    queryFn: () => api.get<{ modules: Module[] }>(`${base}/modules`).then((d) => d.modules),
  });
  const releasesQuery = useQuery({
    queryKey: ["releases", slug, identifier],
    queryFn: () => api.get<{ releases: Release[] }>(`${base}/releases`).then((d) => d.releases),
  });

  function invalidate() {
    void queryClient.invalidateQueries({ queryKey: issueKey });
  }
  function fail(e: unknown, what: string) {
    onError(e instanceof ApiError ? e.message : what);
  }

  const cycleMutation = useMutation({
    mutationFn: (cycleId: string) =>
      api.post(`${base}/cycles/${encodeURIComponent(cycleId)}/issues`, {
        issue_ids: [issue.id],
      }),
    onSuccess: () => {
      onError(null);
      invalidate();
    },
    onError: (e) => fail(e, "Failed to set cycle"),
  });
  const moduleAddMutation = useMutation({
    mutationFn: (moduleId: string) =>
      api.post(`${base}/modules/${encodeURIComponent(moduleId)}/issues`, {
        issue_ids: [issue.id],
      }),
    onSuccess: () => {
      onError(null);
      invalidate();
    },
    onError: (e) => fail(e, "Failed to add module"),
  });
  const moduleRemoveMutation = useMutation({
    mutationFn: (moduleId: string) =>
      api.del(`${base}/modules/${encodeURIComponent(moduleId)}/issues`, {
        issue_ids: [issue.id],
      }),
    onSuccess: () => {
      onError(null);
      invalidate();
    },
    onError: (e) => fail(e, "Failed to remove module"),
  });

  const cycles = cyclesQuery.data ?? [];
  const modules = modulesQuery.data ?? [];
  const releases = releasesQuery.data ?? [];
  const currentCycle = structure?.cycle ?? null;
  const currentModules = structure?.modules ?? [];
  const currentRelease = structure?.release ?? null;
  const moduleIds = new Set(currentModules.map((m) => m.id));
  const addableModules = modules.filter((m) => !moduleIds.has(m.id));

  const cellTrigger =
    "h-[23px] min-h-[23px] gap-[6px] whitespace-nowrap rounded-[4px] border-0 bg-transparent px-1 text-[10px] font-normal text-ink-secondary shadow-none hover:bg-wash-hover hover:text-ink";

  return (
    <section aria-label="Properties" className="mt-[25px]">
      <h2 className="mb-[9px] text-[12px] font-semibold leading-[1.5] text-ink">Properties</h2>

      <Group title="Details">
        <Cell label="Parent">
          <ParentPicker
            slug={slug}
            identifier={identifier}
            value={parentIssue}
            onChange={(p) => {
              onPatch({ parent_id: p ? p.id : null });
            }}
            disabled={disabled}
            triggerClassName={cn(cellTrigger, "max-w-full")}
          />
        </Cell>
        <Cell label="Labels">
          {issue.labels.map((l) => (
            <TagChip
              key={l.id}
              title={l.name}
              onRemove={disabled ? undefined : () => onToggleLabel(l.id, true)}
            >
              <span
                className="h-[6px] w-[6px] rounded-full"
                style={{ backgroundColor: l.color }}
                aria-hidden
              />
              {l.name}
            </TagChip>
          ))}
          <LabelPicker
            labels={labels}
            applied={issue.labels}
            onToggle={onToggleLabel}
            disabled={disabled}
            triggerClassName={cn(cellTrigger, "text-ink-muted")}
            triggerContent={
              <>
                <Plus className="h-3 w-3" />
                <span>{issue.labels.length === 0 ? "Add labels" : "Edit"}</span>
              </>
            }
          />
        </Cell>
        <Cell label="Tracked time">
          <button
            type="button"
            onClick={onOpenWorklogs}
            title="Open worklogs"
            className="flex min-h-[23px] items-center gap-[6px] rounded-[4px] px-1 text-[10px] text-ink-secondary transition-colors hover:bg-wash-hover hover:text-ink"
          >
            <Clock className="h-3 w-3 text-ink-muted" />
            {formatDuration(trackedSeconds)}
          </button>
        </Cell>
        {issue.time_in_state_seconds != null && (
          <Cell label="Time in state">
            {/* C70T1: non-interactive. The current state name is not in
             *  scope here (no prop-drilling), so the tooltip falls back to
             *  "Since {date}". When state_entered_at is absent the duration
             *  renders with no tooltip. */}
            <span
              title={
                issue.state_entered_at
                  ? `Since ${formatDateTime(issue.state_entered_at)}`
                  : undefined
              }
              className="inline-flex min-h-[23px] items-center gap-[6px] px-1 text-[10px] text-ink-secondary"
            >
              <Hourglass className="h-3 w-3 text-ink-muted" />
              {formatStateDuration(issue.time_in_state_seconds)}
            </span>
          </Cell>
        )}
      </Group>

      <Group title="Project structure">
        <Cell label="Cycle">
          <Popover open={cycleOpen} onOpenChange={setCycleOpen}>
            <PopoverTrigger disabled={disabled} className={cellTrigger}>
              {currentCycle ? (
                <span className="truncate">{currentCycle.name}</span>
              ) : (
                <span className="text-ink-muted">No cycle</span>
              )}
            </PopoverTrigger>
            <PopoverContent className="w-52 p-1" align="start">
              {cycles.length === 0 ? (
                <p className="px-2 py-1.5 text-xs text-ink-muted">No cycles in this project.</p>
              ) : (
                cycles.map((c) => (
                  <OptionRow
                    key={c.id}
                    selected={currentCycle?.id === c.id}
                    onClick={() => {
                      if (currentCycle?.id !== c.id) cycleMutation.mutate(c.id);
                      setCycleOpen(false);
                    }}
                  >
                    {c.name}
                  </OptionRow>
                ))
              )}
            </PopoverContent>
          </Popover>
        </Cell>
        <Cell label="Modules">
          {currentModules.map((m) => (
            <TagChip
              key={m.id}
              title={m.name}
              onRemove={disabled ? undefined : () => moduleRemoveMutation.mutate(m.id)}
            >
              {m.name}
            </TagChip>
          ))}
          <Popover open={moduleOpen} onOpenChange={setModuleOpen}>
            <PopoverTrigger disabled={disabled} className={cn(cellTrigger, "text-ink-muted")}>
              <Plus className="h-3 w-3" />
              <span>{currentModules.length === 0 ? "Add modules" : "Edit"}</span>
            </PopoverTrigger>
            <PopoverContent className="w-52 p-1" align="start">
              {addableModules.length === 0 ? (
                <p className="px-2 py-1.5 text-xs text-ink-muted">
                  {modules.length === 0 ? "No modules in this project." : "All modules added."}
                </p>
              ) : (
                addableModules.map((m) => (
                  <OptionRow
                    key={m.id}
                    onClick={() => {
                      moduleAddMutation.mutate(m.id);
                      setModuleOpen(false);
                    }}
                  >
                    {m.name}
                  </OptionRow>
                ))
              )}
            </PopoverContent>
          </Popover>
        </Cell>
        <Cell label="Milestone">
          <Popover open={releaseOpen} onOpenChange={setReleaseOpen}>
            <PopoverTrigger disabled={disabled} className={cellTrigger}>
              {currentRelease ? (
                <span className="truncate">{currentRelease.name}</span>
              ) : (
                <span className="text-ink-muted">No milestone</span>
              )}
            </PopoverTrigger>
            <PopoverContent className="w-52 p-1" align="start">
              <OptionRow
                selected={!currentRelease}
                onClick={() => {
                  if (currentRelease) onPatch({ release_id: null });
                  setReleaseOpen(false);
                }}
              >
                <span className="text-ink-muted">No milestone</span>
              </OptionRow>
              {releases.map((r) => (
                <OptionRow
                  key={r.id}
                  selected={currentRelease?.id === r.id}
                  onClick={() => {
                    if (currentRelease?.id !== r.id) onPatch({ release_id: r.id });
                    setReleaseOpen(false);
                  }}
                >
                  {r.name}
                </OptionRow>
              ))}
            </PopoverContent>
          </Popover>
        </Cell>
        <Cell label="Releases">
          {currentRelease && (
            <TagChip
              title={currentRelease.name}
              onRemove={disabled ? undefined : () => onPatch({ release_id: null })}
            >
              {currentRelease.name}
            </TagChip>
          )}
          {!currentRelease && (
            <Popover>
              <PopoverTrigger disabled={disabled} className={cn(cellTrigger, "text-ink-muted")}>
                <Plus className="h-3 w-3" />
                <span>Set release</span>
              </PopoverTrigger>
              <PopoverContent className="w-52 p-1" align="start">
                {releases.length === 0 ? (
                  <p className="px-2 py-1.5 text-xs text-ink-muted">No releases in this project.</p>
                ) : (
                  releases.map((r) => (
                    <OptionRow key={r.id} onClick={() => onPatch({ release_id: r.id })}>
                      {r.name}
                    </OptionRow>
                  ))
                )}
              </PopoverContent>
            </Popover>
          )}
        </Cell>
      </Group>
    </section>
  );
}
