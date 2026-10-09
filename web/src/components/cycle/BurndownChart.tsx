import { useMemo } from "react";
import { layoutBurndown } from "../../lib/burndown";
import type { CycleBurndown } from "../../lib/types";
import { Card, CardContent, CardHeader, CardTitle } from "../ui/card";

function pointsToPath(pts: { x: number; y: number }[]): string {
  return pts.map((p, i) => `${i === 0 ? "M" : "L"}${p.x.toFixed(1)},${p.y.toFixed(1)}`).join(" ");
}

/**
 * Hand-rolled SVG burndown: remaining scope (solid) vs the ideal
 * straight line (dashed). No chart library — theme-aware via Tailwind
 * stroke/fill utilities, responsive through the viewBox.
 */
export default function BurndownChart({ data }: { data: CycleBurndown }) {
  const layout = useMemo(
    () => layoutBurndown(data.days, data.total_scope),
    [data],
  );
  const { width, height } = layout;
  const lastActual = layout.actual[layout.actual.length - 1];

  return (
    <Card>
      <CardHeader className="pb-2">
        <div className="flex items-center gap-4">
          <CardTitle className="text-sm font-medium">Burndown</CardTitle>
          <span className="ml-auto flex items-center gap-4 text-xs text-muted-foreground">
            <span className="flex items-center gap-1.5">
              <span className="inline-block h-0.5 w-5 bg-primary" />
              Remaining
            </span>
            <span className="flex items-center gap-1.5">
              <span className="inline-block h-0 w-5 border-t-2 border-dashed border-muted-foreground" />
              Ideal
            </span>
          </span>
        </div>
      </CardHeader>
      <CardContent>
        <svg
          viewBox={`0 0 ${width} ${height}`}
          className="h-auto w-full"
          role="img"
          aria-label={`Burndown chart: ${data.total_scope} issues in scope, ${lastActual ? `${data.days.filter((d) => d.remaining != null).length} days tracked` : "not started"}`}
        >
          {layout.yTicks.map((t) => (
            <g key={t.label}>
              <line
                x1={36}
                x2={width - 12}
                y1={t.y}
                y2={t.y}
                className="stroke-border"
                strokeWidth={1}
              />
              <text
                x={30}
                y={t.y + 4}
                textAnchor="end"
                fontSize={11}
                className="fill-muted-foreground"
              >
                {t.label}
              </text>
            </g>
          ))}
          {layout.xTicks.map((t) => (
            <text
              key={t.label + t.x}
              x={t.x}
              y={height - 8}
              textAnchor="middle"
              fontSize={11}
              className="fill-muted-foreground"
            >
              {t.label}
            </text>
          ))}
          <path
            d={pointsToPath(layout.ideal)}
            fill="none"
            className="stroke-muted-foreground"
            strokeWidth={1.5}
            strokeDasharray="5 4"
          />
          {layout.actual.length > 0 && (
            <>
              <path
                d={pointsToPath(layout.actual)}
                fill="none"
                className="stroke-primary"
                strokeWidth={2}
              />
              {layout.actual.map((p, i) => (
                <circle
                  key={i}
                  cx={p.x}
                  cy={p.y}
                  r={3}
                  className="fill-primary"
                />
              ))}
            </>
          )}
        </svg>
      </CardContent>
    </Card>
  );
}
