import { useEffect, useRef, useState } from "react";
import { useNavigate } from "react-router-dom";
import { cn } from "cn";
import IssueDetailContent from "./IssueDetailContent";

/**
 * Plane-style peek view (C19T3): the issue detail slides in from the
 * right while the list/board stays mounted behind it (selection, scroll,
 * filters intact).
 *
 * Spec-exact panel: right-docked slide-over, width
 * min(1050px, calc(100% − 112px)), bg #0d0d0f, 1px left border, shadow
 * −24px 0 50px rgba(0,0,0,.38), z-20, flex column. The underlay board is
 * dimmed — a 40% black veil plus backdrop saturate(.65) approximates the
 * prototype's opacity(.65)/saturate(.65) treatment without a dark modal
 * backdrop.
 *
 * Esc closes (unless focus is in an input/textarea/contenteditable, so
 * inline editors keep their own Escape handling); focus moves into the
 * panel on open and is restored on close. Clicking the dimmed underlay
 * closes too.
 */
export default function PeekDrawer({
  slug,
  identifier,
  uuid,
  onClose,
}: {
  slug: string;
  identifier: string;
  uuid: string;
  onClose: () => void;
}) {
  const navigate = useNavigate();
  const panelRef = useRef<HTMLDivElement>(null);
  const [entered, setEntered] = useState(false);

  useEffect(() => {
    const prev = document.activeElement as HTMLElement | null;
    panelRef.current?.focus({ preventScroll: true });
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== "Escape") return;
      const t = e.target as HTMLElement | null;
      // Floating UI (popovers, menus, dialogs) renders in portals outside
      // the panel — let it handle its own Escape (e.g. close the popover)
      // instead of closing the whole peek.
      if (t && panelRef.current && !panelRef.current.contains(t)) return;
      // Inline editors (title input, description tiptap, composer) keep
      // their own Escape handling (cancel edit, dismiss mentions).
      if (
        t &&
        (t.tagName === "INPUT" ||
          t.tagName === "TEXTAREA" ||
          t.isContentEditable)
      ) {
        return;
      }
      e.stopPropagation();
      onClose();
    };
    window.addEventListener("keydown", onKey, true);
    const raf = requestAnimationFrame(() =>
      requestAnimationFrame(() => setEntered(true)),
    );
    return () => {
      window.removeEventListener("keydown", onKey, true);
      cancelAnimationFrame(raf);
      prev?.focus?.({ preventScroll: true });
    };
  }, [onClose]);

  return (
    <>
      <div
        className="fixed inset-0 z-[19] bg-black/40 backdrop-saturate-[.65]"
        onClick={onClose}
        aria-hidden
      />
      <div
        ref={panelRef}
        tabIndex={-1}
        role="dialog"
        aria-label="Issue peek view"
        className={cn(
          "fixed inset-y-0 right-0 z-20 flex w-[min(1050px,calc(100%-112px))] flex-col border-l border-border bg-[#0d0d0f] shadow-[-24px_0_50px_rgba(0,0,0,0.38)] outline-none transition-transform duration-200 ease-out",
          entered ? "translate-x-0" : "translate-x-full",
        )}
      >
        <IssueDetailContent
          key={uuid}
          slug={slug}
          identifier={identifier}
          uuid={uuid}
          compact
          fillHeight
          onClose={onClose}
          onOpenFull={() => navigate(`/w/${slug}/p/${identifier}/i/${uuid}`)}
        />
      </div>
    </>
  );
}
