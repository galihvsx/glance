# Risk radar

The **Risk view** answers one question: which projects are at risk?
For every workspace project it computes a heuristic **risk score**
(0–100), a **risk level** (healthy / watch / at-risk / critical), and
the **top contributing factors** behind the score. Endpoint:

`GET /api/v1/workspaces/{slug}/risk` — any workspace member (guest 5+)
may read. Response:

```json
{
  "projects": [
    {
      "project_id": "uuid",
      "identifier": "ATLAS",
      "name": "Atlas",
      "open_count": 12,
      "risk_score": 37,
      "risk_level": "watch",
      "factors": [
        { "key": "overdue", "label": "Overdue issues", "value": 0.25,
          "detail": "3 of 12 open issues overdue" }
      ],
      "generated_at": "2026-10-13T01:55:00Z"
    }
  ],
  "generated_at": "2026-10-13T01:55:00Z"
}
```

## Formula (v1, fixed weights)

```
risk_score = round(35 × overdue_ratio
                 + 25 × stalled_share
                 + 20 × unassigned_share
                 + 20 × overload_share)        clamped to 0–100
```

Each ratio is in [0, 1]:

| Factor         | Key          | Ratio                                                          |
| -------------- | ------------ | -------------------------------------------------------------- |
| Overdue        | `overdue`    | overdue open issues / open issues                              |
| Stalled        | `stalled`    | open issues with no activity in 7 days / open issues           |
| Unassigned     | `unassigned` | open issues with no assignee / open issues                    |
| Overload       | `overload`   | overloaded members / all workspace members                    |

Definitions:

- **Open** = live (not deleted, not archived) and not in a done state
  group (`completed` / `cancelled`) — the same definition as
  c29's workload aggregation.
- **Overdue** = `target_date` before today on an open issue (the schema
  has no `due_date`; analytics and workload use `target_date` the same
  way).
- **Stalled** = the issue's latest activity (`MAX(created_at)` over the
  `issue_activities` audit log; `issues.updated_at` when the issue has
  no activity rows) is older than 7 days. The `issue_activities` table
  is append-only (no delete path), so this is reliable history.
- **Overload** = the c29 `GetWorkload` `overloaded` flag, reused
  read-only (never re-implemented): a member with a positive weekly
  capacity whose open estimate points exceed it. The denominator is
  **all workspace members**, including those with no capacity set.

Factors are emitted **descending by score contribution** (weight ×
ratio); a factor with zero contribution is omitted, so a fully healthy
project has an empty `factors` array. `value` is the ratio rounded to
3 decimals; `detail` is the human breakdown (e.g. `"2 of 5 members
overloaded"`).

**Levels:** 0–24 healthy, 25–49 watch, 50–74 at-risk, 75–100 critical.
Projects with **zero open issues** score 0 (healthy) with empty factors.

## Implementation notes

- Tenancy: every query is scoped to the workspace id; unknown
  workspace → `ErrNotFound` → 404. Cross-workspace rows never leak.
- No N+1: exactly **7 batched queries** regardless of project/issue
  volume — 1 workspace existence check, 1 per-project aggregation query
  (open/overdue/stalled/unassigned counts for the whole workspace),
  and the constant 5-query `GetWorkload` call (existence, project scope,
  members, 14-day velocity, unassigned) for the overload flags.
- No migration in v1: the report aggregates existing tables only
  (`projects`, `issues`, `issue_activities`, `issue_assignees`,
  `workspace_members`).

## Honest v1 limitations

1. **Heuristic composite, not a prediction.** The score summarizes four
   observable risk signals with fixed v1 weights; it does not predict
   delivery outcomes. Weights are not user-tunable (custom risk weights
   are out of scope for v1).
2. **Staleness keyed on activity.** Bulk no-op updates bump
   `updated_at` (and may write activity rows), resetting staleness for
   issues that are not actually moving.
3. **Overload share depends on capacity being set.** Members without a
   weekly capacity never trip the overloaded flag (per the workload
   contract: `null` or `0` capacity = "no budget tracked"), so a
   workspace where nobody sets capacities always reports
   `overload` = 0 — the denominator is still all members.
4. **No historical risk series.** Scores are computed live from current
   state; there is no time series of past scores in v1 (no new
   persistence — add only if the formula proves stable).
5. **Archived projects are included** with whatever open issues they
   hold at query time; the report does not filter on project archived
   state (a deliberate v1 simplification — archived projects typically
   hold zero open issues and score healthy).
