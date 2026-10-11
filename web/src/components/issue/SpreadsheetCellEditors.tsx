import { useRef, useState, type KeyboardEvent } from "react";
import { PRIORITY_LABELS, type IssueState } from "../../lib/types";
import { cn } from "../../lib/utils";

/**
 * C20T3: inline cell editors for the spreadsheet view.
 *
 * 13px density, hairline borders, `h-8` so they sit inside the 47px row.
 * Native controls (select / date input) keep the spreadsheet feel and stay
 * keyboard-operable: Esc cancels, Enter/blur commits. Dropdowns commit on
 * selection; the date input commits its draft on Enter or blur.
 */

const editorCls =
  "h-8 w-full rounded-[4px] border border-border bg-canvas px-1.5 text-[13px] text-ink focus:border-focus focus:outline-none [color-scheme:dark]";

function onEditorKey(onCancel: () => void) {
  return {
    onKeyDown: (e: KeyboardEvent<HTMLElement>) => {
      if (e.key === "Escape") {
        e.stopPropagation();
        onCancel();
      } else if (e.key === "Enter") {
        // The row opens the issue on Enter — editors must not bubble.
        e.stopPropagation();
      }
    },
  };
}

export function CellStateEditor({
  states,
  value,
  onCommit,
  onCancel,
  ariaLabel,
}: {
  states: IssueState[];
  value: string;
  onCommit: (stateId: string) => void;
  onCancel: () => void;
  ariaLabel: string;
}) {
  return (
    <select
      autoFocus
      aria-label={ariaLabel}
      className={cn(editorCls, "cursor-pointer")}
      value={value}
      onChange={(e) => {
        if (e.target.value !== value) onCommit(e.target.value);
        else onCancel();
      }}
      // Blur without a selection change = cancel (no-op close).
      onBlur={onCancel}
      {...onEditorKey(onCancel)}
    >
      {states.map((s) => (
        <option key={s.id} value={s.id}>
          {s.name}
        </option>
      ))}
    </select>
  );
}

export function CellPriorityEditor({
  value,
  onCommit,
  onCancel,
  ariaLabel,
}: {
  value: number;
  onCommit: (priority: number) => void;
  onCancel: () => void;
  ariaLabel: string;
}) {
  return (
    <select
      autoFocus
      aria-label={ariaLabel}
      className={cn(editorCls, "cursor-pointer")}
      value={String(value)}
      onChange={(e) => {
        const v = Number(e.target.value);
        if (v !== value) onCommit(v);
        else onCancel();
      }}
      onBlur={onCancel}
      {...onEditorKey(onCancel)}
    >
      {PRIORITY_LABELS.map((label, i) => (
        <option key={i} value={String(i)}>
          {label}
        </option>
      ))}
    </select>
  );
}

export function CellDateEditor({
  value,
  onCommit,
  onCancel,
  ariaLabel,
}: {
  value?: string | null;
  /** Commits the ISO date (yyyy-mm-dd) or null to clear. */
  onCommit: (v: string | null) => void;
  onCancel: () => void;
  ariaLabel: string;
}) {
  const [draft, setDraft] = useState(value ? value.slice(0, 10) : "");
  // Guard: commit and cancel both funnel through blur/keydown paths that
  // can double-fire (e.g. Enter → commit → unmount → blur). Settle once.
  const settled = useRef(false);
  const commit = () => {
    if (settled.current) return;
    settled.current = true;
    onCommit(draft || null);
  };
  const cancel = () => {
    if (settled.current) return;
    settled.current = true;
    onCancel();
  };
  return (
    <input
      type="date"
      autoFocus
      aria-label={ariaLabel}
      className={editorCls}
      value={draft}
      onChange={(e) => setDraft(e.target.value)}
      onBlur={commit}
      onKeyDown={(e) => {
        // Never bubble to the row (it opens the issue on Enter).
        e.stopPropagation();
        if (e.key === "Escape") {
          cancel();
        } else if (e.key === "Enter") {
          e.preventDefault();
          commit();
        }
      }}
    />
  );
}
