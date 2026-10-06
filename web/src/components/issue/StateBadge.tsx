import type { IssueState } from "../../lib/types";
import { Badge } from "../ui/badge";

/** State badge: colored dot (from state.color) + state name. */
export default function StateBadge({ state }: { state: IssueState }) {
  return (
    <Badge variant="secondary" className="gap-1.5">
      <span
        className="h-2 w-2 rounded-full"
        style={{ backgroundColor: state.color }}
        aria-hidden
      />
      {state.name}
    </Badge>
  );
}
