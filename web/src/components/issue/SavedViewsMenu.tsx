import { useEffect, useRef, useState } from "react";
import { useSearchParams } from "react-router-dom";
import {
  Bookmark,
  Check,
  Pencil,
  Plus,
  Share2,
  Star,
  Trash2,
  X,
} from "lucide-react";
import { Button, buttonVariants } from "../ui/button";
import { Input } from "../ui/input";
import { Popover, PopoverContent, PopoverTrigger } from "../ui/popover";
import { Separator } from "../ui/separator";
import { toast } from "../ui/toast";
import { cn } from "cn";
import {
  EMPTY_FILTERS,
  isFilterParam,
  serializeFilters,
  type IssueFilters,
} from "../../lib/filters";
import { ApiError } from "../../lib/api";
import type { SavedViewScope } from "../../lib/savedViews";
import type { DisplaySettings } from "./useDisplaySettings";
import { useSavedViews, type SavedView } from "./useSavedViews";

interface SavedViewsMenuBaseProps {
  filters: IssueFilters;
  /** Null on pages without display settings (spreadsheet): views saved and
   *  applied there carry filters only. */
  display: DisplaySettings | null;
  onApplyDisplay: ((d: DisplaySettings) => void) | null;
  /** Extra classes for the popover trigger (C19T4: PageHeader restyles it). */
  triggerClassName?: string;
  /**
   * C91T1 PQL wiring (Issues page only). pqlMode tells the menu the
   * current save is a PQL view; pqlQuery is the applied query.
   * onViewApplied lets the page settle its PQL mode when a view is
   * applied (PQL view → PQL mode, visual view → visual mode).
   * Pages that do not pass these keep the pre-C91 behavior exactly.
   */
  pqlMode?: boolean;
  pqlQuery?: string;
  onViewApplied?: (pqlQuery: string | null) => void;
  /**
   * C107T0 detail-page mode (cycle/module detail pages). Detail pages
   * do not read URL filter params (PQL mode is local state by the C95
   * decision), have no visual filter bar, and must not silently narrow
   * the membership list to a project-level default on mount. So in
   * this mode the menu: (a) never writes to the URL — applyView and
   * the default-view effect skip setSearchParams entirely, the page
   * settles state through onViewApplied only; (b) never auto-applies
   * a default view — the default-view effect does not run at all;
   * (c) lists PQL views only — a view without a non-empty
   * filters.pql cannot be represented on a surface with no visual
   * filter bar. Rename / delete / share / set-default on a listed
   * PQL view keep working (project-level operations). Default false
   * preserves List / Board / Spreadsheet behavior exactly.
   */
  detailMode?: boolean;
}

/** The scope the menu operates in (C113T1). Exactly one of the two:
 *  a project UUID (the pre-C113 shape — existing call sites pass
 *  `projectId` unchanged) or a workspace slug (MyWork). The union
 *  keeps both call shapes compiling while sharing every line of
 *  list/save/rename/delete/share/default logic below. */
export type SavedViewsMenuProps = SavedViewsMenuBaseProps &
  (
    | { projectId: string; workspaceSlug?: never }
    | { workspaceSlug: string; projectId?: never }
  );

/** Saved views: named filter + display presets, persisted
 *  server-side (C9T2 project scope, C113T1 workspace scope). Mounted
 *  in the project nav (via ProjectNav's `trailing` slot) on the list,
 *  board, and spreadsheet pages, and beside the All work / PQL toggle
 *  on MyWork. Views the owner shares with the scope (C10T1 project,
 *  C113T1 workspace) render under a "Shared" section as read-only
 *  rows; the share/unshare toggle is owner-only. */
