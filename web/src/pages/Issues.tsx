import { useEffect, useMemo, useRef, useState, type FormEvent } from "react";
import { useLocation, useNavigate, useParams, useSearchParams } from "react-router-dom";
import {
  useInfiniteQuery,
  useMutation,
  useQuery,
  useQueryClient,
} from "@tanstack/react-query";
import { Download, Search } from "lucide-react";
import { api, ApiError } from "../lib/api";
import type {
  Cycle,
  Issue,
  IssueListResult,
  IssueState,
  Label as ProjectLabel,
  Member,
  Module,
  Project,
} from "../lib/types";
import { Button } from "../components/ui/button";
import { Card, CardDescription, CardHeader, CardTitle } from "../components/ui/card";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "../components/ui/dialog";
import { Input } from "../components/ui/input";
import { Label } from "../components/ui/label";
import { Skeleton } from "../components/ui/skeleton";
import { Alert, AlertDescription } from "../components/ui/alert";
import { Textarea } from "../components/ui/textarea";
import IssueRow from "../components/issue/IssueRow";
import DuplicateSuggestions from "../components/issue/DuplicateSuggestions";
import PeekDrawer from "../components/issue/PeekDrawer";
import { usePeekParam } from "../components/issue/usePeek";
import PriorityPicker from "../components/issue/PriorityPicker";
import ParentPicker from "../components/issue/ParentPicker";
import QuickAdd from "../components/issue/QuickAdd";
import DraftWithAI from "../components/issue/DraftWithAI";
import BulkActionBar from "../components/issue/BulkActionBar";
import {
  useBulkSelection,
  type BulkSetPayload,
} from "../components/issue/bulkSelection";
import StatePicker from "../components/issue/StatePicker";
import EstimateSelect from "../components/issue/EstimateSelect";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "../components/ui/select";
import {
  applyTemplateToForm,
  fetchTemplates,
  templateDataToDefaults,
  templateKeys,
  type IssueTemplate,
} from "../lib/templates";
import { fetchEstimates, type Estimate } from "../lib/taxonomy";
import DisplayPanel from "../components/issue/DisplayPanel";
import { useDisplaySettings } from "../components/issue/useDisplaySettings";
import FilterPanel from "../components/issue/FilterPanel";
import SavedViewsMenu from "../components/issue/SavedViewsMenu";
import PageHeader, {
  pageHeaderControlClass,
} from "../components/shell/PageHeader";
import {
  activeFilterCount,
  toApiParams,
  useIssueFilters,
  type IssueFilters,
} from "../lib/filters";
import {
  buildExportUrl,
  downloadExport,
  type ExportFormat,
} from "../lib/export";
import { Checkbox } from "../components/ui/checkbox";
import { RequiredCustomFieldsForm } from "../components/issue/IssueCustomFields";
import {
  customFieldKeys,
  fetchCustomFields,
  withCheckboxDefaults,
  type CustomValueInput,
} from "../lib/customFields";
import { toast } from "../components/ui/toast";
import { priorityLabel } from "../lib/types";
import { stateDotClass } from "../lib/stateDot";
import PriorityBars from "../components/issue/PriorityBars";
import { useShortcutAction, isTypingTarget } from "../lib/shortcuts";

const PER_PAGE = 25;

/** Best-effort clipboard copy; silently ignored where unavailable. */
function copyToClipboard(text: string) {
  void (async () => {
    try {
      await navigator.clipboard.writeText(text);
    } catch {
      // Clipboard API unavailable (e.g. insecure context) — ignore.
    }
  })();
}

/** Wraps a paragraph of plain text as a minimal TipTap doc. */
function textToTipTapDoc(text: string): unknown {
  const t = text.trim();
  if (!t) return null;
  return {
    type: "doc",
    content: [
      {
        type: "paragraph",
        content: [{ type: "text", text: t }],
      },
    ],
  };
}

function buildIssuePath(
  slug: string,
  identifier: string,
  f: IssueFilters,
  orderBy: string,
  cursor?: string,
): string {
  const p = toApiParams(f);
  p.set("per_page", String(PER_PAGE));
  p.set("order_by", orderBy);
  if (cursor) p.set("cursor", cursor);
  return `/api/v1/workspaces/${encodeURIComponent(slug)}/projects/${encodeURIComponent(identifier)}/issues?${p}`;
}

