import { Fragment, useEffect, useMemo, useRef, useState, type FormEvent, type ReactNode } from "react";
import { useLocation, useNavigate, useParams, useSearchParams } from "react-router-dom";
import {
  useInfiniteQuery,
  useMutation,
  useQuery,
  useQueryClient,
  type InfiniteData,
} from "@tanstack/react-query";
import { Download, Search } from "lucide-react";
import { api, ApiError } from "../lib/api";
import { stateTransitionConflictMessage } from "../lib/workflow";
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
import SortableIssueRow from "../components/issue/SortableIssueRow";
import ListRowStack from "../components/issue/ListRowStack";
import ListDragRoot from "../components/issue/ListDragRoot";
import ListHeaderDropZone from "../components/issue/ListHeaderDropZone";
import {
  applyListDrop,
  isListDragEnabled,
} from "../components/issue/listReorder";
import {
  dispatchListDragEnd,
  executeListHeaderDrop,
  headerDropId,
  isListReassignEnabled,
  listSortableId,
  resolveListHeaderDrop,
  toHeaderTarget,
  type HeaderTargetFields,
  type ListDragRowRef,
  type ListDragStackRef,
  type ListHeaderDropInfo,
} from "../components/issue/listHeaderDrop";
import NestedListSubtaskRow from "../components/issue/NestedListSubtaskRow";
import DuplicateSuggestions from "../components/issue/DuplicateSuggestions";
import PeekDrawer from "../components/issue/PeekDrawer";
import { usePeekParam } from "../components/issue/usePeek";
import PriorityPicker from "../components/issue/PriorityPicker";
import ParentPicker from "../components/issue/ParentPicker";
import QuickAdd from "../components/issue/QuickAdd";
import DraftWithAI from "../components/issue/DraftWithAI";
import BulkActionBar from "../components/issue/BulkActionBar";
import { BulkMoveDialog } from "../components/issue/MoveIssueDialog";
import {
  moveIssuePath,
  type MoveIssueResult,
} from "../components/issue/useMoveIssue";
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
import {
  buildSubGroupSections,
  effectiveSubGroupBy,
  type SubGroupSection,
} from "../components/issue/subGroup";
import { groupIssuesByEpic } from "../components/issue/groupByEpic";
import { groupIssuesByAssignee } from "../components/issue/groupByAssignee";
import { splitNestedIssues } from "../components/issue/splitNestedIssues";
import EpicGroupBreakdown from "../components/epics/EpicGroupBreakdown";
import FilterPanel from "../components/issue/FilterPanel";
import SavedViewsMenu from "../components/issue/SavedViewsMenu";
import PageHeader, {
  pageHeaderControlClass,
} from "../components/shell/PageHeader";
import {
  activeFilterCount,
  EMPTY_FILTERS,
  toApiParams,
  useIssueFilters,
  type IssueFilters,
} from "../lib/filters";
import { buildPqlRequestParams, pqlHasLimit } from "../lib/pql";
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
  includeSubtasks = false,
): string {
  // C91T1 exclusivity: a non-blank PQL query replaces the visual
  // filters entirely — the request carries ONLY pql (+ pagination /
  // ordering). The backend enforces the same rule server-side, but
  // the frontend must not send a two-mode request in the first place.
  if (f.pql?.trim()) {
    const p = buildPqlRequestParams(f.pql, { orderBy, perPage: PER_PAGE, cursor });
    // C121T1: batched project-structure chips on list rows (C21T2).
    p.set("include_project_structure", "1");
    // C124T0: nested sub-work rows — only when the toggle is on;
    // off means today's flat fetch exactly, no unused data.
    if (includeSubtasks) p.set("include_subtasks", "1");
    return `/api/v1/workspaces/${encodeURIComponent(slug)}/projects/${encodeURIComponent(identifier)}/issues?${p}`;
  }
  const p = toApiParams(f);
  p.set("per_page", String(PER_PAGE));
  p.set("order_by", orderBy);
  // C121T1: batched project-structure chips on list rows (C21T2).
  p.set("include_project_structure", "1");
  // C124T0: same toggle-gated flag as the PQL branch.
  if (includeSubtasks) p.set("include_subtasks", "1");
  if (cursor) p.set("cursor", cursor);
  return `/api/v1/workspaces/${encodeURIComponent(slug)}/projects/${encodeURIComponent(identifier)}/issues?${p}`;
}

/**
 * C127T0: one render path for a group/sub-group header's outer div.
 * With a droppable id the header is a ListHeaderDropZone (the zone
 * appends the idle/hot border token itself); without one — flat
 * layout, PQL mode, the state-Ungrouped sub-header — it is the plain
 * div the list has always rendered. `className` carries the header's
 * classes WITHOUT the border-color token in both branches, so the idle
 * DOM is identical either way. A plain helper function, not a
 * component, so the header element type never remounts spuriously.
 */
