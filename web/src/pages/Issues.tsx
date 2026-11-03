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
import { groupIssuesByLabel } from "../components/issue/groupByLabel";
import { groupIssuesByModule } from "../components/issue/groupByModule";
import { groupIssuesByCycle } from "../components/issue/groupByCycle";
import { groupIssuesByRelease } from "../components/issue/groupByRelease";
import {
  groupIssuesByCreatedBy,
  type CreatorLookupEntry,
} from "../components/issue/groupByCreatedBy";
import { groupIssuesByStateGroup } from "../components/issue/groupByStateGroup";
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
  // priority when the board is grouped by priority, and the member id
  // when grouped by assignees — attached after create, C129T1) — or from the
  // calendar's day-cell quick-add (C26T3: prefills the target/due date).
  useEffect(() => {
    const st = location.state as {
      newIssue?: boolean;
      newIssueName?: string;
      newIssueStateId?: string;
      newIssuePriority?: number;
      newIssueStartDate?: string;
      newIssueTargetDate?: string;
      newIssueAssigneeId?: string;
      newIssueLabelId?: string;
      newIssueModuleId?: string;
      newIssueCycleId?: string;
      newIssueReleaseId?: string;
    } | null;
    if (st?.newIssue) {
      if (typeof st.newIssueName === "string") setName(st.newIssueName);
      if (typeof st.newIssueStateId === "string")
        setNewStateId(st.newIssueStateId);
      if (typeof st.newIssuePriority === "number")
        setNewPriority(st.newIssuePriority);
      // C129T1: board assignee-column expand carries the member id
      // here; the create dialog has no assignee field, so it rides
      // pendingAssigneeRef and attaches after create (same rule as
      // the list's own assignee-group expand — see createMutation's
      // onSuccess). The dialog-close handler clears it on cancel.
      if (typeof st.newIssueAssigneeId === "string")
        pendingAssigneeRef.current = st.newIssueAssigneeId;
      // C130T1: board label-column expand carries the label id
      // here; the create dialog has no label field, so it rides
      // pendingLabelRef and attaches after create (same rule as the
      // assignee carry above and the list's own label-group expand).
      if (typeof st.newIssueLabelId === "string")
        pendingLabelRef.current = st.newIssueLabelId;
      // C131T0: board module-column expand carries the module id
      // here; the create dialog has no module field, so it rides
      // pendingModuleRef and attaches after create (same rule as
      // the label carry above and the list's own module-group expand).
      if (typeof st.newIssueModuleId === "string")
        pendingModuleRef.current = st.newIssueModuleId;
      // C132T1: board cycle-column expand (T2) carries the cycle id
      // here; the create dialog has no cycle field either (verified:
      // same field list as the label/module checks — Priority,
      // State, Estimate, dates, Parent, custom fields, no cycle
      // picker), so it rides pendingCycleRef and attaches after
      // create (same rule as the module carry above and the list's
      // own cycle-group expand). T1 builds this bridge even though
      // the list expand path itself also uses it.
      if (typeof st.newIssueCycleId === "string")
        pendingCycleRef.current = st.newIssueCycleId;
      // C133T1: board release-column expand (T2) carries the release
      // id here; the create dialog has no release field either
      // (verified: same field list as the cycle check — Priority,
      // State, Estimate, dates, Parent, custom fields, no release
      // picker), so it rides pendingReleaseRef and attaches after
      // create (same rule as the cycle carry above and the list's
      // own release-group expand). T1 builds this bridge even
      // though the list expand path itself also uses it.
      if (typeof st.newIssueReleaseId === "string")
        pendingReleaseRef.current = st.newIssueReleaseId;
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
  // C130T0: label carried from a label group's expand-to-full-
  // form (the create dialog has no label field, so the label is
  // attached right after a successful create — see createMutation's
  // onSuccess). Same ref-not-state rule as pendingAssigneeRef:
  // onSuccess must read the value from the render that opened the
  // dialog, and the dialog-close handler clears it on cancel so it
  // never leaks into the next create.
  const pendingLabelRef = useRef<string | null>(null);
  // C131T0: module carried from a module group's expand-to-full-
  // form (the create dialog has no module field, so the module is
  // attached right after a successful create — see createMutation's
  // onSuccess). Same ref-not-state rule as pendingLabelRef:
  // onSuccess must read the value from the render that opened the
  // dialog, and the dialog-close handler clears it on cancel so it
  // never leaks into the next create.
  const pendingModuleRef = useRef<string | null>(null);
  // C132T1: cycle carried from a cycle group's expand-to-full-
  // form (the create dialog has no cycle field, so the cycle is
  // attached right after a successful create — see createMutation's
  // onSuccess). Same ref-not-state rule as pendingModuleRef:
  // onSuccess must read the value from the render that opened the
  // dialog, and the dialog-close handler clears it on cancel so it
  // never leaks into the next create (including "Create more"
  // second creates — onSuccess clears the ref on the first create).
  const pendingCycleRef = useRef<string | null>(null);
  // C133T1: release carried from a release group's expand-to-full-
  // form (the create dialog has no release field, so the release is
  // attached right after a successful create — see createMutation's
  // onSuccess). Same ref-not-state rule as pendingCycleRef:
  // onSuccess must read the value from the render that opened the
  // dialog, and the dialog-close handler clears it on cancel so it
  // never leaks into the next create (including "Create more"
  // second creates — onSuccess clears the ref on the first create).
  const pendingReleaseRef = useRef<string | null>(null);
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
  // C134T1: created-by grouping needs the workspace members to
  // resolve creator ids to names (label = name ?? email). The bulk
  // query above stays gated on bulk.count > 0; this query is
  // always enabled and shares the SAME React Query key
  // ["members", slug] with the IDENTICAL queryFn, so both hooks
  // read one cache entry and one network fetch serves both: the
  // bulk query reuses this cache once a selection exists, and no
  // second members request fires. Key-reuse check: TanStack Query
  // dedupes observers by key, and `enabled` only controls fetching,
  // never the cached data — so the two observers cannot diverge.
  const groupingMembersQuery = useQuery({
    queryKey: ["members", slug],
    queryFn: () =>
      api
        .get<{ members: Member[] }>(
          `/api/v1/workspaces/${encodeURIComponent(slug)}/members`,
        )
        .then((d) => d.members),
    enabled: true,
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
      // C130T0: a label-group expand carries the group label here
      // (same rule as the assignee carry above).
      const pendingLabelId = pendingLabelRef.current;
      pendingLabelRef.current = null;
      // C131T0: a module-group expand carries the group module here
      // (same rule as the label carry above).
      const pendingModuleId = pendingModuleRef.current;
      pendingModuleRef.current = null;
      // C132T1: a cycle-group expand carries the group cycle here
      // (same rule as the module carry above).
      const pendingCycleId = pendingCycleRef.current;
      pendingCycleRef.current = null;
      // C133T1: a release-group expand carries the group release here
      // (same rule as the cycle carry above).
      const pendingReleaseId = pendingReleaseRef.current;
      pendingReleaseRef.current = null;
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
      if (pendingLabelId) {
        // C130T0: same rule as the assignee carry above — the label
        // endpoint is idempotent with no body, and an attach failure
        // surfaces honestly while the row lands in Ungrouped after
        // invalidation.
        void api
          .post(
            `${base}/issues/${issue.id}/labels/${encodeURIComponent(pendingLabelId)}`,
          )
          .then(() =>
            queryClient.invalidateQueries({
              queryKey: ["issues", slug, identifier],
            }),
          )
          .catch((e) => {
            toast.add({
              title: "Issue created but not labeled",
              type: "error",
              description:
                e instanceof Error ? e.message : "Failed to label issue",
            });
            void queryClient.invalidateQueries({
              queryKey: ["issues", slug, identifier],
            });
          });
      }
      if (pendingModuleId) {
        // C131T0: same rule as the label carry above — the module
        // endpoint is idempotent with an {issue_ids} body, and an
        // attach failure surfaces honestly while the row lands in
        // Ungrouped after invalidation.
        void api
          .post(
            `${base}/modules/${encodeURIComponent(pendingModuleId)}/issues`,
            { issue_ids: [issue.id] },
          )
          .then(() =>
            queryClient.invalidateQueries({
              queryKey: ["issues", slug, identifier],
            }),
          )
          .catch((e) => {
            toast.add({
              title: "Issue created but not added to module",
              type: "error",
              description:
                e instanceof Error ? e.message : "Failed to add to module",
            });
            void queryClient.invalidateQueries({
              queryKey: ["issues", slug, identifier],
            });
          });
      }
      if (pendingCycleId) {
        // C132T1: same rule as the module carry above — the cycle
        // endpoint is idempotent with an {issue_ids} body, and an
        // attach failure surfaces honestly while the row lands in
        // Ungrouped after invalidation.
        void api
          .post(
            `${base}/cycles/${encodeURIComponent(pendingCycleId)}/issues`,
            { issue_ids: [issue.id] },
          )
          .then(() =>
            queryClient.invalidateQueries({
              queryKey: ["issues", slug, identifier],
            }),
          )
          .catch((e) => {
            toast.add({
              title: "Issue created but not added to cycle",
              type: "error",
              description:
                e instanceof Error ? e.message : "Failed to add to cycle",
            });
            void queryClient.invalidateQueries({
              queryKey: ["issues", slug, identifier],
            });
          });
      }
      if (pendingReleaseId) {
        // C133T1: same rule as the cycle carry above — the release
        // endpoint is idempotent with an {issue_ids} body, and an
        // attach failure surfaces honestly while the row lands in
        // Ungrouped after invalidation. Single assign: the POST
        // overwrites release_id, so no other membership call exists.
        void api
          .post(
            `${base}/releases/${encodeURIComponent(pendingReleaseId)}/issues`,
            { issue_ids: [issue.id] },
          )
          .then(() =>
            queryClient.invalidateQueries({
              queryKey: ["issues", slug, identifier],
            }),
          )
          .catch((e) => {
            toast.add({
              title: "Issue created but not added to release",
              type: "error",
              description:
                e instanceof Error ? e.message : "Failed to add to release",
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
   *  C130T0: under label grouping, the group label attaches the same
   *  way (POST .../labels/{id}, idempotent, no body). C131T0: under
   *  module grouping, the group module attaches the same way
   *  (POST .../modules/{id}/issues, idempotent, {issue_ids} body).
   *  C132T1: under cycle grouping, the group cycle attaches the same
   *  way (POST .../cycles/{id}/issues, idempotent, {issue_ids} body).
   *  C133T1: under release grouping, the group release attaches the
   *  same way (POST .../releases/{id}/issues, idempotent,
   *  {issue_ids} body) — single assign, overwriting release_id.
   *  Ungrouped quick-add (no member / no label / no module / no
   *  cycle / no release) is a plain create. */
  async function quickCreate(
    stateId: string,
    name: string,
    priority?: number,
    assigneeId?: string,
    labelId?: string,
    moduleId?: string,
    cycleId?: string,
    releaseId?: string,
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
    if (labelId) {
      try {
        await api.post(
          `${base}/issues/${created.id}/labels/${encodeURIComponent(labelId)}`,
        );
      } catch (e) {
        await queryClient.invalidateQueries({
          queryKey: ["issues", slug, identifier],
        });
        throw e;
      }
    }
    if (moduleId) {
      try {
        await api.post(
          `${base}/modules/${encodeURIComponent(moduleId)}/issues`,
          { issue_ids: [created.id] },
        );
      } catch (e) {
        await queryClient.invalidateQueries({
          queryKey: ["issues", slug, identifier],
        });
        throw e;
      }
    }
    if (cycleId) {
      try {
        await api.post(
          `${base}/cycles/${encodeURIComponent(cycleId)}/issues`,
          { issue_ids: [created.id] },
        );
      } catch (e) {
        await queryClient.invalidateQueries({
          queryKey: ["issues", slug, identifier],
        });
        throw e;
      }
    }
    if (releaseId) {
      try {
        await api.post(
          `${base}/releases/${encodeURIComponent(releaseId)}/issues`,
          { issue_ids: [created.id] },
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
   *  dialog has no assignee field; see createMutation's onSuccess).
   *  C130T0: under label grouping the group label rides the same
   *  way — the dialog has no label field either (verified: the
   *  create form renders Priority, State, Estimate, dates, Parent,
   *  and custom fields, but no label picker), so the label attaches
   *  after create via pendingLabelRef rather than a prefilled field.
   *  Title + group label are therefore both kept, via attach.
   *  C131T0: under module grouping the group module rides the same
   *  way — the dialog has no module field either (verified: same
   *  field list as the label check, no module picker), so the module
   *  attaches after create via pendingModuleRef rather than a
   *  prefilled field. Title + group module are therefore both kept,
   *  via attach.
   *  C132T1: under cycle grouping the group cycle rides the same
   *  way — the dialog has no cycle field either (verified: same
   *  field list as the label/module checks, no cycle picker), so
   *  the cycle attaches after create via pendingCycleRef rather
   *  than a prefilled field. Title + group cycle are therefore
   *  both kept, via attach. No form field is invented.
   *  C133T1: under release grouping the group release rides the same
   *  way — the dialog has no release field either (verified: same
   *  field list as the cycle check, no release picker), so the
   *  release attaches after create via pendingReleaseRef rather
   *  than a prefilled field. Title + group release are therefore
   *  both kept, via attach. No form field is invented. */
  function expandQuickAdd(
    stateId: string,
    name: string,
    priority?: number,
    assigneeId?: string,
    labelId?: string,
    moduleId?: string,
    cycleId?: string,
    releaseId?: string,
  ) {
    setName(name);
    setNewStateId(stateId);
    if (priority !== undefined) setNewPriority(priority);
    pendingAssigneeRef.current = assigneeId ?? null;
    pendingLabelRef.current = labelId ?? null;
    pendingModuleRef.current = moduleId ?? null;
    pendingCycleRef.current = cycleId ?? null;
    pendingReleaseRef.current = releaseId ?? null;
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
  // C130T0: label grouping needs no skip either — labels ride on
  // the list rows themselves, exactly like assignee memberships.
  const groupByLabels = settings.groupBy === "labels";
  // C131T0: module grouping needs no skip either — modules ride on
  // the list rows themselves (project_structure.modules), exactly
  // like label memberships.
  const groupByModules = settings.groupBy === "modules";
  // C132T1: cycle grouping needs no skip either — the displayed
  // cycle rides on the list rows themselves
  // (project_structure.cycle), exactly like module memberships.
  const groupByCycles = settings.groupBy === "cycles";
  // C133T1: release grouping needs no skip either — the displayed
  // release rides on the list rows themselves
  // (project_structure.release), exactly like cycle grouping.
  const groupByReleases = settings.groupBy === "releases";
  // C134T1: created-by grouping needs no skip either — the creator
  // id rides on the list rows themselves (Issue.created_by); only
  // the display NAME resolves from the members lookup below, and
  // the grouped render gates on that resolution (see
  // groupingMembersReady) so first paint never flashes every group
  // as "Unknown" and never renders a raw creator id as a label.
  const groupByCreatedBy = settings.groupBy === "createdBy";
  // C135T1: state-group grouping needs no skip either — the group
  // resolves from the loaded project states (IssueState.group);
  // states load with the page, so unlike created-by there is no
  // pending-lookup flash to gate on. The state-by-id lookup below
  // feeds groupIssuesByStateGroup (state id -> group string).
  const groupByStateGroups = settings.groupBy === "stateGroups";
  const stateGroupById = useMemo(
    () => new Map((statesQuery.data ?? []).map((s) => [s.id, s.group])),
    [statesQuery.data],
  );
  const membersById = useMemo(
    () =>
      new Map<string, CreatorLookupEntry>(
        (groupingMembersQuery.data ?? []).map((m) => [
          m.id,
          { name: m.name, email: m.email },
        ]),
      ),
    [groupingMembersQuery.data],
  );
  // Resolved = members data arrived, OR the fetch failed (then the
  // helper runs with an empty map and unresolved creators honestly
  // collapse into Unknown — an error state, not a first-paint flash).
  // While pending, the groups branch below returns NO groups.
  const groupingMembersReady =
    groupingMembersQuery.data !== undefined || groupingMembersQuery.isError;
  const groupingActive =
    groupByState ||
    groupByPriority ||
    groupByEpic ||
    groupByAssignees ||
    groupByLabels ||
    groupByModules ||
    groupByCycles ||
    groupByReleases ||
    groupByCreatedBy ||
    groupByStateGroups;

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
    /** C130T0: label drop-target values, mirroring the assignee pair
     *  above — the key IS the label id verbatim for member groups. */
    targetLabelId?: string;
    isUngroupedLabel?: boolean;
    /** C131T0: module drop-target values, mirroring the label pair
     *  above — the key IS the module id verbatim for member groups.
     *  Module groups carry NO color (NamedRef has id + name only);
     *  the header renders name + count with no color dot. */
    targetModuleId?: string;
    isUngroupedModule?: boolean;
    /** C132T1: cycle drop-target values, mirroring the module pair
     *  above — the key IS the cycle id verbatim for member groups.
     *  Cycle groups carry NO color (NamedRef has id + name only);
     *  the header renders name + count with no color dot. */
    targetCycleId?: string;
    isUngroupedCycle?: boolean;
    /** C133T1: release drop-target values, mirroring the cycle pair
     *  above — the key IS the release id verbatim for member groups.
     *  Release groups carry NO color (NamedRef has id + name only);
     *  the header renders name + count with no color dot. */
    targetReleaseId?: string;
    isUngroupedRelease?: boolean;
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
    } else if (groupByLabels) {
      // C130T0: group by label membership (see groupByLabel.ts
      // for the multi-appearance + Ungrouped-last semantics). The
      // header renders the label's color dot — the same dot the
      // row label chips use (IssueCard) — plus name + count, and
      // no breakdown bar (the helper carries no byGroup).
      gs = groupIssuesByLabel(issues).map((g) => ({
        key: g.key,
        title: g.title,
        color: g.color,
        issues: g.issues,
        stateId: sortedStates[0]?.id,
        targetLabelId: g.isUngrouped ? undefined : g.key,
        isUngroupedLabel: g.isUngrouped,
      }));
    } else if (groupByModules) {
      // C131T0: group by module membership (see groupByModule.ts
      // for the multi-appearance + Ungrouped-last semantics). The
      // header renders name + count with NO color dot — modules
      // have none (NamedRef carries id + name only) — and no
      // breakdown bar (the helper carries no byGroup).
      gs = groupIssuesByModule(issues).map((g) => ({
        key: g.key,
        title: g.title,
        issues: g.issues,
        stateId: sortedStates[0]?.id,
        targetModuleId: g.isUngrouped ? undefined : g.key,
        isUngroupedModule: g.isUngrouped,
      }));
    } else if (groupByCycles) {
      // C132T1: group by displayed cycle (see groupByCycle.ts
      // for the single-appearance + Ungrouped-last semantics). The
      // header renders name + count with NO color dot — cycles
      // have none (NamedRef carries id + name only) — and no
      // breakdown bar (the helper carries no byGroup). Sub-group
      // sections under cycle primaries use the existing C123
      // splitter exactly as under module primaries.
      gs = groupIssuesByCycle(issues).map((g) => ({
        key: g.key,
        title: g.title,
        issues: g.issues,
        stateId: sortedStates[0]?.id,
        targetCycleId: g.isUngrouped ? undefined : g.key,
        isUngroupedCycle: g.isUngrouped,
      }));
    } else if (groupByReleases) {
      // C133T1: group by displayed release (see groupByRelease.ts
      // for the single-appearance + Ungrouped-last semantics). The
      // header renders name + count with NO color dot — releases
      // have none (NamedRef carries id + name only) — and no
      // breakdown bar (the helper carries no byGroup). Sub-group
      // sections under release primaries use the existing C123
      // splitter exactly as under cycle primaries: the splitter is
      // primary-agnostic (it takes only the group's issues), so no
      // dimension-specific wiring point needed extending.
      gs = groupIssuesByRelease(issues).map((g) => ({
        key: g.key,
        title: g.title,
        issues: g.issues,
        stateId: sortedStates[0]?.id,
        targetReleaseId: g.isUngrouped ? undefined : g.key,
        isUngroupedRelease: g.isUngrouped,
      }));
    } else if (groupByCreatedBy) {
      // C134T1: group by creator (see groupByCreatedBy.ts for the
      // single-appearance + Unknown-last/hidden-when-empty semantics).
      // The header renders name + count with NO color dot — members
      // have none — and no breakdown bar. Deliberately NO
      // target*/isUngrouped* fields: created_by is immutable and no
      // endpoint reassigns a creator, so toHeaderTarget returns null
      // and these headers are NOT drop targets (no move pipeline).
      // While the members query is pending, NO groups render at all
      // (not Unknown groups): groupingMembersReady gates here so
      // first paint never flashes every group as "Unknown" and never
      // renders a raw creator id as a label. Sub-group sections
      // under created-by primaries use the existing C123 splitter
      // exactly as under release primaries; a sub-group header keeps
      // its OWN dimension's drop behavior (state/priority/epic) —
      // that pipeline moves the sub-dimension, never the creator.
      // "Show empty groups" synthesizes no creator groups: groups
      // derive from loaded issues only, same boundary as C133.
      gs = groupingMembersReady
        ? groupIssuesByCreatedBy(issues, membersById).map((g) => ({
            key: g.key,
            title: g.title,
            issues: g.issues,
            stateId: sortedStates[0]?.id,
          }))
        : [];
    } else if (groupByStateGroups) {
      // C135T1: group by state group (see groupByStateGroup.ts for
      // the single-appearance + canonical-six-order +
      // Unknown-last/hidden-when-empty semantics). The header
      // renders name + count with NO color dot (groups have no
      // color — no color/stateGroup field is set, so the state-dot
      // branch stays off) and NO breakdown bar (no byGroup).
      // Deliberately NO target*/isUngrouped* fields: a group holds
      // several states, so a cross-group drop has no single target
      // state — toHeaderTarget returns null and these headers are
      // NOT drop targets (no move pipeline). Per-group quick-add
      // IS enabled: the default state is the first-by-sequence
      // state in the group (sortedStates is sequence-sorted), so
      // the new row STAYS in the group. A group with no states at
      // all (possible under "Show empty groups" synthesis, or the
      // Unknown group whose states are not loaded) gets stateId
      // undefined, and the QuickAdd row below renders disabled.
      // "Show empty groups" synthesizes all six canonical groups
      // via the helper's includeEmptyGroups option (closed set);
      // Unknown renders only when it has rows, in both modes.
      // Sub-group sections under state-group primaries use the
      // existing C123 splitter exactly as under created-by
      // primaries; a sub-group header keeps its OWN dimension's
      // drop behavior (state/priority/epic) — that pipeline moves
      // the sub-dimension, never the group.
      gs = groupIssuesByStateGroup(issues, stateGroupById, {
        includeEmptyGroups: settings.showEmptyGroups,
      }).map((g) => ({
        key: g.key,
        title: g.title,
        issues: g.issues,
        stateId: g.isUnknown
          ? undefined
          : sortedStates.find((s) => s.group === g.group)?.id,
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
    groupByLabels,
    groupByModules,
    groupByCycles,
    groupByReleases,
    groupByCreatedBy,
    groupByStateGroups,
    groupingMembersReady,
    membersById,
    stateGroupById,
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
        // C138T1: the created-by branch resolves creator labels from
        // this map (label = name ?? email; member-miss → Unknown).
        membersById,
        // C139T1: the state-groups branch resolves groups from this
        // map (state id -> group string; map-miss → Unknown).
        stateGroupById,
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
    membersById,
    stateGroupById,
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
  // groupByEpic/groupByAssignees/groupByLabels/groupByModules, not
  // the raw setting.
  // C130T0 keyboard boundary: pointer/touch drag is the deliverable;
  // keyboard reorder/drag under label grouping inherits the C128
  // boundary (no dedicated test under any grouping — ListDragRoot's
  // keyboard sensor is grouping-agnostic, per-stack coordinates),
  // and keyboard drag to a header stays out of scope per C127.
  // C131T0: the same boundary holds under module grouping, documented
  // not faked — no dedicated keyboard test under any grouping.
  // C132T1: the same boundary holds under cycle grouping, documented
  // not faked — pointer/touch drag is the deliverable; keyboard
  // reorder/drag under cycle grouping inherits the C128 boundary,
  // and keyboard drag to a header remains out of scope per C127.
  // C133T1: the same boundary holds under release grouping,
  // documented not faked — pointer/touch drag is the deliverable;
  // keyboard reorder/drag under release grouping inherits the C128
  // boundary, and keyboard drag to a header remains out of scope
  // per C127. C134T1: the same boundary holds under created-by
  // grouping — pointer/touch within-group reorder is the
  // deliverable (a pure sort_order PATCH, no creator change);
  // cross-group reorder stays disabled (dispatch keeps row drops
  // stack-local) and headers are not drop targets at all.
  // C135T1: the same boundary holds under state-group grouping —
  // pointer/touch within-group reorder is the deliverable (a pure
  // sort_order PATCH, no state change, with the C128 mixed-state
  // gap-collapse recovery); cross-group reorder stays disabled
  // (dispatch keeps row drops stack-local) and headers are not
  // drop targets at all (a group holds several states, so a
  // cross-group drop has no single target state).
  // Nested subtask rows are NOT draggable (C124/C126
  // precedent, unchanged — they render via NestedListSubtaskRow
  // outside any SortableIssueRow).
  // The drop pipeline lives in
  // listReorder.ts (board's applyDrop precedent); this only wires it
  // to api/queryClient. PATCH carries ONLY {sort_order} — a list
  // reorder never reassigns state. Pagination caveat (plan): drop
  // targets are the currently loaded rows only; after a drop the
  // invalidation refetches the loaded pages, so a drop at the bottom
  // of a partially-loaded list may shift on refetch among unloaded
  // values — no fetch-all-pages workaround by design.
  const effectiveGroupBy = groupByStateGroups
    ? "stateGroups"
    : groupByCreatedBy
    ? "createdBy"
    : groupByReleases
    ? "releases"
    : groupByCycles
    ? "cycles"
    : groupByModules
    ? "modules"
    : groupByLabels
    ? "labels"
    : groupByAssignees
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
  // else none. C136T1: the assignee sub-section joins it — the
  // primary assignee group, else the assignee sub-section, else none
  // (sub-sections ARE assignee now; module/cycle/release stay
  // primary-only).
  // C130T0: the label appearance mirrors it — the primary label
  // group, else none. C137T1: the label sub-section joins it — the
  // primary label group, else the label sub-section, else none
  // (sub-sections ARE labels now, as the third admitted sub-source
  // dimension).
  // C131T0: the module appearance mirrors it — the primary module
  // group, else none (sub-sections are never modules this cycle).
  // C140T1: the module sub-section joins it — the primary module
  // group, else the module sub-section, else none (C137 label
  // precedent, proven: resolveListHeaderDrop's module-move removes
  // sourceModuleId, so a sub-section drag without a sub-source
  // would add the target without removing the dragged appearance
  // and turn a move into a copy; the other appearances are
  // untouched either way, since the remove names one module).
  // C132T1: the cycle appearance mirrors it — the primary cycle
  // group, else none (sub-sections are never cycles this cycle).
  // C140T1: the cycle sub-section joins it, same proof as modules
  // — the cycle-move remove side reads sourceCycleId, and the
  // server-side add is a non-exclusive junction insert
  // (AddCycleIssues: INSERT ... ON CONFLICT DO NOTHING), so a
  // sub-section drag without a sub-source would leave the old
  // membership in place and the displayed cycle would stay the
  // alphabetically-first of the two. Single-appearance display
  // does not remove the need for a remove side; it only hides the
  // stale membership behind the first-by-name pick.
  // C133T1: the release appearance mirrors it — the primary release
  // group, else none (sub-sections are never releases this cycle).
  // C140T1: no release sub-source is added, proven unnecessary —
  // the release-move plan has NO remove side at all (a single
  // POST overwrites release_id), so sourceReleaseId never affects
  // a release drop's call sequence.
  // C134T1: there is NO created-by appearance — created_by is
  // immutable, so no sourceCreatorId exists and no move entries are
  // registered for created-by groups (their headers carry no target
  // fields, so registerHeader no-ops for them).
  // C135T1: there is NO state-group appearance either — a group
  // holds several states, so no single target state exists and no
  // move entries are registered for state-group groups (their
  // headers carry no target fields, so registerHeader no-ops for
  // them, exactly as for created-by groups).
  const listDragMaps = useMemo(() => {
    const sortableMap = new Map<string, ListDragRowRef>();
    const headerMap = new Map<string, ListHeaderDropInfo>();
    const registerStack = (
      stackKey: string,
      rows: Issue[],
      sourceEpicId: string | null,
      sourceAssigneeId: string | null = null,
      sourceLabelId: string | null = null,
      sourceModuleId: string | null = null,
      sourceCycleId: string | null = null,
      sourceReleaseId: string | null = null,
    ): ListDragStackRef => {
      const stack: ListDragStackRef = {
        stackKey,
        rows,
        sourceEpicId,
        sourceAssigneeId,
        sourceLabelId,
        sourceModuleId,
        sourceCycleId,
        sourceReleaseId,
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
        const primaryLabelId = groupByLabels
          ? g.isUngroupedLabel
            ? null
            : (g.targetLabelId ?? null)
          : null;
        const primaryModuleId = groupByModules
          ? g.isUngroupedModule
            ? null
            : (g.targetModuleId ?? null)
          : null;
        const primaryCycleId = groupByCycles
          ? g.isUngroupedCycle
            ? null
            : (g.targetCycleId ?? null)
          : null;
        const primaryReleaseId = groupByReleases
          ? g.isUngroupedRelease
            ? null
            : (g.targetReleaseId ?? null)
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
            // C136T1: assignee sub-sections mirror epic — the
            // sub-section's target is the stack's source assignee
            // (Ungrouped sub-section → null), falling back to the
            // primary assignee group otherwise. C137T1: label
            // sub-sections mirror assignee the same way — labels
            // are the third admitted sub-source dimension.
            // C140T1: module and cycle sub-sections mirror label
            // the same way — the fourth and fifth admitted
            // sub-source dimensions (see the block comment above
            // for the proof). Release sub-sections stay out: the
            // release-move plan has no remove side, so no source
            // is ever read for a release drop.
            const sourceAssigneeId =
              primaryAssigneeId ??
              (effectiveSubGroup === "assignees"
                ? sub.isUngroupedAssignee
                  ? null
                  : (sub.targetAssigneeId ?? null)
                : null);
            const sourceLabelId =
              primaryLabelId ??
              (effectiveSubGroup === "labels"
                ? sub.isUngroupedLabel
                  ? null
                  : (sub.targetLabelId ?? null)
                : null);
            const sourceModuleId =
              primaryModuleId ??
              (effectiveSubGroup === "modules"
                ? sub.isUngroupedModule
                  ? null
                  : (sub.targetModuleId ?? null)
                : null);
            const sourceCycleId =
              primaryCycleId ??
              (effectiveSubGroup === "cycles"
                ? sub.isUngroupedCycle
                  ? null
                  : (sub.targetCycleId ?? null)
                : null);
            registerStack(
              subKey,
              sub.issues,
              sourceEpicId,
              sourceAssigneeId,
              sourceLabelId,
              sourceModuleId,
              sourceCycleId,
              primaryReleaseId,
            );
            registerHeader(subKey, sub, sub.issues);
          }
        } else {
          registerStack(
            g.key,
            g.issues,
            primaryEpicId,
            primaryAssigneeId,
            primaryLabelId,
            primaryModuleId,
            primaryCycleId,
            primaryReleaseId,
          );
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
    groupByLabels,
    groupByModules,
    groupByCycles,
    groupByReleases,
    effectiveSubGroup,
  ]);

  // C127T0: one header drop — resolve to the exact call sequence,
  // execute it (add-before-remove + sort PATCH for epic/assignee/
  // label/module/cycle moves; single assign + sort PATCH, NO remove,
  // for release moves — C133T1), and invalidate so the moved row
  // appears in its new group from the server's state. No optimistic
  // group-move rendering (the simpler correct choice the plan
  // allows); on failure the row stays visibly in its old group
  // after refetch and the error surfaces via the existing
  // reorderError Alert.
  async function handleHeaderDrop(
    dragged: ListDragRowRef,
    info: ListHeaderDropInfo,
  ) {
    setReorderError(null);
    const plan = resolveListHeaderDrop({
      dragged: dragged.issue,
      sourceEpicId: dragged.stack.sourceEpicId,
      sourceAssigneeId: dragged.stack.sourceAssigneeId,
      sourceLabelId: dragged.stack.sourceLabelId,
      sourceModuleId: dragged.stack.sourceModuleId,
      sourceCycleId: dragged.stack.sourceCycleId,
      sourceReleaseId: dragged.stack.sourceReleaseId,
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
      // C130T0: label membership endpoints carry no body — the
      // label id rides the path, and both calls are idempotent.
      addToLabel: (labelId, issueId) =>
        api.post(
          `${base}/issues/${issueId}/labels/${encodeURIComponent(labelId)}`,
        ),
      removeFromLabel: (labelId, issueId) =>
        api.del(
          `${base}/issues/${issueId}/labels/${encodeURIComponent(labelId)}`,
        ),
      // C131T0: module membership endpoints carry an {issue_ids}
      // body — the module id rides the path, and both calls are
      // idempotent (verified against module_handler.go).
      addToModule: (moduleId, issueId) =>
        api.post(
          `${base}/modules/${encodeURIComponent(moduleId)}/issues`,
          { issue_ids: [issueId] },
        ),
      removeFromModule: (moduleId, issueId) =>
        api.del(
          `${base}/modules/${encodeURIComponent(moduleId)}/issues`,
          { issue_ids: [issueId] },
        ),
      // C132T1: cycle membership endpoints carry an {issue_ids}
      // body — the cycle id rides the path, and both calls are
      // idempotent (verified against cycle_handler.go, T0's
      // DELETE route alongside the existing POST route).
      addToCycle: (cycleId, issueId) =>
        api.post(
          `${base}/cycles/${encodeURIComponent(cycleId)}/issues`,
          { issue_ids: [issueId] },
        ),
      removeFromCycle: (cycleId, issueId) =>
        api.del(
          `${base}/cycles/${encodeURIComponent(cycleId)}/issues`,
          { issue_ids: [issueId] },
        ),
      // C133T1: release membership endpoints carry an {issue_ids}
      // body — the release id rides the path, and both calls are
      // idempotent (verified against release_handler.go). The move
      // path calls ONLY addToRelease (single assign overwrites
      // release_id); removeFromRelease serves release-Ungrouped
      // drops only.
      addToRelease: (releaseId, issueId) =>
        api.post(
          `${base}/releases/${encodeURIComponent(releaseId)}/issues`,
          { issue_ids: [issueId] },
        ),
      removeFromRelease: (releaseId, issueId) =>
        api.del(
          `${base}/releases/${encodeURIComponent(releaseId)}/issues`,
          { issue_ids: [issueId] },
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
                // C130T0: same rule for a carried label group label.
                pendingLabelRef.current = null;
                // C131T0: same rule for a carried module group module.
                pendingModuleRef.current = null;
                // C132T1: same rule for a carried cycle group cycle.
                pendingCycleRef.current = null;
                // C133T1: same rule for a carried release group release.
                pendingReleaseRef.current = null;
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
                    {/* C130T0: label groups render the label's color
                        dot — the same dot the row label chips use
                        (IssueCard) — plus name + count below. */}
                    {groupByLabels && g.color ? (
                      <span
                        className="h-2 w-2 rounded-full"
                        style={{ backgroundColor: g.color }}
                        aria-hidden
                      />
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
                  {/* C134T1: per-group quick-add is DISABLED under
                      created-by grouping — the create form has no
                      creator picker and created_by is server-set to
                      the actor, so a row quick-added inside another
                      creator's group would immediately leave that
                      group. The global add (page header) stays
                      available; expand-to-full-form carries nothing
                      for created-by.
                      C135T1: per-group quick-add is ENABLED under
                      state-group grouping — the group's stateId is
                      the first-by-sequence state in the group, so
                      the new row stays in the group. It renders
                      DISABLED only for a group with no states at
                      all (stateless synthesized groups, and the
                      Unknown group whose states are not loaded):
                      there is no state to default to. */}
                  {!groupByCreatedBy && (
                  <QuickAdd
                    className="mt-1"
                    disabled={groupByStateGroups && !g.stateId}
                    onCreate={(name) =>
                      quickCreate(
                        g.stateId ?? "",
                        name,
                        g.priority,
                        g.targetAssigneeId,
                        g.targetLabelId,
                        g.targetModuleId,
                        g.targetCycleId,
                        g.targetReleaseId,
                      )
                    }
                    onExpand={(name) =>
                      expandQuickAdd(
                        g.stateId ?? "",
                        name,
                        g.priority,
                        g.targetAssigneeId,
                        g.targetLabelId,
                        g.targetModuleId,
                        g.targetCycleId,
                        g.targetReleaseId,
                      )
                    }
                  />
                  )}
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
