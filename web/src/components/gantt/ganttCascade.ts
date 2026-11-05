import { addDays, barSpan, daysBetween } from "../../lib/gantt";
import type { IssueLink } from "../../lib/types";

/** Dependency cascade on Gantt move/resize/schedule (C146T1, C157T1).
 *
 *  Pure functions, no React, no api, no query client — the same
 *  precedent as `ganttLinks.ts`'s pure helpers. The wiring task
 *  (C146T2) calls `computeCascade` at drag commit (and, for the
 *  ghost preview, during the drag) and PATCHes one
 *  presence-preserving payload per returned shift.
 *
 *  Binding semantics (cycle 146 plan, rules 1–6; cycle 157, rule 9):
 *  1. Only `blocks` edges cascade; `relates_to` / `duplicates`
 *     never move anything.
 *  2. Direction: source blocks target ⇒ target (and its transitive
 *     dependents) shift. Never the reverse.
 *  3. Forward-only, violation-driven: k = the dragged issue's end
 *     delta in days (move: dayDelta; resize-end: newEnd − oldEnd;
 *     resize-start: end unchanged ⇒ k = 0 ⇒ NO cascade, ever).
 *     If k ≤ 0 the cascade is empty — moving a blocker earlier only
 *     grows gaps, which violates nothing.
 *  4. Uniform shift, gap-preserving: every shifted dependent moves by
 *     exactly k days (both dates, duration preserved). A dependent
 *     shifts only if, after its blockers' shifts, it would violate
 *     finish-to-start: `dependent.start < blockerFinalEnd + 1 day`,
 *     evaluated against ALL of its shifted blockers (diamond-safe).
 *     Pre-existing violations are preserved, not repaired: a
 *     dependent that already violated before the drag shifts by k
 *     only when the drag's push reaches it, so its original overlap
 *     is unchanged — both sides move by k.
 *  5. Transitive, BFS order, cycle-safe: shifts are returned in
 *     breadth-first discovery order from the dragged issue; a
 *     visited set guarantees termination even if a cycle ever slips
 *     past the backend guard. Evaluation runs to a fixpoint over
 *     the reachable set (a dependent is re-checked until every one
 *     of its in-cascade blockers has a final end), which is what
 *     makes the diamond case — including a deeper blocker reaching
 *     a shallower dependent — evaluate against all shifted
 *     blockers rather than first-visited only.
 *  6. Dateless dependents are skipped, not shifted (nothing to
 *     shift); propagation through a skipped node stops on that path
 *     because its end did not move. Dependents outside the fetched
 *     set are skipped: only issues in `issues` can shift, so the
 *     cascade covers the visible set only.
 *  7. Presence-preserving shifts (caller-side): the engine returns
 *     every shift in span form (both dates resolved via `barSpan`:
 *     a single-date dependent shifts as a 1-day span). The caller
 *     maps this to a presence-preserving PATCH payload: send
 *     `start_date` only if the issue originally had one,
 *     `target_date` only if it originally had one — a single-date
 *     dependent stays single-date.
 *  8. Commit (caller-side): the dragged PATCH and one PATCH per
 *     shift fire together (`Promise.allSettled`), with the move
 *     path's success/failure toasts and `["gantt"]` invalidation.
 *  9. Schedule mode (C157): the dragged issue may be dateless —
 *     there is no old end and no uniform k. Its final span is the
 *     1-day span [d, d] (`newStart` == `newEnd` == d, the drop
 *     day). A dependent B violates iff `B.start <
 *     blockerFinalEnd + 1 day` (same predicate as rule 4) and
 *     shifts by the MINIMUM that clears it: `s_B =
 *     (blockerFinalEnd + 1) − B.start` days, applied to both
 *     dates, duration preserved. Shifts are per-dependent, not
 *     uniform, and transitive dependents use the same rule
 *     against their shifted blockers' final ends. A dependent's
 *     shift may GROW across passes — a second, deeper blocker
 *     may shift later and demand more — so schedule mode
 *     re-evaluates shifted nodes until no shift grows: a node's
 *     shift is the max over its shifted blockers of
 *     `(blockerFinalEnd + 1 − originalStart)`, its final end
 *     recomputed from that shift each pass. Growth demanded by a
 *     blocker that is itself a transitive dependent of the node
 *     (a cycle back-edge) is ignored — otherwise a B↔C cycle
 *     would feed its own shift back forever; each node in a cycle
 *     still takes its FIRST shift from every shifted blocker, so
 *     both sides of a cycle shift once by the minimum. A
 *     defensive pass cap (`bfsOrder.length + 1`) bounds the loop
 *     on any input; a DAG fixpoint converges within that bound. */

