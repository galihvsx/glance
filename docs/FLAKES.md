# Flake watch

Known flaky Go tests, their status, and how to catch new ones before they bite.

## Known flakes

| Test | First seen | Status | Notes |
| --- | --- | --- | --- |
| `TestFindSimilarIssuesPerf` | cycle 16 | **FIXED** (cycle 21) | Time-window-sensitive: the trigram similarity query evaluated `similarity()` twice per row, so identical inputs scored differently across runs. Fixed by materializing the score once per row (`WITH scored AS MATERIALIZED (...)` in `internal/service/issue_similar.go`), so the ORDER BY, threshold, and output all reference one deterministic score per row. |
| `TestDispatcherSingleFlight` | cycle 21 | **FIXED** (cycle 22, C22T0) | Test bug, not a prod race: the outbox table is shared with parallel test packages and `claimDue` takes the 25 oldest due rows, so under parallel load the pass claimed foreign rows while the test asserted on its own row (the `started` signal fired on a Send, not on *which* row). Fixed by scoping assertions to explicit signals — started channel, recorded send count, second-Run skip via goroutine+timeout (the old `elapsed > 5s` check could never catch the regression; the run would hang, not return slowly). `go vet`/`-race`/5× full-suite green. |
| `TestGetWorkspaceActivityAggregates` | cycle 22 | **FIXED** (cycle 23, C23T1) | Shared-DB interference: parallel test packages shared one `glance_test` database, so another package's activity rows landed inside the aggregation window and broke the strict timestamp-descending assertion under load. Root cause removed by the per-package test databases (`internal/testdb`): each package now gets its own freshly-migrated DB. Verified by `qa/flake-sweep.sh` 3/3 green on api+auth+service. |
| `TestActivityHTTPLimit` / `TestActivityHTTPFeed` | cycle 22 | **FIXED** (cycle 23, C23T1) | Shared-DB interference: the intermittent 401s under parallel full-suite load came from session/cookie rows in the shared `glance_test` database being visible to (or clobbered by) parallel packages. Root cause removed by the per-package test databases (`internal/testdb`) — api/auth/service each run against their own DB. Verified by `qa/flake-sweep.sh` 3/3 green on api+auth+service. |
| `TestGetTimeSummary` | cycle 24 | **FIXED** (cycle 24, coordinator fixup) | Time-of-day test bug, not a prod issue: the test logged its "today" entry as `now-2h → now-1h` off `time.Now().UTC()`, so runs between 00:00–02:00 UTC straddled the UTC day boundary and produced 3 non-zero day buckets instead of the asserted 2. Fixed by pinning the test clock to noon UTC (`Truncate(24h).Add(12h)`), keeping all entries inside their intended days and the 7-day window. |

Status values: `suspected` (seen fail once, not reproduced) → `confirmed` (reproduced by the sweep) → `fixed` (root-caused and patched) / `under investigation` (assigned task, in progress).

## Running the flake sweep

`qa/flake-sweep.sh` runs the Go suite N times with caching off and reports
per-run wall times plus every failing test. It exits non-zero if any run fails.

```bash
# from the repo root; TEST_DATABASE_URL follows the usual backend dev convention
export TEST_DATABASE_URL=<redacted>
./qa/flake-sweep.sh            # 3 full runs of go test ./...
./qa/flake-sweep.sh 5 ./internal/store   # 5 runs, scoped to one package
```

Per-run logs land in `/tmp/flake-sweep-<pid>/run-<i>.log`.

**Rule:** when a flake is confirmed by the sweep, it gets a row in the table
above in the same cycle — status starts at `confirmed` and moves to `fixed`
(or `under investigation` with the owning task ID) before the cycle closes.
No cycle closes with an undocumented known flake.
