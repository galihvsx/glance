import { useEffect, useRef, useState } from "react";
import { useSearchParams } from "react-router-dom";
import {
  Bookmark,
  Check,
  Pencil,
  Plus,
  Star,
  Trash2,
  X,
} from "lucide-react";
import { Button, buttonVariants } from "../ui/button";
import { Input } from "../ui/input";
import { Popover, PopoverContent, PopoverTrigger } from "../ui/popover";
import { Separator } from "../ui/separator";
import { cn } from "cn";
import {
  isFilterParam,
  serializeFilters,
  type IssueFilters,
} from "../../lib/filters";
import type { DisplaySettings } from "./useDisplaySettings";
import { useSavedViews, type SavedView } from "./useSavedViews";

interface SavedViewsMenuProps {
  slug: string;
  identifier: string;
  filters: IssueFilters;
  /** Null on pages without display settings (spreadsheet): views saved and
   *  applied there carry filters only. */
  display: DisplaySettings | null;
  onApplyDisplay: ((d: DisplaySettings) => void) | null;
}

/** Per-project saved views: named filter + display presets. Mounted in the
 *  project nav (via ProjectNav's `trailing` slot) on the list, board, and
 *  spreadsheet pages. */
export default function SavedViewsMenu({
  slug,
  identifier,
  filters,
  display,
  onApplyDisplay,
}: SavedViewsMenuProps) {
  const { views, createView, renameView, deleteView, setDefaultView } =
    useSavedViews(slug, identifier);
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
    const next = new URLSearchParams();
    // Preserve non-filter params (e.g. ?peek=); replace filter params with
    // the view's verbatim query string.
    for (const [k, v] of searchParams) {
      if (!isFilterParam(k)) next.set(k, v);
    }
    for (const [k, v] of new URLSearchParams(view.filterQuery)) {
      next.set(k, v);
    }
    setSearchParams(next, { replace: true });
    if (view.display && onApplyDisplay) {
      onApplyDisplay(view.display);
    }
    setOpen(false);
  }

  // Default view: applied once per mount when the URL carries no explicit
  // filter params. (The menu is keyed by project in each page, so a project
  // switch remounts and re-runs this.)
  const defaultApplied = useRef(false);
  useEffect(() => {
    if (defaultApplied.current) return;
    defaultApplied.current = true;
    let hasFilterParams = false;
    for (const k of searchParams.keys()) {
      if (isFilterParam(k)) {
        hasFilterParams = true;
        break;
      }
    }
    if (hasFilterParams) return;
    const def = views.find((v) => v.isDefault);
    if (!def) return;
    const next = new URLSearchParams();
    for (const [k, v] of searchParams) {
      if (!isFilterParam(k)) next.set(k, v);
    }
    for (const [k, v] of new URLSearchParams(def.filterQuery)) {
      next.set(k, v);
    }
    setSearchParams(next, { replace: true });
    if (def.display && onApplyDisplay) {
      onApplyDisplay(def.display);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const currentQuery = serializeFilters(filters).toString();
  const activeView = views.find(
    (v) =>
      v.filterQuery === currentQuery &&
      JSON.stringify(v.display) === JSON.stringify(display),
  );

  function handleSave() {
    const created = createView(
      saveName,
      serializeFilters(filters).toString(),
      display,
    );
    if (!created) {
      setSaveError(
        saveName.trim()
          ? "A view with that name already exists."
          : "Give the view a name.",
      );
      return;
    }
    setSaving(false);
    setSaveName("");
    setSaveError(null);
  }

  function handleRename(id: string) {
    if (renameView(id, renameName)) {
      setRenamingId(null);
      setRenameName("");
      setRenameError(null);
    } else {
      setRenameError(
        renameName.trim()
          ? "A view with that name already exists."
          : "Give the view a name.",
      );
    }
  }

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger
        className={cn(buttonVariants({ variant: "outline", size: "sm" }), "gap-2")}
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
        {views.length === 0 ? (
          <p className="px-2 py-3 text-sm text-muted-foreground">
            No saved views yet. Set your filters, then save the current view.
          </p>
        ) : (
          <div className="flex flex-col">
            {views.map((view) => {
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
                      onClick={() => {
                        deleteView(view.id);
                        setConfirmingDelete(null);
                      }}
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
                    onClick={() =>
                      setDefaultView(view.isDefault ? null : view.id)
                    }
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
              Saves the current filters
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
