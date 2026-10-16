// RiskLevelBadge (C30T3): the per-project risk level badge — prototype's
// tinted-bg / colored-text pill, on the shadcn Badge primitive.

import { RISK_LEVEL_META, type RiskLevel } from "../../lib/risk";
import { Badge } from "../ui/badge";

export default function RiskLevelBadge({ level }: { level: RiskLevel }) {
  const meta = RISK_LEVEL_META[level];
  return (
    <Badge
      variant="outline"
      data-testid="risk-level-badge"
      data-level={level}
      style={{
        backgroundColor: meta.bg,
        color: meta.color,
        borderColor: "transparent",
      }}
    >
      {meta.label}
    </Badge>
  );
}
