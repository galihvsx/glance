/** Maps a workflow state group to the Cycle 19 `.state-dot` modifier class
 *  (T11 state-dot system, prototype "State dot system"). The API reports
 *  groups as: triage | backlog | unstarted | started | completed | cancelled.
 *
 *  Prototype mapping: Backlog/Todo hollow, In Progress dashed, Done filled,
 *  Cancelled slashed.
 */
export function stateDotClass(group: string | undefined | null): string {
  switch (group) {
    case "completed":
      return "state-dot--done";
    case "cancelled":
      return "state-dot--cancelled";
    case "started":
      return "state-dot--in-progress";
    case "unstarted":
      return "state-dot--todo";
    case "triage":
    case "backlog":
    default:
      return "state-dot--backlog";
  }
}