export default function SavedViewsMenu({
  projectId,
  workspaceSlug,
  filters,
  display,
  onApplyDisplay,
  triggerClassName,
  pqlMode,
  pqlQuery,
  onViewApplied,
  detailMode = false,
}: SavedViewsMenuProps) {
  // Exactly one scope prop is present (the union enforces it); the
  // workspace form is checked first so a workspace slug can never be
  // mistaken for a project UUID.
  const scope: SavedViewScope =
    workspaceSlug !== undefined
      ? { workspaceSlug }
      : { projectId: projectId ?? "" };
  // Sharing copy adapts to the scope (C113T1): "shared with the
  // workspace" in workspace scope, "shared with the project" in
  // project scope. No other copy changes.
  const scopeLabel = workspaceSlug !== undefined ? "workspace" : "project";
  const {
    views,
    ownViews,
    sharedViews,
    viewsLoading,
    viewsReady,
    viewsError,
    createView,
    renameView,
    deleteView,
    setDefaultView,
    setViewShared,
  } = useSavedViews(scope);
  const [searchParams, setSearchParams] = useSearchParams();
  const [open, setOpen] = useState(false);
  const [saving, setSaving] = useState(false);
  const [saveName, setSaveName] = useState("");
  const [saveError, setSaveError] = useState<string | null>(null);
  const [renamingId, setRenamingId] = useState<string | null>(null);
  const [renameName, setRenameName] = useState("");
  const [renameError, setRenameError] = useState<string | null>(null);
  const [confirmingDelete, setConfirmingDelete] = useState<string | null>(
    null,
  );

  function applyView(view: SavedView) {
    // C107T0: in detail mode the page owns its state (local PQL mode,
    // no URL filter params) — applying a view must not write anything
    // to the URL; the page settles through onViewApplied only.
    if (!detailMode) {
      const next = new URLSearchParams();
      // Preserve non-filter params (e.g. ?peek=); replace filter params with
      // the view's stored filters. serializeFilters writes `pql` for PQL
      // views and omits it for visual ones, so the URL mode follows the
      // view; the page still needs the explicit mode signal below because
      // PQL mode with an empty query is not visible in the URL.
      for (const [k, v] of searchParams) {
        if (!isFilterParam(k)) next.set(k, v);
      }
      for (const [k, v] of serializeFilters(view.filters)) {
        next.set(k, v);
      }
      setSearchParams(next, { replace: true });
    }
    onViewApplied?.(view.filters.pql?.trim() ? view.filters.pql.trim() : null);
    if (view.display && onApplyDisplay) {
      onApplyDisplay(view.display);
    }
    setOpen(false);
  }

  // Default view: applied once per mount when the URL carries no explicit
  // filter params. Only the caller's OWN views are eligible — a default
  // set by another member on their shared view must never auto-apply.
  // Readiness gate (C106T0): the once-flag must NOT burn until the views
  // for the current project have actually been fetched. Every page mounts
  // this menu with projectId "" first (the project resolves after mount)
  // and the disabled query reports isLoading === false with views === [],
  // so gating on !viewsLoading alone spends the flag on the empty first
  // mount and the default then never applies. viewsReady is false until
  // the query settles for a real projectId, so the effect retries once
  // the views arrive. A fetch error likewise must not burn the flag —
  // the load-failure toast is the honest behavior there, and a later
  // successful load must still apply. The menu is keyed by project in
  // each page, so a project switch remounts and re-runs this fresh.
  const defaultApplied = useRef(false);
  useEffect(() => {
    // C107T0: detail pages never auto-apply a default — mounting must
    // show the unfiltered membership list. Return before burning the
    // flag (the mode is fixed per mount, but not burning keeps the
    // effect a pure no-op here).
    if (detailMode) return;
    if (defaultApplied.current || !viewsReady || viewsLoading) return;
    // Fetch failed: no data to decide from. Return WITHOUT burning the
    // flag so a later successful load retries the apply.
    if (viewsError) return;
    defaultApplied.current = true;
    let hasFilterParams = false;
    for (const k of searchParams.keys()) {
      if (isFilterParam(k)) {
        hasFilterParams = true;
        break;
      }
    }
    if (hasFilterParams) return;
    const def = ownViews.find((v) => v.isDefault);
    if (!def) return;
    const next = new URLSearchParams();
    for (const [k, v] of searchParams) {
      if (!isFilterParam(k)) next.set(k, v);
    }
    for (const [k, v] of serializeFilters(def.filters)) {
      next.set(k, v);
    }
    setSearchParams(next, { replace: true });
    onViewApplied?.(def.filters.pql?.trim() ? def.filters.pql.trim() : null);
    if (def.display && onApplyDisplay) {
      onApplyDisplay(def.display);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [detailMode, viewsLoading, views, viewsReady, viewsError]);

  // C107T0: detail mode lists PQL views only, in both sections. A
  // view with a blank/absent pql cannot be represented on a surface
  // with no visual filter bar, so it renders nowhere in this mode —
  // not even as the trigger's active-view label (hence activeView
  // below also searches only the listed views in detail mode).
  const isPqlView = (v: SavedView) => !!v.filters.pql?.trim();
  const listedOwnViews = detailMode ? ownViews.filter(isPqlView) : ownViews;
  const listedSharedViews = detailMode
    ? sharedViews.filter(isPqlView)
    : sharedViews;

  const currentQuery = serializeFilters(filters).toString();
  const activeView = (detailMode
    ? [...listedOwnViews, ...listedSharedViews]
    : views
  ).find(
    (v) =>
      serializeFilters(v.filters).toString() === currentQuery &&
      JSON.stringify(v.display) === JSON.stringify(display),
  );

  async function handleSave() {
    if (!saveName.trim()) {
      setSaveError("Give the view a name.");
      return;
    }
    try {
      // C91T1: in PQL mode the view carries ONLY the query — the parked
      // visual selection is not saved (modes are exclusive). An empty
      // query saves as a plain unfiltered view. Visual mode is
      // unchanged. Old views without a pql key still load as before
      // (sanitizeFilters merges over EMPTY_FILTERS).
      // C107T0: detail mode saves the same PQL-only shape even when
      // the caller did not pass pqlMode — a detail page has no visual
      // filter bar, so a save from its menu is always a query save.
      const filtersToSave =
        pqlMode || detailMode
          ? {
              ...EMPTY_FILTERS,
              ...(pqlQuery?.trim() ? { pql: pqlQuery.trim() } : {}),
            }
          : filters;
      const created = await createView(
        saveName,
        filtersToSave,
        display,
      );
      if (!created) {
        // Blank name (handled above) or the 50-view cap.
        setSaveError("A view with that name already exists.");
        return;
      }
      setSaving(false);
      setSaveName("");
      setSaveError(null);
    } catch (e) {
      if (e instanceof ApiError && e.status === 409) {
        setSaveError("A view with that name already exists.");
      } else {
        setSaveError(null);
        setSaving(false);
        toast.add({ title: "Could not save the view", type: "error" });
      }
    }
  }

  async function handleRename(id: string) {
    if (!renameName.trim()) {
      setRenameError("Give the view a name.");
      return;
    }
    try {
      if (await renameView(id, renameName)) {
        setRenamingId(null);
        setRenameName("");
        setRenameError(null);
      } else {
        setRenameError("A view with that name already exists.");
      }
    } catch (e) {
      if (e instanceof ApiError && e.status === 409) {
        setRenameError("A view with that name already exists.");
      } else {
        setRenamingId(null);
        toast.add({ title: "Could not rename the view", type: "error" });
      }
    }
  }

  async function handleDelete(id: string) {
    try {
      await deleteView(id);
    } catch {
      toast.add({ title: "Could not delete the view", type: "error" });
    } finally {
      setConfirmingDelete(null);
    }
  }

  async function handleToggleDefault(view: SavedView) {
    try {
      await setDefaultView(view.isDefault ? null : view.id);
    } catch {
      toast.add({ title: "Could not update the default view", type: "error" });
    }
  }

  async function handleToggleShared(view: SavedView) {
    try {
      await setViewShared(view.id, !view.shared);
    } catch {
      // Honest error — no silent fallback: the toggle stays as it was.
      toast.add({ title: "Could not update sharing", type: "error" });
    }
  }

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger
        className={cn(
          buttonVariants({ variant: "outline", size: "sm" }),
          "gap-2",
          triggerClassName,
        )}
        aria-label="Saved views"
      >
        <Bookmark className="h-4 w-4" />
        <span className="max-w-32 truncate">
          {activeView ? activeView.name : "Views"}
        </span>
      </PopoverTrigger>
      <PopoverContent className="w-72 p-2" align="end">
        <p className="px-2 pb-1 pt-1 text-[11px] font-medium uppercase tracking-wide text-muted-foreground">
          Saved views
        </p>
        {viewsLoading ? (
          <p className="px-2 py-3 text-sm text-muted-foreground">
            Loading views…
          </p>
        ) : listedOwnViews.length === 0 && listedSharedViews.length === 0 ? (
          <p className="px-2 py-3 text-sm text-muted-foreground">
            No saved views yet. Set your filters, then save the current view.
          </p>
        ) : (
          <div className="flex flex-col">
            {listedOwnViews.map((view) => {
              const active = activeView?.id === view.id;
              if (renamingId === view.id) {
                return (
                  <div key={view.id} className="p-1">
                    <Input
                      autoFocus
                      value={renameName}
                      onChange={(e) => setRenameName(e.target.value)}
                      onKeyDown={(e) => {
                        if (e.key === "Enter") handleRename(view.id);
                        if (e.key === "Escape") setRenamingId(null);
                      }}
                      className="h-8 text-sm"
                    />
                    {renameError && (
                      <p className="px-1 pt-1 text-xs text-destructive">
                        {renameError}
                      </p>
                    )}
                    <div className="flex gap-1 pt-1">
                      <Button
                        size="sm"
                        className="h-7 flex-1"
                        onClick={() => handleRename(view.id)}
                      >
                        Rename
                      </Button>
                      <Button
                        size="sm"
                        variant="ghost"
                        className="h-7"
                        onClick={() => setRenamingId(null)}
                      >
                        Cancel
                      </Button>
                    </div>
                  </div>
                );
              }
              if (confirmingDelete === view.id) {
                return (
                  <div
                    key={view.id}
                    className="flex items-center gap-1 rounded px-2 py-1.5 text-sm"
                  >
                    <span className="flex-1 truncate">
                      Delete “{view.name}”?
                    </span>
                    <Button
                      size="sm"
                      variant="destructive"
                      className="h-7"
                      onClick={() => handleDelete(view.id)}
                    >
                      Delete
                    </Button>
                    <Button
                      size="sm"
                      variant="ghost"
                      className="h-7"
                      onClick={() => setConfirmingDelete(null)}
                    >
                      Cancel
                    </Button>
                  </div>
                );
              }
              return (
                <div
                  key={view.id}
                  className={cn(
                    "group flex items-center gap-0.5 rounded px-1 py-0.5",
                    active && "bg-muted",
                  )}
                >
                  <button
                    type="button"
                    onClick={() => applyView(view)}
                    className="flex min-w-0 flex-1 items-center gap-2 rounded px-2 py-1.5 text-left text-sm hover:bg-muted"
                    title={`Apply view “${view.name}”`}
                  >
                    {active ? (
                      <Check className="h-3.5 w-3.5 shrink-0 text-primary" />
                    ) : (
                      <span className="h-3.5 w-3.5 shrink-0" />
                    )}
                    <span className="truncate">{view.name}</span>
                    {view.isDefault && (
                      <Star className="h-3 w-3 shrink-0 fill-amber-400 text-amber-400" />
                    )}
                  </button>
                  <Button
                    size="icon"
                    variant="ghost"
                    className="h-7 w-7 shrink-0 opacity-0 group-hover:opacity-100 focus:opacity-100"
                    title={
                      view.isDefault
                        ? "Remove default"
                        : "Set as default view"
                    }
                    onClick={() => handleToggleDefault(view)}
                  >
                    <Star
                      className={cn(
                        "h-3.5 w-3.5",
                        view.isDefault && "fill-amber-400 text-amber-400",
                      )}
                    />
                  </Button>
                  <Button
                    size="icon"
                    variant="ghost"
                    className="h-7 w-7 shrink-0 opacity-0 group-hover:opacity-100 focus:opacity-100"
                    title={`Rename “${view.name}”`}
                    onClick={() => {
                      setRenamingId(view.id);
                      setRenameName(view.name);
                      setRenameError(null);
                    }}
                  >
                    <Pencil className="h-3.5 w-3.5" />
                  </Button>
                  {/* Share toggle (C10T1): owner-only — these rows are the
                      caller's own views by construction. */}
                  <Button
                    size="icon"
                    variant="ghost"
                    className="h-7 w-7 shrink-0 opacity-0 group-hover:opacity-100 focus:opacity-100"
                    title={
                      view.shared
                        ? `Stop sharing “${view.name}” with the ${scopeLabel}`
                        : `Share “${view.name}” with the ${scopeLabel}`
                    }
                    onClick={() => handleToggleShared(view)}
                  >
                    <Share2
                      className={cn(
                        "h-3.5 w-3.5",
                        view.shared && "text-primary",
                      )}
                    />
                  </Button>
                  <Button
                    size="icon"
                    variant="ghost"
                    className="h-7 w-7 shrink-0 opacity-0 group-hover:opacity-100 focus:opacity-100"
                    title={`Delete “${view.name}”`}
                    onClick={() => setConfirmingDelete(view.id)}
                  >
                    <Trash2 className="h-3.5 w-3.5" />
                  </Button>
                </div>
              );
            })}
            {/* Shared views (C10T1): other members' views, read-only.
                Apply + owner attribution only — no rename/default/share/
                delete controls for non-owners. */}
            {listedSharedViews.length > 0 && (
              <>
                <Separator className="my-2" />
                <p className="px-2 pb-1 text-[11px] font-medium uppercase tracking-wide text-muted-foreground">
                  Shared
                </p>
                {listedSharedViews.map((view) => {
                  const active = activeView?.id === view.id;
                  return (
                    <div
                      key={view.id}
                      className={cn(
                        "group flex items-center gap-0.5 rounded px-1 py-0.5",
                        active && "bg-muted",
                      )}
                    >
                      <button
                        type="button"
                        onClick={() => applyView(view)}
                        className="flex min-w-0 flex-1 items-center gap-2 rounded px-2 py-1.5 text-left text-sm hover:bg-muted"
                        title={`Apply view “${view.name}”`}
                      >
                        {active ? (
                          <Check className="h-3.5 w-3.5 shrink-0 text-primary" />
                        ) : (
                          <span className="h-3.5 w-3.5 shrink-0" />
                        )}
                        <span className="truncate">{view.name}</span>
                      </button>
                      {view.ownerName && (
                        <span
                          className="max-w-24 truncate px-1 text-xs text-muted-foreground"
                          title={`Shared by ${view.ownerName}`}
                        >
                          {view.ownerName}
                        </span>
                      )}
                    </div>
                  );
                })}
              </>
            )}
          </div>
        )}
        <Separator className="my-2" />
        {saving ? (
          <div className="p-1">
            <Input
              autoFocus
              value={saveName}
              onChange={(e) => setSaveName(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === "Enter") handleSave();
                if (e.key === "Escape") setSaving(false);
              }}
              placeholder="View name…"
              maxLength={64}
              className="h-8 text-sm"
            />
            {saveError && (
              <p className="px-1 pt-1 text-xs text-destructive">{saveError}</p>
            )}
            <div className="flex gap-1 pt-1">
              <Button size="sm" className="h-7 flex-1" onClick={handleSave}>
                Save view
              </Button>
              <Button
                size="sm"
                variant="ghost"
                className="h-7"
                onClick={() => {
                  setSaving(false);
                  setSaveError(null);
                }}
              >
                <X className="h-3.5 w-3.5" />
              </Button>
            </div>
            <p className="px-1 pt-1 text-xs text-muted-foreground">
              {pqlMode
                ? "Saves the current PQL query"
                : "Saves the current filters"}
              {display ? " and display settings" : ""}.
            </p>
          </div>
        ) : (
          <Button
            variant="ghost"
            size="sm"
            className="w-full justify-start gap-2"
            onClick={() => {
              setSaving(true);
              setSaveName("");
              setSaveError(null);
            }}
          >
            <Plus className="h-4 w-4" />
            Save current view
          </Button>
        )}
      </PopoverContent>
    </Popover>
  );
}
