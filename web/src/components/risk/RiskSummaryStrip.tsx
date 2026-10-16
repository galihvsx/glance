// RiskSummaryStrip (C30T3): the four-cell summary strip — project counts
// per risk level, prototype layout (dot + label + band + count).

import { RISK_LEVEL_META, type ProjectRisk, type RiskLevel } from "../../lib/risk";

const LEVELS: RiskLevel[] = ["healthy", "watch", "at-risk", "critical"];

export default function RiskSummaryStrip({
  projects,
}: {
  projects: ProjectRisk[];
}) {
  return (
    <section
      aria-label="Risk summary"
      className="grid grid-cols-2 gap-2 md:grid-cols-4"
    >
      {LEVELS.map((level) => {
        const meta = RISK_LEVEL_META[level];
        const count = projects.filter((p) => p.risk_level === level).length;
        return (
          <div
            key={level}
            data-testid={`risk-summary-${level}`}
            className="flex items-center gap-2.5 rounded-[10px] border border-border-hairline bg-surface px-3.5 py-3"
          >
            <span
              aria-hidden="true"
              className="size-2 shrink-0 rounded-full"
              style={{ backgroundColor: meta.color }}
            />
            <div className="flex flex-col">
              <span className="text-xs font-medium text-ink-secondary">
                {meta.label}
              </span>
              <span className="mt-0.5 text-[10.5px] text-ink-muted">
                {meta.band}
              </span>
            </div>
            <span className="ml-auto text-lg font-semibold tabular-nums">
              {count}
            </span>
          </div>
        );
      })}
    </section>
  );
}
