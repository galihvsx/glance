// ProjectFilterChips (C29T1): workspace project filter for the Workload
// page. Selected project ids are stored in the `project` URL query param
// (repeatable), matching the GET /workload contract.

import { Button } from "../ui/button";
import { cn } from "cn";
import type { Project } from "../../lib/types";

export default function ProjectFilterChips({
  projects,
  selected,
  onChange,
}: {
  projects: Project[];
  selected: string[];
  onChange: (ids: string[]) => void;
}) {
  const toggle = (id: string) => {
    onChange(
      selected.includes(id)
        ? selected.filter((s) => s !== id)
        : [...selected, id],
    );
  };

  return (
    <div
      className="flex flex-wrap items-center gap-1.5"
      role="group"
      aria-label="Filter workload by project"
    >
      <Button
        type="button"
        size="sm"
        variant={selected.length === 0 ? "secondary" : "ghost"}
        aria-pressed={selected.length === 0}
        onClick={() => onChange([])}
      >
        All projects
      </Button>
      {projects.map((p) => {
        const active = selected.includes(p.id);
        return (
          <Button
            key={p.id}
            type="button"
            size="sm"
            variant={active ? "secondary" : "ghost"}
            aria-pressed={active}
            onClick={() => toggle(p.id)}
            className={cn("font-mono text-[11px]")}
          >
            {p.identifier}
          </Button>
        );
      })}
    </div>
  );
}
