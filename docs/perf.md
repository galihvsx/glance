# glance v1 performance budgets (Task 27)

Measured 2026-10-06. All five v1 budgets **pass** on this hardware.

## Environment

- VM: 2 vCPU, PostgreSQL 16.15 on 127.0.0.1 (same host, `sslmode=disable`)
- Binary: `go build ./cmd/glance` (no special flags), Go 1.27.1
- Real configuration throughout — no `synchronous_commit=off` or other
  safety-reducing tricks. The test database is the project's real
  `glance_test` schema with all 16 migrations applied.

## Budgets vs measured

Corpus: 500 issues in one project, seeded through the real service
`CreateIssue` path (sequence upsert, activity row, tsvector maintenance),
so the data looks exactly like production rows. Each budget is p95 over
60 timed iterations after 5 warmup iterations
(`internal/perf/perf_test.go` — assertions FAIL the suite if a budget
is missed).

| Budget (brief)        | Measured p50 | Measured p95 | Measured max | Verdict |
|-----------------------|--------------|--------------|--------------|---------|
| list p95 < 100ms      | 1.62ms       | 3.54ms       | 4.89ms       | ✅ pass |
| detail p95 < 300ms    | 0.33ms       | 0.42ms       | 0.86ms       | ✅ pass |
| search p95 < 200ms    | 2.55ms       | 3.19ms       | 6.59ms       | ✅ pass |
| binary cold start < 2s | —           | —            | 0.14s        | ✅ pass |
| idle RSS < 256MB      | —            | —            | 14.9MB       | ✅ pass |

- **list**: `ListIssues` default order (`-updated_at`, 25/page), no filters.
- **detail**: `GetIssue` (one row + `json_agg` assignees/labels + description),
  cycling 10 issue IDs to avoid measuring a single hot cache line.
- **search**: `ListIssues{q: "latency"}` where the term matches **all 500
  issues** — the worst case for the tsvector filter (every row matches,
  then sort + limit).
- **cold start**: process exec → first `GET /health` → 200, measured over
  4 runs (0.061s–0.138s; worst 0.138s reported). Page cache was warm
  from the immediately preceding run — `drop_caches` is not permitted
  in this sandbox, so treat it as a warm-cache number; with 14×
  headroom the verdict is unaffected.
- **idle RSS**: `VmRSS` from `/proc/<pid>/status` after 30s idle serving
  `/health` (VmHWM identical at 14.9MB — the process never exceeded it).
  The 30s window included one mail-dispatcher tick draining a leftover
  outbox row from the notify tests (real production behavior).

## Where the numbers are measured

The Go budget assertions run at the **service layer** (the `service.*`
call), not through HTTP. Rationale, verified by measurement:

- These endpoints are DB-bound; the service call *is* the query plus
  JSON aggregation.
- The HTTP stack above it (Echo routing + `RequireAuth` middleware)
  was spot-checked at **0.625ms** per `GET /health` against the release
  binary — two orders of magnitude below the tightest budget.

So the service-layer p95 is the honest number, and the HTTP overhead
is documented rather than hand-waved.

## EXPLAIN ANALYZE findings → `000016_perf_indexes`

The one real finding, on the default list order (`-updated_at`):

**Before** — bitmap scan + sort over every live issue in the project:

```
Limit  (cost=98.97..99.04 rows=26)
  ->  Sort  (Sort Key: updated_at DESC, id DESC; top-N heapsort)
        ->  Bitmap Heap Scan on issues
              ->  Bitmap Index Scan on issues_project_idx
```

Correct at 500 rows, but O(n log n) in project size — the sort walks
every live issue before LIMIT.

**After** (`000016_perf_indexes`):

```sql
CREATE INDEX issues_list_default_idx
    ON issues (project_id, updated_at DESC, id DESC)
    WHERE deleted_at IS NULL;
```

```
Limit  (cost=0.28..11.91 rows=26)
  ->  Index Only Scan using issues_list_default_idx
        Index Cond: (project_id = $1)
```

No sort step; 26 heap fetches; list latency is O(per_page) as projects
grow. The partial predicate mirrors the list query's
`deleted_at IS NULL` filter exactly.

**Deliberately not added**: composites for the other `order_by` values
(`created_at`, `sequence_id`, `sort_order`, `priority`). They keep the
bitmap-plus-sort plan, which is fine at realistic project sizes, and
each extra composite adds write amplification on the hot `issues`
table (`updated_at` changes on every issue update). Add when a workload
proves need.

**Checked, no action needed**:

- `priority` / `sort_order` are `NOT NULL` — the Task 15 review's
  NULL-sort-key cursor concern is moot.
- Search uses the planner's choice between `issues_project_idx`
  (bitmap) and `issues_search_idx` (GIN) per query; with a
  project-scoped filter the bitmap + tsvector filter is optimal, and
  the GIN index stands ready for selective terms. Nothing missing.
- `GetIssue` is a PK lookup; assignee/label `json_agg` subqueries ride
  the junction-table PKs (`issue_id` first column). `issue_activities`,
  `comments`, `notifications` indexes from earlier migrations cover the
  detail satellites.

## Reproducing

```sh
export PATH=/home/hatch/workspace/.gotoolchain/go/bin:$PATH
export GOCACHE=/home/hatch/workspace/.gocache GOTMPDIR=/home/hatch/workspace/.gocache GOTOOLCHAIN=local
export TEST_DATABASE_URL='postgres://glance:glance@127.0.0.1:5432/glance_test?sslmode=disable'
go test ./internal/perf/ -v -count=1
```

Cold start / RSS (release binary):

```sh
go build -o /tmp/glance-bin ./cmd/glance
DATABASE_URL='<conn>' PORT=18099 /tmp/glance-bin &
# poll GET /health until 200 → exec-to-first-200 is cold start
sleep 30; grep VmRSS /proc/<pid>/status   # idle RSS
```
