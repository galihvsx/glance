# Workload & capacity

The **Workload view** answers one question: who has too much open work?
For every workspace member it aggregates open assigned issues (counts,
estimate points, overdue, priority buckets), a 14-day completion
velocity, and an **overloaded** flag against that member's **weekly
capacity**. Unassigned open issues are counted per project so triage can
see where slack sits.

## Capacity

`weekly_capacity_points` is a per-member weekly budget in **story
points** (estimate points), set via:

`PUT /api/v1/workspaces/{slug}/members/{userId}/capacity` (member 15+)

```json
{ "weekly_capacity_points": 8 }
```

- `null` (explicit) clears the capacity back to **unset**; the field is
  optional in the body only insofar as `null` is a valid value — an
  absent field is a 400.
- Valid values: integers **0–999** (else 400); the database CHECK
  enforces the same bounds.
- `null` and `0` both mean **"no budget tracked"** — such members are
  never flagged overloaded, not "over budget at zero".
- Any workspace member (guest 5+) can read the workload report; only
  members 15+ can write capacities.
- Cross-workspace member ids are a 404 — tenancy is enforced in the
  service layer, never leaked.

## The overloaded flag

```
overloaded = weekly_capacity_points != nil
             && weekly_capacity_points > 0
             && open_points > weekly_capacity_points
```

`open_points` is the sum of estimate-point values on open assigned
issues (unestimated issues count 0). "Open" means live
(`deleted_at`/`archived_at` NULL) and not in a `completed`/`cancelled`
state. The check is strictly greater: exactly at capacity is fine.

## Capacity-overload alerts

Once a day (on the daily ticker pass) every overloaded member gets one
in-app notification (`capacity.overloaded`) — **at most one per member
per calendar day**, deduped by the `capacity_alerts` table. The alert is
the member's own signal: it honors notification prefs (default in-app
on, email off).

The alert rows also feed the **weekly email digest**: weekly digests
gain a "Capacity overloads" section covering the trailing 7 days of
alerts. Daily digests intentionally exclude it — the per-day in-app
alert already covers daily delivery, so including it would re-surface
the same signal twice.

## Endpoints

- `GET /api/v1/workspaces/{slug}/workload[?project=<uuid>...]` — the
  full report (members + per-project unassigned counts). Any member may
  read. `?project` may repeat to scope counting; a malformed id is a
  400, a project from another workspace is a 404.
- `PUT /api/v1/workspaces/{slug}/members/{userId}/capacity` — set/clear
  capacity (member 15+).

## Honest v1 limitations

- **Points, not hours.** Capacity is measured in estimate points, not
  time. A member with 8 pts of trivial bugs looks identical to one with
  8 pts of hard work — the flag is a signal, not a schedule.
- **One alert per day.** A member who stays overloaded all week gets one
  notification per day, not per check-in. The dedupe key is a calendar
  day (UTC), claimed under a conditional upsert so concurrent passes
  never double-notify.
- **14-day velocity is a simple count.** `completed_14d` counts issues
  whose state transitioned *into* a done group in the last 14 days (from
  the `issue_activities` audit log). An issue created directly in a done
  state has no transition row and does not count; point-weighting is not
  applied.
- **Single instance.** The daily pass runs in-process (spec §3 known
  limitation: one glance instance in v1).
- **Capacity is per workspace.** The same person in two workspaces has
  independent capacities and independent overload checks.