function listHeaderOuter(
  droppableId: string | null,
  className: string,
  inner: ReactNode,
): ReactNode {
  if (droppableId === null) {
    return (
      <div className={`${className} border-border-hairline`}>{inner}</div>
    );
  }
  return (
    <ListHeaderDropZone droppableId={droppableId} className={className}>
      {inner}
    </ListHeaderDropZone>
  );
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
  // C91T1 PQL mode. MODE-RESTORE DECISION (documented choice): the
  // visual selection is preserved IN MEMORY, not discarded — entering
  // PQL mode snapshots the current filters (including the search
  // text) into parkedFiltersRef and clears them from the URL, so the
  // list request can never mix modes; switching back to Filters
  // restores the snapshot exactly. No confirm dialog: switching modes
  // never loses anything, so a confirm would be pure friction. The
  // snapshot is in-memory only — a reload keeps the applied PQL query
  // (it lives in the URL) but not the parked visual selection.
  const [pqlMode, setPqlMode] = useState(filters.pql?.trim() ? true : false);
  const parkedFiltersRef = useRef<IssueFilters | null>(null);
  // Backstop for mode sync: a non-blank ?pql= in the URL (pasted link,
  // back/forward, view apply) always means PQL mode. Mode is NEVER
  // auto-cleared here — leaving PQL mode happens only via an explicit
  // toggle to Filters or applying a visual saved view, because PQL
  // mode with an empty (cleared) query is a real state: no filter.
  useEffect(() => {
    if (filters.pql?.trim()) setPqlMode(true);
  }, [filters.pql]);
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

  // C91T1 PQL mode handlers (see the mode-restore note at the pqlMode
  // state above). The applied query lives in the URL (?pql=) via the
  // shared filter state; the mode flag itself is page state.
  function handlePqlModeChange(mode: "visual" | "pql") {
    if (mode === "pql") {
      if (!pqlMode) {
        // Park the visual selection (pql stripped — the parked set is
        // visual-only) and clear the URL so no request can mix modes.
        parkedFiltersRef.current = { ...filters, pql: "" };
        setFilters({ ...EMPTY_FILTERS, pql: "" });
        setSearchInput("");
      }
      setPqlMode(true);
      return;
    }
    const parked = parkedFiltersRef.current;
    parkedFiltersRef.current = null;
    setPqlMode(false);
    if (parked) {
      setFilters({ ...parked, pql: "" });
      setSearchInput(parked.q);
    } else {
      setFilters({ pql: "" });
    }
  }

  /**
   * Probe-first PQL apply (prototype spec): fetch one page with the
   * pql-only params; commit to the URL only when the server accepts
   * the query. A 400 rejects, so the list query never changes, the
   * list data stays on screen, and the editor shows the server's
   * message + column inline. Empty query = no filter (commits "" and
   * stays in PQL mode).
   */
  async function handlePqlApply(query: string): Promise<void> {
    const q = query.trim();
    if (!q) {
      setFilters({ pql: "" });
      return;
    }
    await api.get<IssueListResult>(
      buildIssuePath(slug, identifier, { ...EMPTY_FILTERS, pql: q }, settings.orderBy),
    );
    setFilters({ pql: q });
  }

  function handlePqlClear() {
    setFilters({ pql: "" });
  }

  /**
   * Saved-view application (C91T1): the menu already wrote the view's
   * filters to the URL; this only settles the mode. A PQL view enters
   * PQL mode; a visual view leaves it. Either way the parked visual
   * selection is dropped — the view, not the pre-view selection, is
   * now the state.
   */
  function handleSavedViewApplied(pqlQuery: string | null) {
    parkedFiltersRef.current = null;
    setPqlMode(pqlQuery !== null);
  }

  // Opened from the command palette ("Create issue" action) or from the
  // board's inline quick-add expand button (prefills title + state, and
  // priority when the board is grouped by priority) — or from the
  // calendar's day-cell quick-add (C26T3: prefills the target/due date).
  useEffect(() => {
    const st = location.state as {
      newIssue?: boolean;
      newIssueName?: string;
      newIssueStateId?: string;
      newIssuePriority?: number;
      newIssueStartDate?: string;
      newIssueTargetDate?: string;
    } | null;
    if (st?.newIssue) {
      if (typeof st.newIssueName === "string") setName(st.newIssueName);
      if (typeof st.newIssueStateId === "string")
        setNewStateId(st.newIssueStateId);
      if (typeof st.newIssuePriority === "number")
        setNewPriority(st.newIssuePriority);
      if (typeof st.newIssueStartDate === "string")
        setNewStartDate(st.newIssueStartDate);
      if (typeof st.newIssueTargetDate === "string")
        setNewTargetDate(st.newIssueTargetDate);
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
  // C129T0: assignee carried from an assignee group's expand-to-full-
  // form (the create dialog has no assignee field, so the member is
  // attached right after a successful create — see createMutation's
  // onSuccess). A ref, not state: onSuccess must read the value from
  // the render that opened the dialog, and the dialog-close handler
  // clears it on cancel so it never leaks into the next create.
  const pendingAssigneeRef = useRef<string | null>(null);
  // C26T3: YYYY-MM-DD dates for the create form — prefilled by the
  // calendar's day-cell quick-add via location.state.
  const [newStartDate, setNewStartDate] = useState("");
  const [newTargetDate, setNewTargetDate] = useState("");
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
    // C124T0: the toggle is part of the key too — flipping it
    // refetches with / without include_subtasks=1 instead of
    // reusing stale rows (Board precedent, C122T1).
    queryKey: [
      "issues",
      slug,
      identifier,
      filters,
      settings.orderBy,
      settings.showSubWorkItems,
    ],
    queryFn: ({ pageParam }: { pageParam?: string }) =>
      api.get<IssueListResult>(
        buildIssuePath(
          slug,
          identifier,
          filters,
          settings.orderBy,
          pageParam,
          settings.showSubWorkItems,
        ),
      ),
    // C98T1: a LIMIT query is a single page — never fetch a next
    // page for it, even if a cursor were ever returned (the server
    // already nulls next_cursor under LIMIT; this is the client-side
    // half of the single-fetch contract).
    getNextPageParam: (last) =>
      pqlHasLimit(filters.pql ?? "") ? undefined : last.next_cursor || undefined,
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
      // C68T1: workflow transition controls — a bulk set was denied by the
      // project's workflow rules; name the permitted next states.
      const conflict = stateTransitionConflictMessage(
        e,
        new Map((statesQuery.data ?? []).map((s) => [s.id, s.name] as const)),
      );
      if (conflict) {
        toast.add({ title: "Move blocked", description: conflict, type: "error" });
        return;
      }
      setBulkError(e instanceof ApiError ? e.message : "Bulk update failed");
    },
  });

  // C24T0 bulk move: sequential POST .../issues/{uuid}/move calls with a
  // partial-success report (moved n, failed m with reasons).
  const [bulkMoveOpen, setBulkMoveOpen] = useState(false);
  const [bulkMoving, setBulkMoving] = useState(false);

  async function bulkMoveToProject(targetIdentifier: string) {
    const ids = bulk.selectedIds;
    if (ids.length === 0 || bulkMoving) return;
    setBulkMoving(true);
    const displayOf = (id: string) =>
      issues.find((i) => i.id === id)?.display_id ?? id.slice(0, 8);
    let moved = 0;
    const failures: { display: string; reason: string }[] = [];
    for (const id of ids) {
      try {
        await api.post<MoveIssueResult>(
          moveIssuePath(slug, identifier, id),
          { target_project_identifier: targetIdentifier },
        );
        moved++;
      } catch (e) {
        failures.push({
          display: displayOf(id),
          reason: e instanceof Error ? e.message : "failed",
        });
      }
    }
    setBulkMoving(false);
    setBulkMoveOpen(false);
    bulk.clear();
    void queryClient.invalidateQueries({ queryKey: ["issues", slug, identifier] });
    if (failures.length === 0) {
      toast.add({
        title: `Moved ${moved} issue${moved === 1 ? "" : "s"} to ${targetIdentifier}`,
        type: "success",
      });
    } else {
      toast.add({
        title: `Moved ${moved} of ${ids.length} issues`,
        description: failures
          .map((f) => `${f.display}: ${f.reason}`)
          .join("; "),
        type: "error",
      });
    }
  }

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
      start_date?: string;
      target_date?: string;
      is_draft?: boolean;
      custom_values?: Record<string, CustomValueInput>;
    }) => api.post<Issue>(`${base}/issues`, body),
    onSuccess: (issue) => {
      // C129T0: an expand-to-full-form from an assignee group carries
      // the group member here — captured and cleared immediately so
      // no later create can reuse it — since the dialog has no
      // assignee field, the member is attached right after create.
      const pendingAssigneeId = pendingAssigneeRef.current;
      pendingAssigneeRef.current = null;
      resetCreateForm();
      // "Create more" keeps the dialog open for rapid entry; otherwise
      // the dialog closes and the toast is the confirmation.
      if (!createMore) setDialogOpen(false);
      void queryClient.invalidateQueries({
        queryKey: ["issues", slug, identifier],
      });
      // C26T3: the calendar buckets by target/start date — a created
      // issue must appear on its day without a manual refresh.
      void queryClient.invalidateQueries({
        queryKey: ["calendar", slug, identifier],
      });
      showCreatedToast(issue);
      if (pendingAssigneeId) {
        // Attach failure surfaces honestly and the row lands in
        // Ungrouped after invalidation (same rule as list quick-add).
        void api
          .post(
            `${base}/issues/${issue.id}/assignees/${encodeURIComponent(pendingAssigneeId)}`,
          )
          .then(() =>
            queryClient.invalidateQueries({
              queryKey: ["issues", slug, identifier],
            }),
          )
          .catch((e) => {
            toast.add({
              title: "Issue created but not assigned",
              type: "error",
              description:
                e instanceof Error ? e.message : "Failed to assign issue",
            });
            void queryClient.invalidateQueries({
              queryKey: ["issues", slug, identifier],
            });
          });
      }
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
    setNewStartDate("");
    setNewTargetDate("");
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
      // C26T3: dates ride along when set (calendar quick-add prefills
      // the due date); the server 400s an invalid range, surfaced below.
      ...(newStartDate ? { start_date: newStartDate } : {}),
      ...(newTargetDate ? { target_date: newTargetDate } : {}),
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
      ...(newStartDate ? { start_date: newStartDate } : {}),
      ...(newTargetDate ? { target_date: newTargetDate } : {}),
      is_draft: true,
      ...(Object.keys(customValues).length > 0
        ? { custom_values: customValues }
        : {}),
    });
  }

  /** Inline quick-add: title only. The new row appears via query
   *  invalidation — no full reload. Under assignee grouping, a group
   *  member attaches after create (C129T0, board epic quick-add
   *  precedent): an attach failure surfaces (QuickAdd shows the
   *  thrown error) and the row lands in Ungrouped after invalidation.
   *  Ungrouped quick-add (no member) is a plain create. */
  async function quickCreate(
    stateId: string,
    name: string,
    priority?: number,
    assigneeId?: string,
  ): Promise<void> {
    const created = await api.post<Issue>(`${base}/issues`, {
      name,
      ...(stateId ? { state_id: stateId } : {}),
      ...(priority !== undefined ? { priority } : {}),
    });
    if (assigneeId) {
      try {
        await api.post(
          `${base}/issues/${created.id}/assignees/${encodeURIComponent(assigneeId)}`,
        );
      } catch (e) {
        await queryClient.invalidateQueries({
          queryKey: ["issues", slug, identifier],
        });
        throw e;
      }
    }
    await queryClient.invalidateQueries({
      queryKey: ["issues", slug, identifier],
    });
  }

  /** Expand an inline quick-add into the full create form, keeping the
   *  typed title, the group's state/priority, and — under assignee
   *  grouping — the group member (attached after create, since the
   *  dialog has no assignee field; see createMutation's onSuccess). */
  function expandQuickAdd(
    stateId: string,
    name: string,
    priority?: number,
    assigneeId?: string,
  ) {
    setName(name);
    setNewStateId(stateId);
    if (priority !== undefined) setNewPriority(priority);
    pendingAssigneeRef.current = assigneeId ?? null;
    setFormError(null);
    setDialogOpen(true);
  }

  const fetchedIssues =
    issuesQuery.data?.pages.flatMap((p) => p.results) ?? [];
  // C124T0: the split applies to the FULL fetched page set BEFORE
  // grouping. `issues` below is the post-split visible rows, so the
  // flat path, every primary group's `issues`, and every sub-group
  // section's `issues` (built from the groups) are visible rows only,
  // and group header counts reflect visible rows. No JSX render site
  // changes this task: the flat path, select-all, header count, and
  // orderedIssues all read `issues`, which is now the visible set.
  //
  // T1 CONTRACT (read this before touching the render sites):
  //   listSplit.nestedByParent: Map<string, SubtaskRow[]>
  // in this component's scope, computed from the FULL fetched set
  // (before group filtering), keyed by top-level parent issue id,
  // rows sorted by sort_order ASC, non-empty only when the toggle is
  // on. A parent appearing in several groups (epic multi-appearance)
  // looks its rows up by the same id under EACH appearance.
  // Toggle off: listSplit.visibleIssues === fetchedIssues and the
  // map is empty. T1 renders rows from this map under parent rows;
  // this task only computes it.
  const listSplit = useMemo(
    () => splitNestedIssues(fetchedIssues, settings.showSubWorkItems),
    [fetchedIssues, settings.showSubWorkItems],
  );
  const issues = listSplit.visibleIssues;
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
  // C72T0: epic grouping needs no skip — epic memberships ride on the
  // list rows themselves, so filters narrow the members, not the groups.
  const groupByEpic = settings.groupBy === "epic";
  // C129T0: assignee grouping needs no skip either — assignees ride on
  // the list rows themselves, exactly like epic memberships.
  const groupByAssignees = settings.groupBy === "assignees";
  const groupingActive =
    groupByState || groupByPriority || groupByEpic || groupByAssignees;

  interface IssueGroup {
    key: string;
    title: string;
    color?: string;
    /** Workflow group for the header state-dot (state grouping). */
    stateGroup?: string;
    /** C72T0: per-state-group member counts for the epic header progress
     *  bar (epic grouping only). */
    byGroup?: Record<string, number>;
    issues: Issue[];
    stateId?: string;
    priority?: number;
    /** C127T0: explicit drop-target values for this group's header
     *  (see listHeaderDrop.toHeaderTarget). Epic groups carry NO epic
     *  id field of their own — the key IS the epic id verbatim, so
     *  the mapping from key → target field happens once here, at
     *  group build time, never by parsing a key at drop time. */
    targetStateId?: string;
    targetPriority?: number;
    targetEpicId?: string;
    isUngroupedEpic?: boolean;
    /** C129T0: assignee drop-target values, mirroring the epic pair
     *  above — the key IS the assignee id verbatim for member groups. */
    targetAssigneeId?: string;
    isUngroupedAssignee?: boolean;
  }

  // C124T0: `issues` here is already the post-split visible rows
  // (split above, before grouping), so each group's `issues` — and
  // the sub-group sections built from them below — are visible rows.
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
        targetStateId: s.id,
      }));
    } else if (groupByPriority) {
      gs = [4, 3, 2, 1, 0].map((p) => ({
        key: `priority:${p}`,
        title: priorityLabel(p),
        issues: issues.filter((i) => i.priority === p),
        stateId: sortedStates[0]?.id,
        priority: p,
        targetPriority: p,
      }));
    } else if (groupByEpic) {
      // C72T0: group by LIVE epic membership (see groupByEpic.ts for the
      // multi-appearance + archived-exclusion + Ungrouped-last semantics).
      // Progress counts come from the loaded states — no extra fetches.
      gs = groupIssuesByEpic(
        issues,
        (i) => stateById.get(i.state_id)?.group,
      ).map((g) => ({
        key: g.key,
        title: g.title,
        issues: g.issues,
        byGroup: g.byGroup,
        stateId: sortedStates[0]?.id,
        targetEpicId: g.isUngrouped ? undefined : g.key,
        isUngroupedEpic: g.isUngrouped,
      }));
    } else if (groupByAssignees) {
      // C129T0: group by assignee membership (see groupByAssignee.ts
      // for the multi-appearance + Ungrouped-last semantics). No
      // breakdown bar: the helper carries no byGroup (unlike epic
      // groups), and the header renders the assignee name from the
      // row — the same name the row chips use — plus the count.
      gs = groupIssuesByAssignee(issues).map((g) => ({
        key: g.key,
        title: g.title,
        issues: g.issues,
        stateId: sortedStates[0]?.id,
        targetAssigneeId: g.isUngrouped ? undefined : g.key,
        isUngroupedAssignee: g.isUngrouped,
      }));
    }
    return settings.showEmptyGroups
      ? gs
      : gs.filter((g) => g.issues.length > 0);
  }, [
    groupByState,
    groupByPriority,
    groupByEpic,
    groupByAssignees,
    sortedStates,
    issues,
    settings.showEmptyGroups,
  ]);
  // C123T1: sub-group dimension. The effective rule (never equals the
  // active primary dimension; groupBy "none" → flat) lives in the shared
  // helper; sub-grouping applies only to primary groups actually
  // rendered, so the existing group-skip rules (single-state /
  // single-priority filter) gate it via groupingActive. When the
  // effective sub-group is "none", the layout below is today's exactly.
  const effectiveSubGroup = effectiveSubGroupBy(settings);
  const subGroupingActive = groupingActive && effectiveSubGroup !== "none";
  const subSectionsByGroup = useMemo(() => {
    if (!subGroupingActive) return null;
    const m = new Map<string, SubGroupSection[]>();
    for (const g of groups) {
      const secs = buildSubGroupSections(g.issues, effectiveSubGroup, {
        states: sortedStates,
        stateById,
      });
      // Show-empty filtering for sub-sections follows showEmptyGroups,
      // same as primary groups.
      m.set(
        g.key,
        settings.showEmptyGroups
          ? secs
          : secs.filter((s) => s.issues.length > 0),
      );
    }
    return m;
  }, [
    subGroupingActive,
    groups,
    effectiveSubGroup,
    sortedStates,
    stateById,
    settings.showEmptyGroups,
  ]);
  // Flattened order = primary-group order, then sub-section order
  // within each group — sub-sections only re-order the visual stacking
  // of the same rows, so selection (j/k), peek, and counts are
  // unchanged. (Epic sub-groups multi-appear, exactly like primary
  // epic groups, so orderedIssues may list an issue more than once.)
  const orderedIssues = !groupingActive
    ? issues
    : subSectionsByGroup
      ? groups.flatMap((g) =>
          (subSectionsByGroup.get(g.key) ?? []).flatMap((s) => s.issues),
        )
      : groups.flatMap((g) => g.issues);
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

  // C126T0: list drag-to-reorder. The effective grouping is what
  // renders (a grouping skipped by its single-value filter renders
  // flat), so the enable rule reads groupByState/groupByPriority/
  // groupByEpic/groupByAssignees, not the raw setting. The drop pipeline lives in
  // listReorder.ts (board's applyDrop precedent); this only wires it
  // to api/queryClient. PATCH carries ONLY {sort_order} — a list
  // reorder never reassigns state. Pagination caveat (plan): drop
  // targets are the currently loaded rows only; after a drop the
  // invalidation refetches the loaded pages, so a drop at the bottom
  // of a partially-loaded list may shift on refetch among unloaded
  // values — no fetch-all-pages workaround by design.
  const effectiveGroupBy = groupByAssignees
    ? "assignees"
    : groupByEpic
      ? "epic"
      : groupByPriority
        ? "priority"
        : groupByState
          ? "state"
          : "none";
  const dragEnabled = isListDragEnabled({
    orderBy: settings.orderBy,
    pqlMode,
    groupBy: effectiveGroupBy,
  });
  const [reorderError, setReorderError] = useState<string | null>(null);
  const issuesExactKey = [
    "issues",
    slug,
    identifier,
    filters,
    settings.orderBy,
    settings.showSubWorkItems,
  ] as const;

  async function handleListDrop(
    stack: Issue[],
    activeId: string,
    overId: string,
  ) {
    setReorderError(null);
    await applyListDrop(stack, activeId, overId, {
      getData: () =>
        queryClient.getQueryData<InfiniteData<IssueListResult>>(
          issuesExactKey,
        ),
      setData: (d) => queryClient.setQueryData(issuesExactKey, d),
      patch: (id, body) => api.patch(`${base}/issues/${id}`, body),
      rebalance: (body) => api.post(`${base}/issues/rebalance`, body),
      refetchFlat: async () => {
        await queryClient.invalidateQueries({
          queryKey: ["issues", slug, identifier],
        });
        const r = await issuesQuery.refetch();
        return r.data?.pages.flatMap((p) => p.results) ?? [];
      },
      invalidate: () =>
        queryClient.invalidateQueries({
          queryKey: ["issues", slug, identifier],
        }),
      onError: (m) => setReorderError(m),
    });
  }

  // C127T0: reassign drag — a SECOND drag mode, independent of C126's
  // reorder gate: enabled whenever grouping is active and not PQL
  // mode, under ANY orderBy (a reassignment persists regardless of
  // display order; the appended sort_order only affects Manual-rank
  // display). Since C128 both drags coexist under priority/epic
  // grouping too (row drops reorder within their stack, header drops
  // reassign), exactly as they already did under state grouping.
  // One DndContext owns the whole list
  // (ListDragRoot) — per-stack contexts could not receive cross-group
  // header drops — and dispatch, not context isolation, keeps row
  // drops stack-local. A drag is active when either mode holds.
  const reassignEnabled = isListReassignEnabled({
    pqlMode,
    groupingActive,
  });
  const dndActive = dragEnabled || reassignEnabled;

  // C127T0: stack-scoped sortable ids + section-scoped header
  // droppable ids, built once per render from the same group/section
  // objects the layout renders, so drop dispatch reads lookup maps,
  // never parses an id string. The epic appearance a stack's rows
  // were dragged from is recorded per stack: the primary epic group,
  // else the epic sub-section, else none (that source decides the
  // remove side of an epic move — see listHeaderDrop). C129T0: the
  // assignee appearance mirrors it — the primary assignee group,
  // else none (sub-sections are never assignee this cycle).
  const listDragMaps = useMemo(() => {
    const sortableMap = new Map<string, ListDragRowRef>();
    const headerMap = new Map<string, ListHeaderDropInfo>();
    const registerStack = (
      stackKey: string,
      rows: Issue[],
      sourceEpicId: string | null,
      sourceAssigneeId: string | null = null,
    ): ListDragStackRef => {
      const stack: ListDragStackRef = {
        stackKey,
        rows,
        sourceEpicId,
        sourceAssigneeId,
      };
      for (const issue of rows) {
        sortableMap.set(listSortableId(stackKey, issue.id), {
          rowId: issue.id,
          issue,
          stack,
        });
      }
      return stack;
    };
    const registerHeader = (
      sectionKey: string,
      fields: HeaderTargetFields,
      rows: Issue[],
    ) => {
      const target = toHeaderTarget(fields);
      if (target) {
        headerMap.set(headerDropId(sectionKey, target), {
          target,
          targetRows: rows,
        });
      }
    };
    if (!groupingActive) {
      registerStack("flat", issues, null);
    } else {
      for (const g of groups) {
        const primaryEpicId = groupByEpic
          ? g.isUngroupedEpic
            ? null
            : (g.targetEpicId ?? null)
          : null;
        const primaryAssigneeId = groupByAssignees
          ? g.isUngroupedAssignee
            ? null
            : (g.targetAssigneeId ?? null)
          : null;
        if (subSectionsByGroup) {
          registerHeader(g.key, g, g.issues);
          for (const sub of subSectionsByGroup.get(g.key) ?? []) {
            const subKey = `${g.key}::${sub.key}`;
            const sourceEpicId =
              primaryEpicId ??
              (effectiveSubGroup === "epic"
                ? sub.isUngroupedEpic
                  ? null
                  : (sub.targetEpicId ?? null)
                : null);
            // Sub-sections are never assignee this cycle, so the
            // source is the primary assignee group, else none.
            registerStack(subKey, sub.issues, sourceEpicId, primaryAssigneeId);
            registerHeader(subKey, sub, sub.issues);
          }
        } else {
          registerStack(g.key, g.issues, primaryEpicId, primaryAssigneeId);
          registerHeader(g.key, g, g.issues);
        }
      }
    }
    return { sortableMap, headerMap };
  }, [
    groupingActive,
    groups,
    subSectionsByGroup,
    issues,
    groupByEpic,
    groupByAssignees,
    effectiveSubGroup,
  ]);

  // C127T0: one header drop — resolve to the exact call sequence,
  // execute it (add-before-remove + sort PATCH for epic/assignee
  // moves), and invalidate so the moved row appears in its new group
  // from the server's state. No optimistic group-move rendering (the
  // simpler correct choice the plan allows); on failure the row stays
  // visibly in its old group after refetch and the error surfaces
  // via the existing reorderError Alert.
  async function handleHeaderDrop(
    dragged: ListDragRowRef,
    info: ListHeaderDropInfo,
  ) {
    setReorderError(null);
    const plan = resolveListHeaderDrop({
      dragged: dragged.issue,
      sourceEpicId: dragged.stack.sourceEpicId,
      sourceAssigneeId: dragged.stack.sourceAssigneeId,
      target: info.target,
      targetRows: info.targetRows,
    });
    if (!plan) return; // own-group / already-member: silent no-op
    await executeListHeaderDrop(dragged.rowId, plan, {
      patch: (id, body) => api.patch(`${base}/issues/${id}`, body),
      addToEpic: (epicId, issueId) =>
        api.post(`${base}/epics/${encodeURIComponent(epicId)}/issues`, {
          issue_ids: [issueId],
        }),
      removeFromEpic: (epicId, issueId) =>
        api.del(`${base}/epics/${encodeURIComponent(epicId)}/issues`, {
          issue_ids: [issueId],
        }),
      // C129T0: assignee membership endpoints carry no body — the
      // user id rides the path, and both calls are idempotent.
      addToAssignee: (assigneeId, issueId) =>
        api.post(
          `${base}/issues/${issueId}/assignees/${encodeURIComponent(assigneeId)}`,
        ),
      removeFromAssignee: (assigneeId, issueId) =>
        api.del(
          `${base}/issues/${issueId}/assignees/${encodeURIComponent(assigneeId)}`,
        ),
      invalidate: () =>
        queryClient.invalidateQueries({
          queryKey: ["issues", slug, identifier],
        }),
      onError: (m) => setReorderError(m),
    });
  }

  // C127T0: one drag-end for the whole list — header ids route to
  // the reassignment pipeline, same-stack row ids to the C126 reorder
  // pipeline (only when its gate holds), everything else is a silent
  // no-op. See listHeaderDrop.dispatchListDragEnd.
  function handleListDragEnd(
    activeSortableId: string,
    overId: string | null,
  ) {
    dispatchListDragEnd({
      activeSortableId,
      overId,
      sortableMap: listDragMaps.sortableMap,
      headerMap: listDragMaps.headerMap,
      reorderEnabled: dragEnabled,
      onRowDrop: (stack, activeRowId, overRowId) =>
        void handleListDrop(stack, activeRowId, overRowId),
      onHeaderDrop: (ref, info) => void handleHeaderDrop(ref, info),
    });
  }

  /**
   * One list row + its nested subtask rows (C124), draggable when
   * either drag mode holds (C126 reorder and/or C127 reassign),
   * otherwise today's rendering exactly (a bare fragment adds no
   * DOM). Shared by all three render sites so the row props cannot
   * drift between flat/grouped/sub-grouped paths. `stackKey` is the
   * stack this appearance belongs to — it scopes the row's sortable
   * id, so a multi-appearance row never collides with itself.
   */
  function renderListRow(issue: Issue, kbd: boolean, stackKey: string) {
    const nestedRows = listSplit.nestedByParent.get(issue.id) ?? [];
    const nested =
      nestedRows.length > 0 ? (
        <div className="ml-7 flex flex-col border-l-2 border-border pl-3">
          {nestedRows.map((row) => (
            <NestedListSubtaskRow
              key={row.id}
              row={row}
              state={stateById.get(row.state_id ?? "")}
              onOpen={(id) => openPeek(id)}
            />
          ))}
        </div>
      ) : null;
    const rowProps = {
      issue,
      state: stateById.get(issue.state_id),
      fields: settings.fields,
      onOpen: (it: Issue) => openPeek(it.id),
      kbdSelected: kbd,
      bulkSelected: bulk.isSelected(issue.id),
      bulkActive: bulk.count > 0,
      onToggleSelect: () => bulk.toggle(issue.id),
    };
    if (!dndActive) {
      return (
        <>
          <IssueRow {...rowProps} />
          {nested}
        </>
      );
    }
    return (
      <SortableIssueRow
        {...rowProps}
        sortableId={listSortableId(stackKey, issue.id)}
      >
        {nested}
      </SortableIssueRow>
    );
  }

  /** Plain IssueRow face for a stack's drag overlay. */
  function renderOverlayRow(issue: Issue) {
    return (
      <IssueRow
        issue={issue}
        state={stateById.get(issue.state_id)}
        fields={settings.fields}
        onOpen={(it) => openPeek(it.id)}
        kbdSelected={indexById.get(issue.id) === selected}
        bulkSelected={bulk.isSelected(issue.id)}
        bulkActive={bulk.count > 0}
        onToggleSelect={() => bulk.toggle(issue.id)}
      />
    );
  }

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
            pqlMode={pqlMode}
            pqlAppliedQuery={filters.pql?.trim() ?? ""}
            onPqlModeChange={handlePqlModeChange}
            onPqlApply={handlePqlApply}
            onPqlClear={handlePqlClear}
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
            pqlMode={pqlMode}
            pqlQuery={filters.pql?.trim() ?? ""}
            onViewApplied={handleSavedViewApplied}
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
                // C129T0: same rule for a carried assignee group member.
                pendingAssigneeRef.current = null;
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
                {/* C26T3: start/due dates — the calendar's day-cell
                    quick-add prefills the due date via location.state. */}
                <div className="space-y-2">
                  <Label htmlFor="i-start">Start date</Label>
                  <Input
                    id="i-start"
                    type="date"
                    value={newStartDate}
                    onChange={(e) => setNewStartDate(e.target.value)}
                  />
                </div>
                <div className="space-y-2">
                  <Label htmlFor="i-target">Due date</Label>
                  <Input
                    id="i-target"
                    type="date"
                    value={newTargetDate}
                    onChange={(e) => setNewTargetDate(e.target.value)}
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
              placeholder={
                pqlMode
                  ? "Search is replaced by PQL mode"
                  : "Search issues…  ( / )"
              }
              disabled={pqlMode}
              title={
                pqlMode
                  ? "Free-text search is a visual filter — switch back to Filters to search"
                  : undefined
              }
              className="h-[30px] w-56 pl-8 text-[11px]"
            />
          </form>
          {hasActiveFilters && (
            <Button
              variant="ghost"
              size="sm"
              onClick={() => {
                // Clearing means clearing: the parked visual selection
                // is dropped too, so toggling back cannot resurrect it.
                parkedFiltersRef.current = null;
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
              disabled={exporting || pqlMode}
              title={
                pqlMode
                  ? "Export follows visual filters only — the export endpoint does not take a PQL query yet"
                  : undefined
              }
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

      {/* C126T0: reorder failures surface like other list errors (the
          bulkError-style Alert pattern) — no new toast system. */}
      {reorderError && (
        <Alert variant="destructive" className="mb-4">
          <AlertDescription>{reorderError}</AlertDescription>
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
          onMoveToProject={() => setBulkMoveOpen(true)}
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
      <BulkMoveDialog
        open={bulkMoveOpen}
        onOpenChange={setBulkMoveOpen}
        slug={slug}
        sourceIdentifier={identifier}
        count={bulk.count}
        onConfirm={(t) => void bulkMoveToProject(t)}
        confirming={bulkMoving}
      />

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
          <ListDragRoot
            enabled={dndActive}
            onDragStart={() => setReorderError(null)}
            onDragEnd={handleListDragEnd}
            overlayIssueForId={(id) =>
              listDragMaps.sortableMap.get(id)?.issue
            }
            renderOverlayRow={renderOverlayRow}
          >
          {groupingActive ? (
            <div>
              {groups.map((g, gi) => {
                // C127T0: the primary header is a drop target when
                // reassign drag holds and the group names an assignable
                // target — otherwise the plain header div, exactly as
                // before (see listHeaderOuter).
                const gTarget = toHeaderTarget(g);
                const gDropId =
                  reassignEnabled && gTarget
                    ? headerDropId(g.key, gTarget)
                    : null;
                return (
                <section
                  key={g.key}
                  aria-label={g.title}
                  className={gi > 0 ? "mt-6" : undefined}
                >
                  {/* C19T5: group header — 35px, 12px/600, bottom hairline,
                      state-dot (state groups) or priority bars (priority
                      groups) + mono count. */}
                  {listHeaderOuter(
                    gDropId,
                    "flex h-[35px] items-center gap-2 border-b",
                    <>
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
                    </>,
                  )}
                  {/* C72T0: epic group header progress — the shared
                      EpicGroupBreakdown (5 segments, no triage, done =
                      completed + cancelled), counts computed client-side
                      from the loaded members. */}
                  {g.byGroup && (
                    <div className="mb-1 mt-2 max-w-[480px]">
                      <EpicGroupBreakdown
                        byGroup={g.byGroup}
                        total={g.issues.length}
                        epicName={g.title}
                      />
                    </div>
                  )}
                  {/* C123T1: sub-group sections nest inside the primary
                      group (same group-header + row primitives, one
                      level deeper). When the effective sub-group is
                      "none" this is today's flat row stack. QuickAdd
                      stays once per primary group, after all
                      sub-sections — counts and quick-add targets are
                      unchanged. */}
                  {subSectionsByGroup ? (
                    <div>
                      {(subSectionsByGroup.get(g.key) ?? []).map((sub) => {
                        // C127T0: the sub-header is a drop target on
                        // the same terms as the primary header — except
                        // the state-Ungrouped sub-header, whose target
                        // is null (it names no assignable value) and
                        // always renders as the plain div.
                        const subKey = `${g.key}::${sub.key}`;
                        const subTarget = toHeaderTarget(sub);
                        const subDropId =
                          reassignEnabled && subTarget
                            ? headerDropId(subKey, subTarget)
                            : null;
                        return (
                        <div key={sub.key}>
                          {listHeaderOuter(
                            subDropId,
                            "flex h-[30px] items-center gap-2 border-b pl-4",
                            <>
                            {sub.stateGroup ? (
                              <span
                                className={`state-dot ${stateDotClass(sub.stateGroup)}`}
                                aria-hidden
                              />
                            ) : null}
                            <h3 className="text-[12px] font-medium text-ink">
                              {sub.title}
                            </h3>
                            <span className="font-mono text-[10px] text-ink-muted">
                              {sub.issues.length}
                            </span>
                            </>,
                          )}
                          {sub.byGroup && (
                            <div className="mb-1 mt-2 max-w-[480px] pl-4">
                              <EpicGroupBreakdown
                                byGroup={sub.byGroup}
                                total={sub.issues.length}
                                epicName={sub.title}
                              />
                            </div>
                          )}
                          {/* C127T0: one sortable context per sub-section
                              stack inside the list-wide DndContext
                              (ListDragRoot). Disabled: ListRowStack
                              renders only its children, so this is
                              today's DOM. */}
                          <ListRowStack
                            items={sub.issues.map((i) =>
                              listSortableId(subKey, i.id),
                            )}
                            draggable={dndActive}
                          >
                            <div>
                              {sub.issues.map((issue) => (
                                <Fragment key={issue.id}>
                                  {renderListRow(
                                    issue,
                                    indexById.get(issue.id) === selected,
                                    subKey,
                                  )}
                                </Fragment>
                              ))}
                            </div>
                          </ListRowStack>
                        </div>
                        );
                      })}
                    </div>
                  ) : (
                    <ListRowStack
                      items={g.issues.map((i) =>
                        listSortableId(g.key, i.id),
                      )}
                      draggable={dndActive}
                    >
                      <div>
                        {g.issues.map((issue) => (
                          <Fragment key={issue.id}>
                            {renderListRow(
                              issue,
                              indexById.get(issue.id) === selected,
                              g.key,
                            )}
                          </Fragment>
                        ))}
                      </div>
                    </ListRowStack>
                  )}
                  <QuickAdd
                    className="mt-1"
                    onCreate={(name) =>
                      quickCreate(
                        g.stateId ?? "",
                        name,
                        g.priority,
                        g.targetAssigneeId,
                      )
                    }
                    onExpand={(name) =>
                      expandQuickAdd(
                        g.stateId ?? "",
                        name,
                        g.priority,
                        g.targetAssigneeId,
                      )
                    }
                  />
                </section>
                );
              })}
            </div>
          ) : (
            <ListRowStack
              items={issues.map((i) => listSortableId("flat", i.id))}
              draggable={dndActive}
            >
              <div>
                {issues.map((issue, i) => (
                  <Fragment key={issue.id}>
                    {renderListRow(issue, i === selected, "flat")}
                  </Fragment>
                ))}
                {filters.state && (
                  <QuickAdd
                    className="mt-1"
                    onCreate={(name) => quickCreate(filters.state, name)}
                    onExpand={(name) => expandQuickAdd(filters.state, name)}
                  />
                )}
              </div>
            </ListRowStack>
          )}
          </ListDragRoot>
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