/** Minimal issue shape the cascade needs: id + stored dates. The
 *  fetched `Issue` objects satisfy this structurally. */
export interface CascadeIssue {
  id: string;
  start_date?: string | null;
  target_date?: string | null;
}

/** One dependent's new span, in span form (both dates resolved via
 *  `barSpan`: a single-date dependent shifts as a 1-day span). The
 *  caller maps this to a presence-preserving PATCH payload (rule 7):
 *  send `start_date` only if the issue originally had one,
 *  `target_date` only if it originally had one — a single-date
 *  dependent stays single-date. */
export interface CascadeShift {
  id: string;
  start_date: string;
  target_date: string;
}

/** Drag mode, mirroring the Gantt drag state (`Gantt.tsx`): "move"
 *  shifts both dates, "resize-end" moves only the end,
 *  "resize-start" moves only the start (end unchanged ⇒ no cascade),
 *  "schedule" places a dateless issue on one day (its final span is
 *  the 1-day span [newStart, newEnd]) and cascades per rule 9. */
export type CascadeMode = "move" | "resize-start" | "resize-end" | "schedule";

/** Compute the cascade shifts caused by dragging/resizing/scheduling
 *  one issue.
 *
 *  `issues` is the flat fetched Gantt set (dates as stored);
 *  `links` is the fetched link list. `newStart` / `newEnd` are the
 *  dragged issue's prospective span days ("YYYY-MM-DD", already
 *  resolved through `barSpan` by the caller, exactly as the drag
 *  code computes them; in "schedule" mode both are the drop day).
 *  Returns the shifts in BFS order from the dragged issue, each
 *  carrying the dependent's new span. The dragged issue itself is
 *  never in the result. */
