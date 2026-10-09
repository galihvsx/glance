// Global keyboard shortcuts.
//
// The keydown listener maps keys to semantic actions; pages register
// handlers via useShortcutAction. Keys are never hijacked while the user is
// typing (input/textarea/select/contenteditable, e.g. the tiptap editor) —
// except Escape and Cmd/Ctrl+K, which intentionally work everywhere so
// overlays can always be dismissed and the palette always opened.
//
// The SHORTCUT_REGISTRY below is the single source of truth for the
// cheatsheet (key → action → description → contexts) and is covered by
// vitest tests in shortcuts.test.ts.

import { useEffect, useRef } from "react";

export type ShortcutAction =
  | "new-issue"
  | "focus-search"
  | "next-item"
  | "prev-item"
  | "open-selected"
  | "toggle-palette"
  | "open-cheatsheet"
  | "close-topmost"
  | "goto-home"
  | "goto-mywork";

export type ShortcutContext =
  | "Global"
  | "Issue list"
  | "Board"
  | "Issue detail";

export const SHORTCUT_CONTEXTS: ShortcutContext[] = [
  "Global",
  "Issue list",
  "Board",
  "Issue detail",
];

export interface ShortcutDefinition {
  action: ShortcutAction;
  /** Display tokens, e.g. ["c"], ["Ctrl/⌘", "K"], or ["g", "m"] for a chord. */
  keys: string[];
  /** True when keys form a two-key chord (press in sequence, not together). */
  chord?: boolean;
  description: string;
  contexts: ShortcutContext[];
}

// Note on Enter: Issues/Board open the selected issue in the peek drawer on
// Enter via their own local listeners (not the global one below), but it is
// a real shortcut so it is documented in the registry.
export const SHORTCUT_REGISTRY: ShortcutDefinition[] = [
  {
    action: "toggle-palette",
    keys: ["Ctrl/⌘", "K"],
    description: "Command palette",
    contexts: ["Global"],
  },
  {
    action: "open-cheatsheet",
    keys: ["?"],
    description: "Open keyboard shortcut cheatsheet",
    contexts: ["Global"],
  },
  {
    action: "goto-home",
    keys: ["g", "h"],
    chord: true,
    description: "Go to Home",
    contexts: ["Global"],
  },
  {
    action: "goto-mywork",
    keys: ["g", "m"],
    chord: true,
    description: "Go to My work",
    contexts: ["Global"],
  },
  {
    action: "close-topmost",
    keys: ["Esc"],
    description: "Close topmost dialog / drawer",
    contexts: ["Global", "Board", "Issue detail"],
  },
  {
    action: "new-issue",
    keys: ["c"],
    description: "Create issue",
    contexts: ["Issue list"],
  },
  {
    action: "focus-search",
    keys: ["/"],
    description: "Focus search",
    contexts: ["Issue list"],
  },
  {
    action: "next-item",
    keys: ["j"],
    description: "Next issue",
    contexts: ["Issue list"],
  },
  {
    action: "prev-item",
    keys: ["k"],
    description: "Previous issue",
    contexts: ["Issue list"],
  },
  {
    action: "open-selected",
    keys: ["Enter"],
    description: "Open selected issue in peek view",
    contexts: ["Issue list", "Board"],
  },
];

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

export interface KeyPressResolution {
  action: ShortcutAction | null;
  pendingPrefix: string | null;
}

// resolveKeyPress is the pure, unit-testable key → action mapping used by
// the global listener (modifiers, the typing guard, Escape and Cmd/Ctrl+K
// are handled by the listener itself, not here).
export function resolveKeyPress(
  key: string,
  pendingPrefix: string | null,
): KeyPressResolution {
  if (pendingPrefix === "g") {
    const lower = key.toLowerCase();
    if (lower === "m") return { action: "goto-mywork", pendingPrefix: null };
    if (lower === "h") return { action: "goto-home", pendingPrefix: null };
    // Not a known chord: drop the prefix and fall through to normal keys.
  }
  switch (key) {
    case "g":
      return { action: null, pendingPrefix: "g" };
    case "?":
      return { action: "open-cheatsheet", pendingPrefix: null };
    case "c":
      return { action: "new-issue", pendingPrefix: null };
    case "/":
      return { action: "focus-search", pendingPrefix: null };
    case "j":
      return { action: "next-item", pendingPrefix: null };
    case "k":
      return { action: "prev-item", pendingPrefix: null };
    default:
      return { action: null, pendingPrefix: null };
  }
}

let installed = false;

// Chord keys expire: a lone "g" older than this stops acting as a prefix.
const CHORD_TIMEOUT_MS = 800;

// installGlobalShortcuts installs the window keydown listener. Idempotent;
// returns a remover.
//
//   Cmd/Ctrl+K → toggle-palette (works even while typing)
//   Esc        → close-topmost (works even while typing; Radix
//                Dialog/Sheet overlays also close natively on Esc)
//   ?          → open-cheatsheet
//   g then m   → goto-mywork
//   g then h   → goto-home
//   c          → new-issue
//   /          → focus-search
//   j / k      → next-item / prev-item
export function installGlobalShortcuts(): () => void {
  if (installed) return () => {};
  installed = true;
  let pendingPrefix: string | null = null;
  let chordTimer: ReturnType<typeof setTimeout> | null = null;

  const clearChord = () => {
    pendingPrefix = null;
    if (chordTimer) {
      clearTimeout(chordTimer);
      chordTimer = null;
    }
  };

  const onKeyDown = (e: KeyboardEvent) => {
    if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k") {
      e.preventDefault();
      emitShortcutAction("toggle-palette");
      return;
    }
    // Escape must always dismiss the topmost overlay, even mid-typing.
    // (stopPropagation by an inner editor, e.g. the issue title input,
    // still wins because it runs before this window listener.)
    if (!e.metaKey && !e.ctrlKey && !e.altKey && e.key === "Escape") {
      clearChord();
      emitShortcutAction("close-topmost");
      return;
    }
    if (e.metaKey || e.ctrlKey || e.altKey) {
      clearChord();
      return;
    }
    if (isTypingTarget(e.target)) {
      clearChord();
      return;
    }
    const { action, pendingPrefix: next } = resolveKeyPress(
      e.key,
      pendingPrefix,
    );
    clearChord();
    if (next) {
      pendingPrefix = next;
      chordTimer = setTimeout(clearChord, CHORD_TIMEOUT_MS);
    }
    if (action) {
      if (action === "focus-search") e.preventDefault();
      emitShortcutAction(action);
    }
  };
  window.addEventListener("keydown", onKeyDown);
  return () => {
    window.removeEventListener("keydown", onKeyDown);
    clearChord();
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
