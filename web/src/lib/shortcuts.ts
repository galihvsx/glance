// Global keyboard shortcuts (Task 25).
//
// The keydown listener maps keys to semantic actions; pages register
// handlers via useShortcutAction. Keys are never hijacked while the user is
// typing (input/textarea/select/contenteditable, e.g. the tiptap editor).

import { useEffect, useRef } from "react";

export type ShortcutAction =
  | "new-issue"
  | "focus-search"
  | "next-item"
  | "prev-item"
  | "toggle-palette";

type ActionHandler = () => void;

const handlers = new Map<ShortcutAction, Set<ActionHandler>>();

export function onShortcutAction(
  action: ShortcutAction,
  fn: ActionHandler,
): () => void {
  let set = handlers.get(action);
  if (!set) {
    set = new Set();
    handlers.set(action, set);
  }
  set.add(fn);
  return () => {
    set.delete(fn);
  };
}

export function emitShortcutAction(action: ShortcutAction): void {
  handlers.get(action)?.forEach((fn) => {
    try {
      fn();
    } catch {
      // A shortcut handler must never break the listener.
    }
  });
}

// isTypingTarget reports whether keyboard events from target belong to a
// text-entry element. Takes EventTarget so it stays unit-testable without
// a DOM.
export function isTypingTarget(target: EventTarget | null): boolean {
  const el = target as (HTMLElement & { tagName?: unknown }) | null;
  if (!el || typeof el.tagName !== "string") return false;
  const tag = el.tagName.toUpperCase();
  if (tag === "INPUT" || tag === "TEXTAREA" || tag === "SELECT") return true;
  if (el.isContentEditable) return true;
  return false;
}

let installed = false;

// installGlobalShortcuts installs the window keydown listener. Idempotent;
// returns a remover.
//
//   Cmd/Ctrl+K → toggle-palette (works even while typing)
//   c           → new-issue
//   /           → focus-search
//   j / k       → next-item / prev-item
export function installGlobalShortcuts(): () => void {
  if (installed) return () => {};
  installed = true;
  const onKeyDown = (e: KeyboardEvent) => {
    if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k") {
      e.preventDefault();
      emitShortcutAction("toggle-palette");
      return;
    }
    if (e.metaKey || e.ctrlKey || e.altKey) return;
    if (isTypingTarget(e.target)) return;
    switch (e.key) {
      case "c":
        emitShortcutAction("new-issue");
        break;
      case "/":
        e.preventDefault();
        emitShortcutAction("focus-search");
        break;
      case "j":
        emitShortcutAction("next-item");
        break;
      case "k":
        emitShortcutAction("prev-item");
        break;
      default:
        break;
    }
  };
  window.addEventListener("keydown", onKeyDown);
  return () => {
    window.removeEventListener("keydown", onKeyDown);
    installed = false;
  };
}

// useShortcutAction registers fn for action while the component is mounted.
export function useShortcutAction(
  action: ShortcutAction,
  fn: () => void,
): void {
  const ref = useRef(fn);
  useEffect(() => {
    ref.current = fn;
  });
  useEffect(() => onShortcutAction(action, () => ref.current()), [action]);
}
