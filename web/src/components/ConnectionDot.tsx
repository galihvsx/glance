// Realtime connection status indicator (Task 25): a small fixed dot,
// bottom-right, reflecting the WS client's connection state.

import { useConnectionStatus } from "../lib/realtime";

const DOT: Record<string, string> = {
  open: "bg-emerald-500",
  connecting: "bg-amber-500",
  closed: "bg-muted-foreground/40",
};

const LABEL: Record<string, string> = {
  open: "Live — realtime connected",
  connecting: "Reconnecting to realtime…",
  closed: "Realtime disconnected",
};

export default function ConnectionDot() {
  const status = useConnectionStatus();
  return (
    <div
      title={LABEL[status]}
      aria-label={LABEL[status]}
      className="fixed bottom-4 right-4 z-50 flex items-center gap-2 rounded-full border bg-background/90 px-3 py-1.5 text-xs text-muted-foreground shadow-sm backdrop-blur"
    >
      <span className={`h-2 w-2 rounded-full ${DOT[status]}`} />
      {status === "open"
        ? "Live"
        : status === "connecting"
          ? "Reconnecting"
          : "Offline"}
    </div>
  );
}
