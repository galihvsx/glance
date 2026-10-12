import { useEffect, useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { FolderInput } from "lucide-react";
import { api } from "../../lib/api";
import type { Estimate } from "../../lib/taxonomy";
import type {
  IssueProjectStructure,
  IssueState,
  Project,
} from "../../lib/types";
import { Button } from "../ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "../ui/dialog";
import { Skeleton } from "../ui/skeleton";
import { Alert, AlertDescription } from "../ui/alert";
import { cn } from "cn";

/** One estimate point the source issue carries, for preview matching. */
export interface SourceEstimate {
  scaleName: string;
  key: string;
  value: number;
}

export interface MovePreview {
  /** The state the issue will land in (null = target has no states). */
  targetState: { id: string; name: string } | null;
  /** True when no name match existed and the backlog fallback was used. */
  stateFallback: boolean;
  labelsKept: string[];
  labelsDropped: string[];
  estimate:
    | { kind: "none" }
    | { kind: "kept"; label: string }
    | { kind: "dropped" };
}

/**
 * computeMovePreview mirrors the backend move semantics exactly (see
 * service.MoveIssue): state matched by name case-insensitively else the
 * target's backlog-group state; estimate matched by value across the
 * target's scales; labels are workspace-scoped so they always match.
 * Pure — unit-tested in movePreview.test.ts.
 */
export function computeMovePreview(args: {
  sourceStateName: string | undefined;
  targetStates: IssueState[];
  sourceEstimate: SourceEstimate | null;
  targetEstimates: Estimate[];
  sourceLabelNames: string[];
}): MovePreview {
  const { sourceStateName, targetStates, sourceEstimate, targetEstimates, sourceLabelNames } = args;

  let targetState: MovePreview["targetState"] = null;
  let stateFallback = false;
  if (sourceStateName !== undefined) {
    const match = targetStates.find(
      (s) => s.name.toLowerCase() === sourceStateName.toLowerCase(),
    );
    if (match) {
      targetState = { id: match.id, name: match.name };
    } else {
      const backlog = [...targetStates]
        .filter((s) => s.group === "backlog")
        .sort((a, b) => a.sequence - b.sequence)[0];
      if (backlog) {
        targetState = { id: backlog.id, name: backlog.name };
        stateFallback = true;
      }
    }
  }

  let estimate: MovePreview["estimate"] = { kind: "none" };
  if (sourceEstimate !== null) {
    let hit: { scale: string; key: string } | null = null;
    for (const scale of targetEstimates) {
      const p = scale.points.find((pt) => pt.value === sourceEstimate.value);
      if (p) {
        hit = { scale: scale.name, key: p.key };
        break;
      }
    }
    estimate = hit
      ? { kind: "kept", label: `${hit.scale} · ${hit.key}` }
      : { kind: "dropped" };
  }

  return {
    targetState,
    stateFallback,
    labelsKept: [...sourceLabelNames],
    labelsDropped: [],
    estimate,
  };
}

function projectsPath(slug: string): string {
  return `/api/v1/workspaces/${encodeURIComponent(slug)}/projects`;
}

function useWorkspaceProjects(slug: string, open: boolean) {
  return useQuery({
    queryKey: ["projects", slug],
    queryFn: () =>
      api.get<{ projects: Project[] }>(projectsPath(slug)).then((d) => d.projects),
    enabled: open,
  });
}

function useProjectStates(slug: string, identifier: string | null, enabled: boolean) {
  return useQuery({
    queryKey: ["states", slug, identifier],
    queryFn: () =>
      api
        .get<{ states: IssueState[] }>(
          `/api/v1/workspaces/${encodeURIComponent(slug)}/projects/${encodeURIComponent(identifier!)}/states`,
        )
        .then((d) => d.states),
    enabled: enabled && identifier !== null,
  });
}

function useProjectEstimates(slug: string, identifier: string | null, enabled: boolean) {
  return useQuery({
    queryKey: ["estimates", slug, identifier],
    queryFn: () =>
      api
        .get<{ estimates: Estimate[] }>(
          `/api/v1/workspaces/${encodeURIComponent(slug)}/projects/${encodeURIComponent(identifier!)}/estimates`,
        )
        .then((d) => d.estimates),
    enabled: enabled && identifier !== null,
  });
}

/** Minimal issue shape the move dialog needs. */
export interface MoveIssueSubject {
  id: string;
  display_id: string;
  name: string;
  state_id: string;
  labels: { name: string }[];
  estimate_point_id?: string | null;
  project_structure?: IssueProjectStructure;
  custom_values?: Record<string, unknown>;
}

function findEstimate(
  estimates: Estimate[] | undefined,
  pointId: string | null | undefined,
): SourceEstimate | null {
  if (!pointId || !estimates) return null;
  for (const scale of estimates) {
    const p = scale.points.find((pt) => pt.id === pointId);
    if (p) return { scaleName: scale.name, key: p.key, value: p.value };
  }
  return null;
}

function ProjectPickerList({
  projects,
  selected,
  onSelect,
}: {
  projects: Project[];
  selected: string | null;
  onSelect: (identifier: string) => void;
}) {
  return (
    <div
      role="radiogroup"
      aria-label="Target project"
      className="max-h-56 overflow-y-auto rounded-md border border-border"
    >
      {projects.map((p) => {
        const active = selected === p.identifier;
        return (
          <button
            key={p.id}
            type="button"
            role="radio"
            aria-checked={active}
            onClick={() => onSelect(p.identifier)}
            className={cn(
              "flex w-full items-center gap-2 px-3 py-2 text-left text-sm transition-colors hover:bg-wash-hover",
              active && "bg-wash text-ink",
            )}
          >
            <span className="flex h-4 w-4 items-center justify-center rounded-full border border-border">
              {active && <span className="h-2 w-2 rounded-full bg-ink" />}
            </span>
            <span className="min-w-0 flex-1 truncate font-medium">{p.name}</span>
            <span className="rounded bg-wash px-1.5 py-0.5 font-mono text-[10px] text-ink-muted">
              {p.identifier}
            </span>
          </button>
        );
      })}
    </div>
  );
}

function DropsSummary({ issue }: { issue: MoveIssueSubject }) {
  const ps = issue.project_structure;
  const drops: string[] = [];
  if (ps?.cycle) drops.push(`cycle ${ps.cycle.name}`);
  for (const m of ps?.modules ?? []) drops.push(`module ${m.name}`);
  if (ps?.release) drops.push(`release ${ps.release.name}`);
  const customCount = issue.custom_values ? Object.keys(issue.custom_values).length : 0;
  if (customCount > 0)
    drops.push(`${customCount} custom field ${customCount === 1 ? "value" : "values"}`);
  if (drops.length === 0) return null;
  return (
    <div className="rounded-md border border-border bg-wash px-3 py-2 text-xs text-ink-secondary">
      <span className="font-medium text-ink">Will be removed:</span> {drops.join(", ")}
      {" "}(project-scoped data can't move across projects).
    </div>
  );
}

/**
 * MoveIssueDialog (C24T0): pick a target project, see the remap preview
 * (landing state, kept labels, estimate fate, dropped memberships)
 * BEFORE confirming. The caller performs the move in onConfirm.
 */
export default function MoveIssueDialog({
  open,
  onOpenChange,
  slug,
  sourceIdentifier,
  issue,
  sourceStates,
  onConfirm,
  confirming,
  error,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  slug: string;
  sourceIdentifier: string;
  issue: MoveIssueSubject;
  sourceStates: IssueState[];
  onConfirm: (targetIdentifier: string) => void;
  confirming: boolean;
  error: string | null;
}) {
  const [target, setTarget] = useState<string | null>(null);
  useEffect(() => {
    if (open) setTarget(null);
  }, [open ]);

  const projectsQuery = useWorkspaceProjects(slug, open);
  const targets = useMemo(
    () =>
      (projectsQuery.data ?? []).filter(
        (p) => p.identifier !== sourceIdentifier && !p.archived_at,
      ),
    [projectsQuery.data, sourceIdentifier],
  );

  const targetStatesQuery = useProjectStates(slug, target, open);
  const targetEstimatesQuery = useProjectEstimates(slug, target, open);
  const sourceEstimatesQuery = useProjectEstimates(slug, sourceIdentifier, open);

  const preview = useMemo<MovePreview | null>(() => {
    if (!target || !targetStatesQuery.data) return null;
    const sourceStateName = sourceStates.find((s) => s.id === issue.state_id)?.name;
    return computeMovePreview({
      sourceStateName,
      targetStates: targetStatesQuery.data,
      sourceEstimate: findEstimate(sourceEstimatesQuery.data, issue.estimate_point_id),
      targetEstimates: targetEstimatesQuery.data ?? [],
      sourceLabelNames: issue.labels.map((l) => l.name),
    });
  }, [
    target,
    targetStatesQuery.data,
    targetEstimatesQuery.data,
    sourceEstimatesQuery.data,
    sourceStates,
    issue,
  ]);

  const previewLoading =
    target !== null &&
    (targetStatesQuery.isPending || targetEstimatesQuery.isPending);

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-md">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            <FolderInput className="h-4 w-4" />
            Move issue to project
          </DialogTitle>
          <DialogDescription>
            <span className="font-mono">{issue.display_id}</span> · {issue.name} — the
            issue and its sub-issues move together and get new display IDs.
          </DialogDescription>
        </DialogHeader>

        <div className="space-y-3">
          {projectsQuery.isPending ? (
            <Skeleton className="h-40 w-full" />
          ) : projectsQuery.isError ? (
            <Alert variant="destructive">
              <AlertDescription>Failed to load projects.</AlertDescription>
            </Alert>
          ) : targets.length === 0 ? (
            <p className="text-sm text-ink-muted">
              No other projects in this workspace to move to.
            </p>
          ) : (
            <ProjectPickerList projects={targets} selected={target} onSelect={setTarget} />
          )}

          {target !== null && (
            <div className="space-y-2">
              <h4 className="text-xs font-medium text-ink-muted">Preview</h4>
              {previewLoading || !preview ? (
                <Skeleton className="h-24 w-full" />
              ) : (
                <div className="space-y-1.5 rounded-md border border-border bg-wash px-3 py-2 text-xs">
                  <div className="flex items-center justify-between gap-2">
                    <span className="text-ink-muted">Will land in</span>
                    <span className="font-medium text-ink">
                      {preview.targetState ? preview.targetState.name : "—"}
                      {preview.stateFallback && (
                        <span className="ml-1.5 rounded bg-amber-100 px-1.5 py-0.5 text-[10px] font-medium text-amber-800 dark:bg-amber-900/40 dark:text-amber-200">
                          fallback
                        </span>
                      )}
                    </span>
                  </div>
                  <div className="flex items-center justify-between gap-2">
                    <span className="text-ink-muted">Labels</span>
                    <span className="text-right font-medium text-ink">
                      {preview.labelsKept.length === 0
                        ? "none"
                        : `${preview.labelsKept.length} kept (${preview.labelsKept.join(", ")})`}
                    </span>
                  </div>
                  <div className="flex items-center justify-between gap-2">
                    <span className="text-ink-muted">Estimate</span>
                    <span className="font-medium text-ink">
                      {preview.estimate.kind === "none"
                        ? "none"
                        : preview.estimate.kind === "kept"
                          ? `kept → ${preview.estimate.label}`
                          : "dropped (no matching value)"}
                    </span>
                  </div>
                </div>
              )}
              <DropsSummary issue={issue} />
            </div>
          )}

          {error && (
            <Alert variant="destructive">
              <AlertDescription>{error}</AlertDescription>
            </Alert>
          )}
        </div>

        <DialogFooter>
          <Button variant="ghost" onClick={() => onOpenChange(false)} disabled={confirming}>
            Cancel
          </Button>
          <Button
            onClick={() => target && onConfirm(target)}
            disabled={!target || confirming || targets.length === 0}
          >
            {confirming ? "Moving…" : "Move issue"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

/**
 * BulkMoveDialog (C24T0): target-project picker for multi-select moves.
 * No per-issue preview — the caller moves sequentially and reports
 * partial success.
 */
export function BulkMoveDialog({
  open,
  onOpenChange,
  slug,
  sourceIdentifier,
  count,
  onConfirm,
  confirming,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  slug: string;
  sourceIdentifier: string;
  count: number;
  onConfirm: (targetIdentifier: string) => void;
  confirming: boolean;
}) {
  const [target, setTarget] = useState<string | null>(null);
  useEffect(() => {
    if (open) setTarget(null);
  }, [open ]);

  const projectsQuery = useWorkspaceProjects(slug, open);
  const targets = useMemo(
    () =>
      (projectsQuery.data ?? []).filter(
        (p) => p.identifier !== sourceIdentifier && !p.archived_at,
      ),
    [projectsQuery.data, sourceIdentifier],
  );

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-md">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            <FolderInput className="h-4 w-4" />
            Move {count} {count === 1 ? "issue" : "issues"} to project
          </DialogTitle>
          <DialogDescription>
            Each issue moves with its sub-issues and gets a new display ID.
            States remap by name (fallback: backlog), project-scoped data
            (cycles, modules, releases, custom values) is dropped.
          </DialogDescription>
        </DialogHeader>

        {projectsQuery.isPending ? (
          <Skeleton className="h-40 w-full" />
        ) : targets.length === 0 ? (
          <p className="text-sm text-ink-muted">
            No other projects in this workspace to move to.
          </p>
        ) : (
          <ProjectPickerList projects={targets} selected={target} onSelect={setTarget} />
        )}

        <DialogFooter>
          <Button variant="ghost" onClick={() => onOpenChange(false)} disabled={confirming}>
            Cancel
          </Button>
          <Button
            onClick={() => target && onConfirm(target)}
            disabled={!target || confirming || targets.length === 0}
          >
            {confirming ? "Moving…" : `Move ${count} ${count === 1 ? "issue" : "issues"}`}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
