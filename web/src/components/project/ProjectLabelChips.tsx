// ProjectLabelChips (C76T1): project-label chips reusing the issue-label
// chip styling (secondary badge + color dot). Renders the first `max`
// chips and collapses the rest into a +N overflow chip whose title lists
// the hidden names; every chip carries an accessible title. Renders
// nothing when the project has no labels.

import { Badge } from "../ui/badge";
import type { ProjectLabel } from "../../lib/types";

export default function ProjectLabelChips({
  labels,
  max = 3,
  className,
}: {
  labels: ProjectLabel[];
  /** Max chips before the +N overflow (default 3). */
  max?: number;
  className?: string;
}) {
  if (labels.length === 0) return null;
  const shown = labels.slice(0, Math.max(0, max));
  const rest = labels.slice(shown.length);

  return (
    <span
      className={`inline-flex min-w-0 items-center gap-1 ${className ?? ""}`}
      role="group"
      aria-label={`Project labels: ${labels.map((l) => l.name).join(", ")}`}
    >
      {shown.map((l) => (
        <Badge
          key={l.id}
          variant="secondary"
          className="gap-1 text-xs font-normal"
          title={l.name}
        >
          <span
            className="h-2 w-2 shrink-0 rounded-full"
            style={{ backgroundColor: l.color }}
            aria-hidden
          />
          <span className="max-w-28 truncate">{l.name}</span>
        </Badge>
      ))}
      {rest.length > 0 && (
        <Badge
          variant="outline"
          className="shrink-0 text-xs font-normal"
          title={rest.map((l) => l.name).join(", ")}
        >
          +{rest.length}
        </Badge>
      )}
    </span>
  );
}
