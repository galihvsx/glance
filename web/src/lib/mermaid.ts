// Mermaid diagram hydration for the markdown pipeline (C18T5).
//
// Beyond-parity: Plane's mermaid support is still an OPEN feature request
// (plane/plane#9786) — Plane does not render ```mermaid fences at all;
// glance renders them as diagrams client-side.
//
// mermaid is a heavy dependency (~2 MB), so this module is NEVER imported
// statically: the Markdown component loads it with a dynamic import() only
// when a rendered page actually contains a mermaid fence. Vite splits it
// (and mermaid itself) into lazy chunks, so the initial bundle does not
// grow. The fence detector (isMermaidFence) lives in markdown.ts because
// the renderer needs it synchronously.
//
// Honest degradation: until hydration runs — and if it ever fails (bad
// diagram syntax, blocked/failed module load) — the fence renders as a
// plain code block. hydrateMermaidDiagrams() never leaves a blank hole.

import { MERMAID_CLASS } from "./markdown";

/** Attribute tracking hydration state: rendering | done | failed. Absent = pending. */
const STATE_ATTR = "data-mermaid-state";

type MermaidModule = typeof import("mermaid");

let loadPromise: Promise<MermaidModule> | null = null;

/**
 * Lazily import mermaid (own chunk via dynamic import) and initialize it
 * once. `securityLevel: "strict"` keeps the rendered SVG sanitized; the
 * module is cached process-wide after the first load.
 */
export function loadMermaid(): Promise<MermaidModule> {
  if (!loadPromise) {
    loadPromise = import("mermaid").then((mod) => {
      mod.default.initialize({ startOnLoad: false, securityLevel: "strict" });
      return mod;
    });
  }
  return loadPromise;
}

/** Test-only: drop the cached module so tests can inject a fake loader. */
export function resetMermaidForTests(): void {
  loadPromise = null;
}

let diagramSeq = 0;

/**
 * Replace every unprocessed `pre.glance-mermaid` under `root` with its
 * rendered SVG diagram. Idempotent: processed blocks carry a
 * `data-mermaid-state` marker and are skipped on repeat calls.
 *
 * Any failure — the dynamic import rejects, or mermaid rejects the
 * diagram source — leaves the fallback code block untouched, marked
 * `data-mermaid-state="failed"` so it is not retried forever.
 */
export async function hydrateMermaidDiagrams(
  root: ParentNode,
  load: () => Promise<MermaidModule> = loadMermaid,
): Promise<void> {
  const targets = Array.from(
    root.querySelectorAll(`pre.${MERMAID_CLASS}:not([${STATE_ATTR}])`),
  ) as HTMLElement[];
  if (targets.length === 0) return;

  let render: MermaidModule["default"]["render"];
  try {
    render = (await load()).default.render;
  } catch {
    for (const t of targets) t.setAttribute(STATE_ATTR, "failed");
    return;
  }

  await Promise.all(
    targets.map(async (pre) => {
      // The placeholder's textContent is the raw diagram source (the
      // fallback code block HTML-escapes it; the DOM unescapes on read).
      const source = pre.textContent ?? "";
      if (!source.trim()) {
        pre.setAttribute(STATE_ATTR, "failed");
        return;
      }
      pre.setAttribute(STATE_ATTR, "rendering");
      try {
        diagramSeq += 1;
        const id = `glance-mermaid-${diagramSeq}-${Date.now().toString(36)}`;
        const { svg } = await render(id, source);
        pre.innerHTML = svg;
        pre.setAttribute("role", "img");
        pre.setAttribute("aria-label", "Mermaid diagram");
        pre.setAttribute(STATE_ATTR, "done");
      } catch {
        // Keep the fallback code block exactly as-is — never a blank hole.
        pre.setAttribute(STATE_ATTR, "failed");
      }
    }),
  );
}