export default function Issues() {
  const { slug = "", identifier = "" } = useParams<{
    slug: string;
    identifier: string;
  }>();
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const location = useLocation();
  const [filters, setFilters, clearFilters] = useIssueFilters();
  // Seed from the URL so a pasted link shows its query in the box.
  const [searchInput, setSearchInput] = useState(filters.q);
  // C8T3: export state — format selector + download with the current filters.
  const [exportFormat, setExportFormat] = useState<ExportFormat>("csv");
  const [exporting, setExporting] = useState(false);
  const [exportError, setExportError] = useState<string | null>(null);

  const onExport = async () => {
    setExporting(true);
    setExportError(null);
    try {
      await downloadExport(
        buildExportUrl(slug, identifier, filters, exportFormat),
      );
    } catch (e) {
      setExportError(e instanceof Error ? e.message : "Export failed");
    } finally {
      setExporting(false);
    }
  };
  const [dialogOpen, setDialogOpen] = useState(false);
  // C19T4: ?create=1 deep-link opens the create dialog — the shared
  // PageHeader's "Add work item" button navigates here with that param from
  // views that have no create dialog of their own.
  const [searchParams, setSearchParams] = useSearchParams();
  useEffect(() => {
    if (searchParams.get("create") === "1") {
      setDialogOpen(true);
      const next = new URLSearchParams(searchParams);
      next.delete("create");
      setSearchParams(next, { replace: true });
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);
  const [selected, setSelected] = useState(0);
  const searchRef = useRef<HTMLInputElement>(null);
  const { settings, update, updateFields, reset } = useDisplaySettings(
    slug,
    identifier,
  );
  const { peekUuid, openPeek, closePeek } = usePeekParam();

  // Opened from the command palette ("Create issue" action) or from the
  // board's inline quick-add expand button (prefills title + state, and
  // priority when the board is grouped by priority).
  useEffect(() => {
    const st = location.state as {
      newIssue?: boolean;
      newIssueName?: string;
      newIssueStateId?: string;
      newIssuePriority?: number;
    } | null;
    if (st?.newIssue) {
      if (typeof st.newIssueName === "string") setName(st.newIssueName);
      if (typeof st.newIssueStateId === "string")
        setNewStateId(st.newIssueStateId);
      if (typeof st.newIssuePriority === "number")
        setNewPriority(st.newIssuePriority);
      setDialogOpen(true);
      navigate(location.pathname, { replace: true });
    }
    // Only on mount — the replace clears state so this can't loop.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  useShortcutAction("new-issue", () => setDialogOpen(true));
  useShortcutAction("focus-search", () => searchRef.current?.focus());
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [newPriority, setNewPriority] = useState(0);
  const [newStateId, setNewStateId] = useState("");
  const [newEstimatePointId, setNewEstimatePointId] = useState("");
  const [parentIssue, setParentIssue] = useState<Issue | null>(null);
  const [createMore, setCreateMore] = useState(false);
  const [formError, setFormError] = useState<string | null>(null);
  // C18T1: required custom-field values for the create form (field id →
  // value), owned here and rendered by RequiredCustomFieldsForm.
  const [customFieldValues, setCustomFieldValues] = useState<
    Record<string, CustomValueInput>
  >({});
  // C7T1: template picked from the dropdown + any note about dropped
  // defaults (stale refs, labels that can't be set at creation).
  const [appliedTemplate, setAppliedTemplate] = useState<IssueTemplate | null>(
    null,
  );
  const [templateNote, setTemplateNote] = useState<string | null>(null);
  // C5T3: true after a Draft-with-AI insert, until the user edits the
  // description or the form resets — drives the honesty hint.
  const [aiGenerated, setAiGenerated] = useState(false);

  const base = `/api/v1/workspaces/${encodeURIComponent(slug)}/projects/${encodeURIComponent(identifier)}`;

  const projectQuery = useQuery({
    queryKey: ["project", slug, identifier],
    queryFn: () => api.get<Project>(base),
  });
  const statesQuery = useQuery({
    queryKey: ["states", slug, identifier],
    queryFn: () =>
      api.get<{ states: IssueState[] }>(`${base}/states`).then((d) => d.states),
  });

  // C7T1: templates + estimates for the create modal's template picker.
  // Fetched lazily — only once the create dialog opens.
  const templatesQuery = useQuery({
    queryKey: templateKeys(slug, identifier).templates,
    queryFn: () => fetchTemplates(slug, identifier),
    enabled: dialogOpen,
  });
  const estimatesQuery = useQuery({
    queryKey: ["estimates", slug, identifier],
    queryFn: () => fetchEstimates(slug, identifier),
    enabled: dialogOpen,
  });
  const labelsForTemplateQuery = useQuery({
    queryKey: ["labels", slug, identifier],
    queryFn: () =>
      api
        .get<{ labels: ProjectLabel[] }>(`${base}/labels`)
        .then((d) => d.labels),
    enabled: dialogOpen,
  });

  // C18T1: required custom fields for the create form — fetched lazily
  // with the dialog (shares the cache key with the issue-detail editors).
  // The server 400s the create naming any required field left empty.
  const customFieldsQuery = useQuery({
    queryKey: customFieldKeys(slug, identifier).fields,
    queryFn: () => fetchCustomFields(slug, identifier),
    enabled: dialogOpen,
  });
  const requiredCustomFields = (customFieldsQuery.data ?? []).filter(
    (f) => f.required,
  );

  /**
   * Apply a template to the create form (C7T1). Explicit user action via the
   * picker; the user reviews the prefilled values before saving. Refs that
   * no longer exist (a state/estimate/label deleted after the template was
   * saved) are dropped with an honest note instead of creating a wrong
   * issue.
   */
  function applyTemplate(t: IssueTemplate) {
    const states = statesQuery.data ?? [];
    const estimates: Estimate[] = estimatesQuery.data ?? [];
    const knownLabels = new Set(
      (labelsForTemplateQuery.data ?? []).map((l) => l.id),
    );
    const merged = applyTemplateToForm(t, {
      name,
      description,
      priority: newPriority,
      stateId: newStateId,
      estimatePointId: newEstimatePointId,
      labelIds: [],
    });
    const notes: string[] = [];
    let stateId = merged.stateId;
    if (stateId && !states.some((s) => s.id === stateId)) {
      stateId = "";
      notes.push("its state no longer exists");
    }
    const pointIds = new Set(
      estimates.flatMap((e) => e.points.map((p) => p.id)),
    );
    let estimatePointId = merged.estimatePointId;
    if (estimatePointId && !pointIds.has(estimatePointId)) {
      estimatePointId = "";
      notes.push("its estimate no longer exists");
    }
    const defaults = templateDataToDefaults(t.template_data);
    const liveLabelIds = defaults.labelIds.filter((id) =>
      knownLabels.has(id),
    );
    if (liveLabelIds.length > 0) {
      notes.push(
        `sets ${liveLabelIds.length} label${liveLabelIds.length > 1 ? "s" : ""} — add them after creating the issue`,
      );
    } else if (defaults.labelIds.length > 0) {
      notes.push("its labels no longer exist");
    }
    setName(merged.name);
    setDescription(merged.description);
    setNewPriority(merged.priority);
    setNewStateId(stateId);
    setNewEstimatePointId(estimatePointId);
    setFormError(null);
    setAiGenerated(false);
    setAppliedTemplate(t);
    setTemplateNote(
      notes.length > 0 ? `Template "${t.name}": ${notes.join("; ")}.` : null,
    );
  }

  const issuesQuery = useInfiniteQuery({
    queryKey: ["issues", slug, identifier, filters, settings.orderBy],
    queryFn: ({ pageParam }: { pageParam?: string }) =>
      api.get<IssueListResult>(
        buildIssuePath(slug, identifier, filters, settings.orderBy, pageParam),
      ),
    getNextPageParam: (last) => last.next_cursor || undefined,
    initialPageParam: undefined as string | undefined,
  });

  const stateById = new Map((statesQuery.data ?? []).map((s) => [s.id, s]));

  // C5T8 bulk operations: list-view multi-select + action bar. Members
  // and labels are fetched lazily — only once something is selected.
  const bulk = useBulkSelection();
  const [bulkError, setBulkError] = useState<string | null>(null);
  const [membershipError, setMembershipError] = useState<string | null>(null);
  const membersQuery = useQuery({
    queryKey: ["members", slug],
    queryFn: () =>
      api
        .get<{ members: Member[] }>(
          `/api/v1/workspaces/${encodeURIComponent(slug)}/members`,
        )
        .then((d) => d.members),
    enabled: bulk.count > 0,
  });
  const labelsQuery = useQuery({
    queryKey: ["labels", slug, identifier],
    queryFn: () =>
      api
        .get<{ labels: ProjectLabel[] }>(`${base}/labels`)
        .then((d) => d.labels),
    enabled: bulk.count > 0,
  });
  const cyclesQuery = useQuery({
    queryKey: ["cycles", slug, identifier],
    queryFn: () =>
      api.get<{ cycles: Cycle[] }>(`${base}/cycles`).then((d) => d.cycles),
    enabled: bulk.count > 0,
  });
  const modulesQuery = useQuery({
    queryKey: ["modules", slug, identifier],
    queryFn: () =>
      api.get<{ modules: Module[] }>(`${base}/modules`).then((d) => d.modules),
    enabled: bulk.count > 0,
  });
  const bulkMutation = useMutation({
    mutationFn: (set: BulkSetPayload) =>
      api.patch<{ updated: number; issue_ids: string[] }>(
        `${base}/issues/bulk`,
        { issue_ids: bulk.selectedIds, set },
      ),
    onSuccess: (res) => {
      bulk.clear();
      setBulkError(null);
      void queryClient.invalidateQueries({
        queryKey: ["issues", slug, identifier],
      });
      toast.add({
        title: `Updated ${res.updated} issue${res.updated === 1 ? "" : "s"}`,
        type: "success",
      });
    },
    onError: (e) => {
      setBulkError(e instanceof ApiError ? e.message : "Bulk update failed");
    },
  });

  /** C24T2: bulk "add to cycle / module". Membership POSTs are idempotent
   *  — the server skips already-members and returns {added: n}. On
   *  success the selection clears (like the bulk PATCH), the issue list
   *  revalidates (existing bulk pattern), and the cycle/module lists
   *  revalidate so their issue counts stay fresh. */
  function onMembershipAdded(
    kind: "cycle" | "module",
    targetId: string,
    added: number,
  ) {
    const name =
      kind === "cycle"
        ? cyclesQuery.data?.find((c) => c.id === targetId)?.name
        : modulesQuery.data?.find((m) => m.id === targetId)?.name;
    bulk.clear();
    setMembershipError(null);
    void queryClient.invalidateQueries({
      queryKey: ["issues", slug, identifier],
    });
    void queryClient.invalidateQueries({
      queryKey: [kind === "cycle" ? "cycles" : "modules", slug, identifier],
    });
    toast.add({
      title: `Added ${added} issue${added === 1 ? "" : "s"} to ${kind}`,
      description: name,
      type: "success",
    });
  }

  const bulkCycleMutation = useMutation({
    mutationFn: (cycleId: string) =>
      api.post<{ added: number }>(
        `${base}/cycles/${encodeURIComponent(cycleId)}/issues`,
        { issue_ids: bulk.selectedIds },
      ),
    onSuccess: (res, cycleId) => onMembershipAdded("cycle", cycleId, res.added),
    onError: (e) => {
      setMembershipError(
        e instanceof ApiError ? e.message : "Failed to add issues to cycle",
      );
    },
  });

  const bulkModuleMutation = useMutation({
    mutationFn: (moduleId: string) =>
      api.post<{ added: number }>(
        `${base}/modules/${encodeURIComponent(moduleId)}/issues`,
        { issue_ids: bulk.selectedIds },
      ),
    onSuccess: (res, moduleId) =>
      onMembershipAdded("module", moduleId, res.added),
    onError: (e) => {
      setMembershipError(
        e instanceof ApiError ? e.message : "Failed to add issues to module",
      );
    },
  });

  const createMutation = useMutation({
    mutationFn: (body: {
      name: string;
      description: unknown;
      priority: number;
      state_id?: string;
      estimate_point_id?: string;
      parent_id?: string;
      is_draft?: boolean;
      custom_values?: Record<string, CustomValueInput>;
    }) => api.post<Issue>(`${base}/issues`, body),
    onSuccess: (issue) => {
      resetCreateForm();
      // "Create more" keeps the dialog open for rapid entry; otherwise
      // the dialog closes and the toast is the confirmation.
      if (!createMore) setDialogOpen(false);
      void queryClient.invalidateQueries({
        queryKey: ["issues", slug, identifier],
      });
      showCreatedToast(issue);
      if (issue.is_draft) {
        void queryClient.invalidateQueries({
          queryKey: ["drafts", slug, identifier],
        });
      }
    },
    onError: (e) => {
      setFormError(
        e instanceof ApiError ? e.message : "Failed to create issue",
      );
    },
  });

  /** Clear the create form (after a successful create). */
  function resetCreateForm() {
    setName("");
    setDescription("");
    setNewPriority(0);
    setNewStateId("");
    setNewEstimatePointId("");
    setParentIssue(null);
    setFormError(null);
    setAiGenerated(false);
    setAppliedTemplate(null);
    setTemplateNote(null);
    setCustomFieldValues({});
  }

  /** Custom-field values for the create body: untouched required
   *  checkboxes default to false (unchecked IS the displayed state).
   *  Empty when the project has no required fields / nothing filled. */
  function createCustomValues(): Record<string, CustomValueInput> {
    return withCheckboxDefaults(requiredCustomFields, customFieldValues);
  }

  /** Success toast for a created issue: display_id + Copy link / View. */
  function showCreatedToast(issue: Issue) {
    const url = `${window.location.origin}/w/${slug}/p/${identifier}/i/${issue.id}`;
    toast.add({
      title: issue.is_draft
        ? `${issue.display_id} saved as draft`
        : `${issue.display_id} created`,
      type: "success",
      description: (
        <span className="flex flex-col gap-2">
          <span className="truncate">{issue.name}</span>
          <span className="flex gap-2">
            <button
              type="button"
              className="rounded-md border px-2 py-1 text-xs font-medium hover:bg-accent"
              onClick={() => copyToClipboard(url)}
            >
              Copy link
            </button>
            <button
              type="button"
              className="rounded-md border px-2 py-1 text-xs font-medium hover:bg-accent"
              onClick={() => {
                setDialogOpen(false);
                openPeek(issue.id);
              }}
            >
              View
            </button>
          </span>
        </span>
      ),
    });
  }

  function onSearchSubmit(e: FormEvent) {
    e.preventDefault();
    setFilters({ q: searchInput });
  }

  const hasActiveFilters = activeFilterCount(filters) > 0;

  async function onCreate(e: FormEvent) {
    e.preventDefault();
    setFormError(null);
    const customValues = createCustomValues();
    createMutation.mutate({
      name: name.trim(),
      description: textToTipTapDoc(description) ?? undefined,
      priority: newPriority,
      ...(newStateId ? { state_id: newStateId } : {}),
      ...(newEstimatePointId ? { estimate_point_id: newEstimatePointId } : {}),
      ...(parentIssue ? { parent_id: parentIssue.id } : {}),
      // C18T1: required custom fields ride along; the server 400s naming
      // any left empty, surfaced inline via setFormError below.
      ...(Object.keys(customValues).length > 0
        ? { custom_values: customValues }
        : {}),
    });
  }

  /** Save as draft (C6T4): same body plus is_draft — the issue lands in the
   *  Drafts view instead of the working set. Drafts skip the required
   *  check server-side, but filled values are still stored. */
  async function onSaveDraft(e: FormEvent) {
    e.preventDefault();
    setFormError(null);
    const customValues = createCustomValues();
    createMutation.mutate({
      name: name.trim(),
      description: textToTipTapDoc(description) ?? undefined,
      priority: newPriority,
      ...(newStateId ? { state_id: newStateId } : {}),
      ...(newEstimatePointId ? { estimate_point_id: newEstimatePointId } : {}),
      ...(parentIssue ? { parent_id: parentIssue.id } : {}),
      is_draft: true,
      ...(Object.keys(customValues).length > 0
        ? { custom_values: customValues }
        : {}),
    });
  }

  /** Inline quick-add: title only. The new row appears via query
   *  invalidation — no full reload. */
  async function quickCreate(
    stateId: string,
    name: string,
    priority?: number,
  ): Promise<void> {
    await api.post<Issue>(`${base}/issues`, {
      name,
      ...(stateId ? { state_id: stateId } : {}),
      ...(priority !== undefined ? { priority } : {}),
    });
    await queryClient.invalidateQueries({
      queryKey: ["issues", slug, identifier],
    });
  }

  /** Expand an inline quick-add into the full create form, keeping the
   *  typed title and the group's state/priority. */
  function expandQuickAdd(stateId: string, name: string, priority?: number) {
    setName(name);
    setNewStateId(stateId);
    if (priority !== undefined) setNewPriority(priority);
    setFormError(null);
    setDialogOpen(true);
  }

  const issues = issuesQuery.data?.pages.flatMap((p) => p.results) ?? [];
  const states = statesQuery.data ?? [];

  // "Select all" covers every loaded page (the selection is a set of
  // ids, independent of the rendered rows).
  const allLoadedSelected =
    issues.length > 0 && issues.every((i) => bulk.isSelected(i.id));
  function toggleSelectAllLoaded() {
    if (allLoadedSelected) {
      bulk.clear();
    } else {
      bulk.selectAll(issues.map((i) => i.id));
    }
  }
  const sortedStates = useMemo(
    () => [...states].sort((a, b) => a.sequence - b.sequence),
    [states],
  );
  // Plane-style grouping, driven by the Display panel. State grouping is
  // skipped when filtering to a single state (or before states load);
  // priority grouping is skipped when filtering to a single priority.
  const groupByState =
    settings.groupBy === "state" && sortedStates.length > 0 && !filters.state;
  const groupByPriority =
    settings.groupBy === "priority" && filters.priorities.length !== 1;
  const groupingActive = groupByState || groupByPriority;

  interface IssueGroup {
    key: string;
    title: string;
    color?: string;
    /** Workflow group for the header state-dot (state grouping). */
    stateGroup?: string;
    issues: Issue[];
    stateId?: string;
    priority?: number;
  }

  const groups = useMemo<IssueGroup[]>(() => {
    let gs: IssueGroup[] = [];
    if (groupByState) {
      gs = sortedStates.map((s) => ({
        key: s.id,
        title: s.name,
        color: s.color,
        stateGroup: s.group,
        issues: issues.filter((i) => i.state_id === s.id),
        stateId: s.id,
      }));
    } else if (groupByPriority) {
      gs = [4, 3, 2, 1, 0].map((p) => ({
        key: `priority:${p}`,
        title: priorityLabel(p),
        issues: issues.filter((i) => i.priority === p),
        stateId: sortedStates[0]?.id,
        priority: p,
      }));
    }
    return settings.showEmptyGroups
      ? gs
      : gs.filter((g) => g.issues.length > 0);
  }, [
    groupByState,
    groupByPriority,
    sortedStates,
    issues,
    settings.showEmptyGroups,
  ]);
  const orderedIssues = groupingActive
    ? groups.flatMap((g) => g.issues)
    : issues;
  const indexById = useMemo(
    () => new Map(orderedIssues.map((it, i) => [it.id, i] as const)),
    [orderedIssues],
  );
  const selectedId = orderedIssues[selected]?.id;

  // j/k move the list selection; Enter opens the selected issue in the
  // peek drawer (keeping list context), not the full page.
  useEffect(() => {
    setSelected(0);
  }, [issuesQuery.data]);
  useShortcutAction("next-item", () =>
    setSelected((s) => Math.min(s + 1, Math.max(orderedIssues.length - 1, 0))),
  );
  useShortcutAction("prev-item", () =>
    setSelected((s) => Math.max(s - 1, 0)),
  );
  useEffect(() => {
    const onKeyDown = (e: KeyboardEvent) => {
      if (
        e.key === "Enter" &&
        !e.metaKey &&
        !e.ctrlKey &&
        !e.altKey &&
        !isTypingTarget(e.target) &&
        selectedId
      ) {
        openPeek(selectedId);
      }
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [selectedId, openPeek]);

  return (
    <div className="w-full">
      {/* C19T4: shared page header replaces the per-page h1 + ProjectNav. */}
      <PageHeader
        viewName="Work items"
        count={issues.length}
        showViews
        filterPanel={
          <FilterPanel
            slug={slug}
            identifier={identifier}
            filters={filters}
            onChange={setFilters}
            onClear={clearFilters}
            triggerClassName={pageHeaderControlClass}
          />
        }
        displayPanel={
          <DisplayPanel
            settings={settings}
            onUpdate={update}
            onUpdateFields={updateFields}
            onReset={reset}
            view="list"
            triggerClassName={pageHeaderControlClass}
          />
        }
        extraControls={
          <SavedViewsMenu
            key={`${slug}:${identifier}`}
            projectId={projectQuery.data?.id ?? ""}
            filters={filters}
            display={settings}
            onApplyDisplay={update}
            triggerClassName={pageHeaderControlClass}
          />
        }
        addButton={{ onClick: () => setDialogOpen(true) }}
        menuItems={[
          {
            label: "Intake inbox",
            onClick: () => navigate(`/w/${slug}/p/${identifier}/intake`),
          },
        ]}
      />
      {/* C19T5: prototype shell content — max-width 1060px centered,
          padding 24px 28px. */}
      <div className="mx-auto w-full max-w-[1060px] px-7 py-6">
        <Dialog
          open={dialogOpen}
          onOpenChange={(open) => {
              setDialogOpen(open);
              // A picked parent or template must not leak into the next create.
              if (!open) {
                setParentIssue(null);
                setAppliedTemplate(null);
                setTemplateNote(null);
              }
            }}
          >
          <DialogContent>
            <DialogHeader>
              <DialogTitle>New issue</DialogTitle>
              <DialogDescription>
                It gets the next sequence number, e.g. {identifier}-1.
              </DialogDescription>
            </DialogHeader>
            <form onSubmit={onCreate} className="space-y-4">
              {/* C7T1: template picker — selecting one prefills the form;
                  the user reviews before saving. */}
              {(templatesQuery.data ?? []).length > 0 && (
                <div className="space-y-2">
                  <Label>Template</Label>
                  <Select
                    value={appliedTemplate?.id ?? "__none"}
                    onValueChange={(v) => {
                      const t = (templatesQuery.data ?? []).find(
                        (x) => x.id === v,
                      );
                      if (t) applyTemplate(t);
                    }}
                  >
                    <SelectTrigger className="w-full">
                      <SelectValue placeholder="Use a template…" />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem value="__none" disabled>
                        Use a template…
                      </SelectItem>
                      {(templatesQuery.data ?? []).map((t) => (
                        <SelectItem key={t.id} value={t.id}>
                          {t.name}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                  {appliedTemplate && (
                    <div className="flex items-center justify-between rounded-md bg-accent px-2 py-1 text-xs">
                      <span className="truncate">
                        Using template{" "}
                        <span className="font-medium">
                          “{appliedTemplate.name}”
                        </span>
                      </span>
                      <Button
                        type="button"
                        variant="ghost"
                        size="sm"
                        className="h-6 px-2"
                        onClick={() => {
                          setAppliedTemplate(null);
                          setTemplateNote(null);
                        }}
                      >
                        Clear
                      </Button>
                    </div>
                  )}
                  {templateNote && (
                    <p className="text-xs text-muted-foreground">
                      {templateNote}
                    </p>
                  )}
                </div>
              )}
              <div className="space-y-2">
                <Label htmlFor="i-name">Title</Label>
                <Input
                  id="i-name"
                  value={name}
                  onChange={(e) => setName(e.target.value)}
                  placeholder="Fix the login redirect"
                  required
                />
                {/* C8T7: duplicate detection — suggestions as the title is
                    typed (debounced 400ms); each row opens the peek drawer. */}
                <DuplicateSuggestions
                  slug={slug}
                  identifier={identifier}
                  title={name}
                  open={dialogOpen}
                  onOpenPeek={openPeek}
                />
              </div>
              <div className="space-y-2">
                <div className="flex items-center justify-between">
                  <Label htmlFor="i-desc">Description</Label>
                  <DraftWithAI
                    slug={slug}
                    identifier={identifier}
                    title={name}
                    onDraft={(d) => {
                      setDescription(d);
                      setAiGenerated(true);
                    }}
                    onError={setFormError}
                    disabled={createMutation.isPending}
                  />
                </div>
                <Textarea
                  id="i-desc"
                  value={description}
                  onChange={(e) => {
                    setDescription(e.target.value);
                    setAiGenerated(false);
                  }}
                  placeholder="What needs to happen…"
                  rows={4}
                />
                {aiGenerated && (
                  <p className="text-xs text-muted-foreground">
                    AI-generated — review before saving.
                  </p>
                )}
              </div>
              <div className="flex flex-wrap gap-4">
                <div className="space-y-2">
                  <Label>Priority</Label>
                  <PriorityPicker
                    value={newPriority}
                    onChange={setNewPriority}
                  />
                </div>
                {states.length > 0 && (
                  <div className="space-y-2">
                    <Label>State</Label>
                    <StatePicker
                      states={states}
                      value={newStateId}
                      onChange={setNewStateId}
                    />
                  </div>
                )}
                <div className="space-y-2">
                  <Label>Estimate</Label>
                  <EstimateSelect
                    estimates={estimatesQuery.data ?? []}
                    value={newEstimatePointId}
                    onChange={setNewEstimatePointId}
                  />
                </div>
              </div>
              <div className="space-y-2">
                <Label>Parent</Label>
                <ParentPicker
                  slug={slug}
                  identifier={identifier}
                  value={parentIssue}
                  onChange={setParentIssue}
                  disabled={createMutation.isPending}
                />
              </div>
              {/* C18T1: required custom fields — the server 400s the
                  create naming any left empty (surfaced in formError). */}
              {requiredCustomFields.length > 0 && (
                <div className="space-y-2">
                  <RequiredCustomFieldsForm
                    fields={requiredCustomFields}
                    values={customFieldValues}
                    onChange={setCustomFieldValues}
                    disabled={createMutation.isPending}
                  />
                </div>
              )}
              {formError && (
                <Alert variant="destructive">
                  <AlertDescription>{formError}</AlertDescription>
                </Alert>
              )}
              <DialogFooter className="items-center sm:justify-between">
                <Label className="flex cursor-pointer items-center gap-2 text-sm font-normal text-muted-foreground">
                  <Checkbox
                    checked={createMore}
                    onCheckedChange={(v) => setCreateMore(v === true)}
                  />
                  Create more
                </Label>
                <div className="flex gap-2">
                  <Button
                    type="button"
                    variant="outline"
                    disabled={createMutation.isPending || !name.trim()}
                    onClick={onSaveDraft}
                    title="Save as an unfinished draft — it won't appear in the working set until published"
                  >
                    {createMutation.isPending ? "Saving…" : "Save as draft"}
                  </Button>
                  <Button
                    type="submit"
                    disabled={createMutation.isPending || !name.trim()}
                  >
                    {createMutation.isPending ? "Creating…" : "Create issue"}
                  </Button>
                </div>
              </DialogFooter>
            </form>
        </DialogContent>
        </Dialog>

        {/* Filter toolbar — 30px controls on the token ramp. */}
        <div className="mb-4 flex flex-wrap items-center gap-2">
          <form onSubmit={onSearchSubmit} className="relative">
            <Search className="absolute left-2 top-[8px] h-3.5 w-3.5 text-ink-muted" />
            <Input
              ref={searchRef}
              value={searchInput}
              onChange={(e) => setSearchInput(e.target.value)}
              placeholder="Search issues…  ( / )"
              className="h-[30px] w-56 pl-8 text-[11px]"
            />
          </form>
          {hasActiveFilters && (
            <Button
              variant="ghost"
              size="sm"
              onClick={() => {
                clearFilters();
                setSearchInput("");
              }}
            >
              Clear
            </Button>
          )}
          {/* C8T3: export the current filtered set as CSV/JSON */}
          <div className="ml-auto flex items-center gap-2">
            <Select
              value={exportFormat}
              onValueChange={(v) => setExportFormat(v as ExportFormat)}
            >
              <SelectTrigger
              className="h-[30px] w-24 text-[11px]"
              aria-label="Export format"
            >
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="csv">CSV</SelectItem>
                <SelectItem value="json">JSON</SelectItem>
              </SelectContent>
            </Select>
            <Button
              variant="outline"
              onClick={onExport}
              disabled={exporting}
              className="h-[30px] text-[10px]"
            >
              <Download className="mr-1 h-3.5 w-3.5" />
              {exporting ? "Exporting…" : "Export"}
            </Button>
          </div>
          {exportError && (
            <span className="w-full text-sm text-destructive">{exportError}</span>
          )}
      </div>

      {issuesQuery.isError && (
        <Alert variant="destructive" className="mb-4">
          <AlertDescription>
            {issuesQuery.error instanceof ApiError
              ? issuesQuery.error.message
              : "Failed to load issues"}
          </AlertDescription>
        </Alert>
      )}

      {bulk.count > 0 && (
        <BulkActionBar
          states={states}
          members={membersQuery.data ?? null}
          labels={labelsQuery.data ?? null}
          selectedCount={bulk.count}
          loadedCount={issues.length}
          allLoadedSelected={allLoadedSelected}
          onToggleSelectAll={toggleSelectAllLoaded}
          onClearSelection={bulk.clear}
          onApply={(set) => bulkMutation.mutate(set)}
          applying={bulkMutation.isPending}
          error={bulkError}
          cycles={cyclesQuery.data ?? null}
          modules={modulesQuery.data ?? null}
          structureLoading={cyclesQuery.isPending || modulesQuery.isPending}
          onAddToCycle={(id) => bulkCycleMutation.mutate(id)}
          onAddToModule={(id) => bulkModuleMutation.mutate(id)}
          organizeApplying={
            bulkCycleMutation.isPending || bulkModuleMutation.isPending
          }
          organizeError={membershipError}
        />
      )}

      {issuesQuery.isPending ? (
        <div aria-hidden>
          {[0, 1, 2].map((i) => (
            <Skeleton
              key={i}
              className="h-[47px] w-full rounded-none border-b border-border-hairline"
            />
          ))}
        </div>
      ) : issues.length === 0 && !groupingActive ? (
        <Card>
          <CardHeader>
            <CardTitle>
              {hasActiveFilters ? "No issues match" : "No issues yet"}
            </CardTitle>
            <CardDescription>
              {hasActiveFilters
                ? "Try clearing the filters."
                : "Create the first issue in this project."}
            </CardDescription>
          </CardHeader>
        </Card>
      ) : (
        <>
          {groupingActive ? (
            <div>
              {groups.map((g, gi) => (
                <section
                  key={g.key}
                  aria-label={g.title}
                  className={gi > 0 ? "mt-6" : undefined}
                >
                  {/* C19T5: group header — 35px, 12px/600, bottom hairline,
                      state-dot (state groups) or priority bars (priority
                      groups) + mono count. */}
                  <div className="flex h-[35px] items-center gap-2 border-b border-border-hairline">
                    {g.stateGroup ? (
                      <span
                        className={`state-dot ${stateDotClass(g.stateGroup)}`}
                        aria-hidden
                      />
                    ) : g.priority !== undefined ? (
                      <PriorityBars priority={g.priority} />
                    ) : null}
                    <h2 className="text-[12px] font-semibold text-ink">
                      {g.title}
                    </h2>
                    <span className="font-mono text-[10px] text-ink-muted">
                      {g.issues.length}
                    </span>
                  </div>
                  <div>
                    {g.issues.map((issue) => (
                      <IssueRow
                        key={issue.id}
                        issue={issue}
                        state={stateById.get(issue.state_id)}
                        fields={settings.fields}
                        onOpen={(it) => openPeek(it.id)}
                        kbdSelected={indexById.get(issue.id) === selected}
                        bulkSelected={bulk.isSelected(issue.id)}
                        bulkActive={bulk.count > 0}
                        onToggleSelect={() => bulk.toggle(issue.id)}
                      />
                    ))}
                  </div>
                  <QuickAdd
                    className="mt-1"
                    onCreate={(name) =>
                      quickCreate(g.stateId ?? "", name, g.priority)
                    }
                    onExpand={(name) =>
                      expandQuickAdd(g.stateId ?? "", name, g.priority)
                    }
                  />
                </section>
              ))}
            </div>
          ) : (
            <div>
              {issues.map((issue, i) => (
                <IssueRow
                  key={issue.id}
                  issue={issue}
                  state={stateById.get(issue.state_id)}
                  fields={settings.fields}
                  onOpen={(it) => openPeek(it.id)}
                  kbdSelected={i === selected}
                  bulkSelected={bulk.isSelected(issue.id)}
                  bulkActive={bulk.count > 0}
                  onToggleSelect={() => bulk.toggle(issue.id)}
                />
              ))}
              {filters.state && (
                <QuickAdd
                  className="mt-1"
                  onCreate={(name) => quickCreate(filters.state, name)}
                  onExpand={(name) => expandQuickAdd(filters.state, name)}
                />
              )}
            </div>
          )}
          {issuesQuery.hasNextPage && (
            <div className="mt-4 flex justify-center">
              <Button
                variant="outline"
                onClick={() => void issuesQuery.fetchNextPage()}
                disabled={issuesQuery.isFetchingNextPage}
              >
                {issuesQuery.isFetchingNextPage ? "Loading…" : "Load more"}
              </Button>
            </div>
          )}
        </>
      )}

      {peekUuid && (
        <PeekDrawer
          slug={slug}
          identifier={identifier}
          uuid={peekUuid}
          onClose={closePeek}
        />
      )}
      </div>
    </div>
  );
}
