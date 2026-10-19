// ProjectLabelAssignment (C76T1): per-project assignment of the
// workspace project-label taxonomy — a LabelPicker-style multi-select
// popover (checkboxes + color dots). Guests get a read-only chip view;
// members assign/unassign, which invalidates the project detail and the
// workspace project list (both embed the chips).

import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Tags } from "lucide-react";
import { api, ApiError } from "../../lib/api";
import type { Project } from "../../lib/types";
import {
  assignProjectLabel,
  fetchProjectLabels,
  projectLabelKeys,
  unassignProjectLabel,
} from "../../lib/projectLabels";
import ProjectLabelChips from "./ProjectLabelChips";
import { Alert, AlertDescription } from "../ui/alert";
import { buttonVariants } from "../ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "../ui/card";
import { Checkbox } from "../ui/checkbox";
import { Label } from "../ui/label";
import { Popover, PopoverContent, PopoverTrigger } from "../ui/popover";
import { ScrollArea } from "../ui/scroll-area";
import { Skeleton } from "../ui/skeleton";
import { cn } from "cn";

function errMsg(err: unknown, fallback: string): string {
  return err instanceof ApiError ? err.message : fallback;
}

export default function ProjectLabelAssignment({
  slug,
  identifier,
  canEdit,
}: {
  slug: string;
  identifier: string;
  canEdit: boolean;
}) {
  const queryClient = useQueryClient();
  const [open, setOpen] = useState(false);
  const [error, setError] = useState<string | null>(null);

  // Same query key as GeneralSection — the detail endpoint embeds the
  // assigned labels, so both stay in sync.
  const projectQuery = useQuery({
    queryKey: ["project", slug, identifier],
    queryFn: () =>
      api.get<Project>(
        `/api/v1/workspaces/${encodeURIComponent(slug)}/projects/${encodeURIComponent(identifier)}`,
      ),
  });
  const labelsQuery = useQuery({
    queryKey: projectLabelKeys(slug).labels,
    queryFn: () => fetchProjectLabels(slug),
    enabled: canEdit,
  });

  const assigned = projectQuery.data?.labels ?? [];
  const assignedIds = new Set(assigned.map((l) => l.id));
  const labels = [...(labelsQuery.data ?? [])].sort((a, b) =>
    a.name.localeCompare(b.name),
  );

  const invalidate = () => {
    void queryClient.invalidateQueries({
      queryKey: ["project", slug, identifier],
    });
    void queryClient.invalidateQueries({ queryKey: ["projects", slug] });
  };

  const toggleMutation = useMutation({
    mutationFn: async ({
      labelId,
      currentlyApplied,
    }: {
      labelId: string;
      currentlyApplied: boolean;
    }) => {
      if (currentlyApplied) {
        await unassignProjectLabel(slug, identifier, labelId);
      } else {
        await assignProjectLabel(slug, identifier, labelId);
      }
    },
    onSuccess: () => {
      setError(null);
      invalidate();
    },
    onError: (e) => setError(errMsg(e, "Failed to update project labels")),
  });

  return (
    <Card className="rounded-lg border-border-hairline">
      <CardHeader className="flex flex-row items-center justify-between">
        <CardTitle className="text-xs font-semibold">Project labels</CardTitle>
        {canEdit && (
          <Popover open={open} onOpenChange={setOpen}>
            <PopoverTrigger
              disabled={toggleMutation.isPending}
              className={cn(
                buttonVariants({ variant: "outline", size: "sm" }),
                "gap-2",
              )}
            >
              <Tags className="h-4 w-4" aria-hidden />
              {assigned.length === 0
                ? "Assign labels"
                : `${assigned.length} label${assigned.length > 1 ? "s" : ""}`}
            </PopoverTrigger>
            <PopoverContent className="w-64 p-2" align="end">
              {labelsQuery.isPending ? (
                <div className="space-y-2 p-2">
                  <Skeleton className="h-6 w-full" />
                  <Skeleton className="h-6 w-full" />
                </div>
              ) : labels.length === 0 ? (
                <p className="p-2 text-sm text-muted-foreground">
                  No project labels in this workspace yet. Manage them from
                  the Projects page.
                </p>
              ) : (
                <ScrollArea className="max-h-64">
                  {labels.map((l) => {
                    const checked = assignedIds.has(l.id);
                    return (
                      <Label
                        key={l.id}
                        className="flex cursor-pointer items-center gap-2 rounded-md px-2 py-1.5 text-sm hover:bg-accent"
                      >
                        <Checkbox
                          checked={checked}
                          onCheckedChange={() =>
                            toggleMutation.mutate({
                              labelId: l.id,
                              currentlyApplied: checked,
                            })
                          }
                        />
                        <span
                          className="h-2.5 w-2.5 shrink-0 rounded-full"
                          style={{ backgroundColor: l.color }}
                          aria-hidden
                        />
                        <span className="truncate">{l.name}</span>
                      </Label>
                    );
                  })}
                </ScrollArea>
              )}
            </PopoverContent>
          </Popover>
        )}
      </CardHeader>
      <CardContent className="space-y-3">
        {error && (
          <Alert variant="destructive">
            <AlertDescription>{error}</AlertDescription>
          </Alert>
        )}
        {!canEdit && (
          <p className="text-xs text-muted-foreground">
            You are a guest here — labels are read-only. Members can assign
            them.
          </p>
        )}
        {projectQuery.isPending ? (
          <Skeleton className="h-8 w-full" />
        ) : assigned.length === 0 ? (
          <p className="text-xs text-muted-foreground">
            No labels on this project.
          </p>
        ) : (
          <ProjectLabelChips labels={assigned} max={10} />
        )}
      </CardContent>
    </Card>
  );
}
