// relativeTime: compact human labels ("just now", "3m ago", "2h ago")
// for timestamps in the notification center. Older than 30 days falls back
// to a calendar date so the feed stays truthful without fake precision.

export function relativeTime(iso: string, now: Date = new Date()): string {
  const then = new Date(iso).getTime();
  if (Number.isNaN(then)) return iso;
  const diffSec = Math.floor((now.getTime() - then) / 1000);

  if (diffSec < 0) return "just now"; // clock skew: never show "in the future"
  if (diffSec < 60) return "just now";

  const diffMin = Math.floor(diffSec / 60);
  if (diffMin < 60) return `${diffMin}m ago`;

  const diffHours = Math.floor(diffMin / 60);
  if (diffHours < 24) return `${diffHours}h ago`;

  const diffDays = Math.floor(diffHours / 24);
  if (diffDays < 7) return `${diffDays}d ago`;

  if (diffDays < 30) return `${Math.floor(diffDays / 7)}w ago`;

  const d = new Date(then);
  const sameYear = d.getFullYear() === now.getFullYear();
  return d.toLocaleDateString("en-US", {
    month: "short",
    day: "numeric",
    ...(sameYear ? {} : { year: "numeric" }),
  });
}