export function computeCascade(
  issues: readonly CascadeIssue[],
  links: readonly IssueLink[],
  draggedId: string,
  newStart: string,
  newEnd: string,
  mode: CascadeMode,
): CascadeShift[] {
  // Rule 3: resize-start never cascades (its end is unchanged).
  if (mode === "resize-start") return [];

  const byId = new Map(issues.map((i) => [i.id, i]));
  const dragged = byId.get(draggedId);
  if (!dragged) return [];

  // Rule 9: in schedule mode the dragged issue may be dateless —
  // skip the old-end early-return for this mode only. Its final
  // span comes from newStart / newEnd (both the drop day).
  // Move/resize keep the existing early-returns and loop untouched.
  const isSchedule = mode === "schedule";
  let k = 0;
  if (!isSchedule) {
    // The dragged issue's old end is its resolved span end (barSpan
    // semantics: a single-date bar ends on its one date).
    const draggedSpan = barSpan(dragged.start_date, dragged.target_date);
    if (draggedSpan.start === null || draggedSpan.end === null) return [];

    // Rule 3: k = end delta; k ≤ 0 ⇒ empty cascade.
    k = daysBetween(draggedSpan.end, newEnd);
    if (k <= 0) return [];
  }
  void newStart; // The shift amount depends only on the end delta.

  // Rule 1 + 2: blocks-only adjacency, blocker → dependents, over
  // the fetched set only (rule 6: outside-set dependents are skipped).
  const dependentsOf = new Map<string, string[]>();
  const blockersOf = new Map<string, string[]>();
  for (const link of links) {
    if (link.kind !== "blocks") continue;
    if (!byId.has(link.issue_id) || !byId.has(link.target_issue_id)) continue;
    const deps = dependentsOf.get(link.issue_id) ?? [];
    deps.push(link.target_issue_id);
    dependentsOf.set(link.issue_id, deps);
    const blockers = blockersOf.get(link.target_issue_id) ?? [];
    blockers.push(link.issue_id);
    blockersOf.set(link.target_issue_id, blockers);
  }

  // Rule 5: BFS discovery from the dragged issue (visited set ⇒
  // cycle-safe). The dragged issue itself is visited, never shifted.
  const visited = new Set<string>([draggedId]);
  const bfsOrder: string[] = [];
  const queue: string[] = [draggedId];
  while (queue.length > 0) {
    const current = queue.shift()!;
    for (const dep of dependentsOf.get(current) ?? []) {
      if (visited.has(dep)) continue;
      visited.add(dep);
      bfsOrder.push(dep);
      queue.push(dep);
    }
  }
  if (bfsOrder.length === 0) return [];

  // Resolved original spans; null ⇒ dateless (rule 6: skipped).
  const originalSpan = new Map<string, { start: string; end: string }>();
  for (const id of bfsOrder) {
    const span = barSpan(byId.get(id)!.start_date, byId.get(id)!.target_date);
    if (span.start !== null && span.end !== null) {
      originalSpan.set(id, { start: span.start, end: span.end });
    }
  }

  const shifted = new Set<string>([draggedId]);
  const finalEnd = new Map<string, string>([[draggedId, newEnd]]);
  const newSpan = new Map<string, { start: string; end: string }>();

  if (isSchedule) {
    // Rule 9: per-dependent minimal shifts. A dependent's required
    // shift is the max over its shifted blockers of
    // (blockerFinalEnd + 1 − originalStart); a node's shift may
    // GROW across passes when a deeper blocker shifts later, so
    // re-evaluate shifted nodes until no shift grows. Growth
    // demanded by a blocker that is itself a transitive dependent
    // of the node (a cycle back-edge) is ignored — otherwise a
    // B↔C cycle would feed its own shift back forever. The first
    // shift still counts every shifted blocker, so both sides of
    // a cycle shift once by the minimum. The pass cap is a
    // defensive bound only: a DAG fixpoint converges within
    // longest-path passes (≤ bfsOrder.length).
    const reachable = new Map<string, Set<string>>();
    for (const id of bfsOrder) {
      const seen = new Set<string>();
      const pending = [...(dependentsOf.get(id) ?? [])];
      while (pending.length > 0) {
        const current = pending.shift()!;
        if (seen.has(current)) continue;
        seen.add(current);
        for (const dep of dependentsOf.get(current) ?? []) {
          if (!seen.has(dep)) pending.push(dep);
        }
      }
      reachable.set(id, seen);
    }
    const shiftDays = new Map<string, number>();
    let changed = true;
    let passes = 0;
    const maxPasses = bfsOrder.length + 1;
    while (changed && passes < maxPasses) {
      changed = false;
      passes++;
      for (const id of bfsOrder) {
        const span = originalSpan.get(id);
        if (!span) continue; // Dateless: skipped, never a shifted blocker.
        const alreadyShifted = shifted.has(id);
        let required = 0;
        for (const blocker of blockersOf.get(id) ?? []) {
          if (!shifted.has(blocker)) continue;
          if (alreadyShifted && reachable.get(id)!.has(blocker)) continue;
          const demand = daysBetween(span.start, addDays(finalEnd.get(blocker)!, 1));
          if (demand > required) required = demand;
        }
        if (required <= 0) continue;
        if (required <= (shiftDays.get(id) ?? 0)) continue;
        shifted.add(id);
        shiftDays.set(id, required);
        const start = addDays(span.start, required);
        const end = addDays(span.end, required);
        newSpan.set(id, { start, end });
        finalEnd.set(id, end);
        changed = true;
      }
    }
  } else {
    // Rule 4 + 5: shift a dependent by exactly k iff ANY of its
    // shifted blockers violates finish-to-start against it
    // (dependent.start < blockerFinalEnd + 1 day). Every shifted
    // blocker moved by k, so its final end is its original end + k
    // (the dragged issue's final end is newEnd = oldEnd + k). Repeat
    // to a fixpoint so a dependent is judged against ALL of its
    // shifted blockers, not just the ones shifted before its first
    // check; a dateless node never enters `shifted`, so propagation
    // through it stops (rule 6).
    let changed = true;
    while (changed) {
      changed = false;
      for (const id of bfsOrder) {
        if (shifted.has(id)) continue;
        const span = originalSpan.get(id);
        if (!span) continue; // Dateless: skipped, never a shifted blocker.
        const violates = (blockersOf.get(id) ?? []).some((blocker) => {
          if (!shifted.has(blocker)) return false;
          const blockerEnd = finalEnd.get(blocker)!;
          return span.start < addDays(blockerEnd, 1);
        });
        if (!violates) continue;
        shifted.add(id);
        const start = addDays(span.start, k);
        const end = addDays(span.end, k);
        newSpan.set(id, { start, end });
        finalEnd.set(id, end);
        changed = true;
      }
    }
  }

  // Rule 5: shifts out in BFS order.
  const out: CascadeShift[] = [];
  for (const id of bfsOrder) {
    const span = newSpan.get(id);
    if (span) out.push({ id, start_date: span.start, target_date: span.end });
  }
  return out;
}
