// RiskFactorRow (C30T3): one contributing factor — label + detail on the
// left, ratio + mini bar on the right, drill-down link underneath.
//
// Deviation from the prototype: the drill-down link is always visible.
// The prototype reveals it on row hover, which keyboard and screen-reader
// users can never reach; the app's other drill-downs (Workload) are
// always-visible links too.

import { Link } from "react-router-dom";
import {
  riskDrilldownHref,
  riskDrilldownLabel,
  type ProjectRisk,
  type RiskFactor,
} from "../../lib/risk";

export default function RiskFactorRow({
  slug,
  project,
  factor,
  color = "#4f8ef7",
}: {
  slug: string;
  project: ProjectRisk;
  factor: RiskFactor;
  /** Bar color — the card passes its level color (prototype behavior). */
  color?: string;
}) {
  const href = riskDrilldownHref(slug, project, factor.key);
  const label = riskDrilldownLabel(factor.key);
  return (
    <div
      data-testid="risk-factor-row"
      data-factor-key={factor.key}
      className="grid grid-cols-[1fr_auto] gap-x-4 border-b border-border-hairline py-2.5 last:border-b-0"
    >
      <div>
        <div className="text-[12.5px] font-medium text-ink">
          {factor.label}
        </div>
        <div className="mt-0.5 text-[11.5px] text-ink-muted">
          {factor.detail}
        </div>
      </div>
      <div className="flex flex-col items-end justify-center gap-1.5">
        <span className="text-[11.5px] text-ink-secondary tabular-nums">
          {factor.value.toFixed(3)}
        </span>
        <div className="h-[5px] w-[140px] overflow-hidden rounded-full bg-wash">
          <div
            className="h-full rounded-full"
            style={{
              width: `${Math.min(100, Math.max(0, factor.value * 100))}%`,
              backgroundColor: color,
            }}
          />
        </div>
      </div>
      {href && label && (
        <Link
          to={href}
          className="col-span-2 mt-0.5 text-[11px] text-[#4f8ef7] hover:text-[#649cf5] hover:underline"
        >
          {label} &rarr;
        </Link>
      )}
    </div>
  );
}
