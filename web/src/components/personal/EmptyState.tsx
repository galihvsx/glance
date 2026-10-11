// EmptyState (C19T9): shared token-styled empty state for the personal
// views (My work, Home, Notifications, Drafts, Archived, Projects,
// Workspaces). Dashed hairline card, 13px/600 title, 12px muted copy —
// the Cycle 19 token ramp, not shadcn Card chrome.
import type { LucideIcon } from "lucide-react";

export default function EmptyState({
  icon: Icon,
  title,
  children,
}: {
  icon: LucideIcon;
  title: string;
  children?: React.ReactNode;
}) {
  return (
    <div className="flex flex-col items-center rounded-lg border border-dashed border-border px-6 py-12 text-center">
      <Icon className="mb-3 h-6 w-6 text-ink-muted" aria-hidden="true" />
      <p className="text-[13px] font-semibold text-ink">{title}</p>
      {children && (
        <div className="mt-1 max-w-sm text-xs text-ink-muted">{children}</div>
      )}
    </div>
  );
}
