# Flake watch

Known flaky Go tests, their status, and how to catch new ones before they bite.

## Known flakes

| Test | First seen | Status | Notes |
| --- | --- | --- | --- |
| `TestFindSimilarIssuesPerf` | cycle 16 | **FIXED** (cycle 21) | Time-window-sensitive: the trigram similarity query evaluated `similarity()` twice per row, so identical inputs scored differently across runs. Fixed by materializing the score once per row (`WITH scored AS MATERIALIZED (...)` in `internal/service/issue_similar.go`), so the ORDER BY, threshold, and output all reference one deterministic score per row. |
| `TestDispatcherSingleFlight` | cycle 21 | **FIXED** (cycle 22, C22T0) | Test bug, not a prod race: the outbox table is shared with parallel test packages and `claimDue` takes the 25 oldest due rows, so under parallel load the pass claimed foreign rows while the test asserted on its own row (the `started` signal fired on a Send, not on *which* row). Fixed by scoping assertions to explicit signals — started channel, recorded send count, second-Run skip via goroutine+timeout (the old `elapsed > 5s` check could never catch the regression; the run would hang, not return slowly). `go vet`/`-race`/5× full-suite green. |
| `TestGetWorkspaceActivityAggregates` | cycle 22 | suspected | Failed once in a parallel full-suite run (strict timestamp-descending assertion under load); green in isolation and on package re-run. |
| `TestActivityHTTPLimit` / `TestActivityHTTPFeed` | cycle 22 | suspected | Intermittent 401s under parallel full-suite load (`activity: status = 401` right after the same cookie returned 200); green 5/5 in isolation. Load-dependent, unrelated to the mail singleflight fix (no shared code path). |

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
