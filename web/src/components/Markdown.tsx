import { useEffect, useRef } from "react";
import { MERMAID_CLASS, renderMarkdown } from "../lib/markdown";

/**
 * Renders markdown to HTML and hydrates ```mermaid fences into diagrams.
 *
 * The mermaid library (and its hydration wrapper) is lazy-loaded via
 * dynamic import() only when the rendered markdown actually contains a
 * mermaid fence, so the initial bundle never includes it. Until hydration
 * completes — or if it fails — mermaid fences display as plain code blocks.
 * Use this component (not raw renderMarkdown) for every markdown surface
 * so diagrams work everywhere markdown renders (C18T5).
 */
export function Markdown({
  markdown,
  className,
}: {
  markdown: string;
  className?: string;
}) {
  const ref = useRef<HTMLDivElement>(null);

  // Runs after every render: fresh markdown produces fresh placeholders.
  // The dynamic import is cached by the module loader, so repeat renders
  // cost nothing; hydration itself skips already-processed blocks.
  useEffect(() => {
    const el = ref.current;
    if (el && el.querySelector(`pre.${MERMAID_CLASS}`)) {
      void import("../lib/mermaid").then(({ hydrateMermaidDiagrams }) =>
        hydrateMermaidDiagrams(el),
      );
    }
  });

  return (
    <div
      ref={ref}
      className={className}
      dangerouslySetInnerHTML={{ __html: renderMarkdown(markdown) }}
    />
  );
}
