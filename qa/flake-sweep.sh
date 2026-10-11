#!/usr/bin/env bash
# qa/flake-sweep.sh — catch flaky Go tests by running the suite repeatedly.
#
# Usage: ./qa/flake-sweep.sh [RUNS] [PKG ...]
#   RUNS   number of full-suite passes (default 3)
#   PKG    go package pattern(s), default ./... (repo root)
#
# Requires TEST_DATABASE_URL in the environment (same convention as the rest
# of the backend dev workflow — see the "Development" section of README.md).
# Runs with `go test -count=1` so nothing is cached between passes, reports
# per-run wall times and every failing test, and exits non-zero if ANY run
# failed. Per-run logs go to /tmp/flake-sweep-<pid>/run-<i>.log.
#
# Example:
#   export TEST_DATABASE_URL='postgres://glance:<password>@127.0.0.1:5432/glance_test?sslmode=disable'
#   ./qa/flake-sweep.sh 3 ./...

set -euo pipefail

# Anchor to the repo root so go test ./... resolves regardless of caller CWD.
cd "$(dirname "$0")/.."

RUNS="${1:-3}"
shift || true
if [[ $# -gt 0 ]]; then
  PKGS=("$@")
else
  PKGS=(./...)
fi

if [[ ! "$RUNS" =~ ^[0-9]+$ ]] || [[ "$RUNS" -lt 1 ]]; then
  echo "flake-sweep: RUNS must be a positive integer, got '$RUNS'" >&2
  exit 2
fi

: "${TEST_DATABASE_URL:?flake-sweep: TEST_DATABASE_URL must be exported (backend dev convention — see README.md \"Development\").}"

LOGDIR="/tmp/flake-sweep-$$"
mkdir -p "$LOGDIR"

total_failures=0
summary=()

echo "flake-sweep: $RUNS run(s) of 'go test -count=1 ${PKGS[*]}'"
echo "flake-sweep: logs -> $LOGDIR"

for ((i = 1; i <= RUNS; i++)); do
  log="$LOGDIR/run-$i.log"
  start=$(date +%s)
  # Capture failures without tripping `set -e`; classify run outcome below.
  set +e
  go test -count=1 "${PKGS[@]}" >"$log" 2>&1
  rc=$?
  set -e
  end=$(date +%s)
  elapsed=$((end - start))

  if [[ $rc -eq 0 ]]; then
    status="PASS"
  else
    status="FAIL"
    total_failures=$((total_failures + 1))
  fi

  failing=$(grep -h '^--- FAIL:' "$log" 2>/dev/null | sed 's/^--- FAIL: *//' | tr '\n' ' ' | sed 's/ *$//' || true)
  if [[ -n "$failing" ]]; then
    printf 'run %d/%d: %s (%ds) — failures: %s\n' "$i" "$RUNS" "$status" "$elapsed" "$failing"
  else
    printf 'run %d/%d: %s (%ds)\n' "$i" "$RUNS" "$status" "$elapsed"
  fi
  summary+=("run $i: $status (${elapsed}s)")
done

echo '---'
printf '%s\n' "${summary[@]}"
echo '---'

if [[ "$total_failures" -gt 0 ]]; then
  echo "flake-sweep: $total_failures/$RUNS run(s) FAILED — see $LOGDIR/run-<i>.log for details" >&2
  exit 1
fi
echo "flake-sweep: all $RUNS run(s) passed"
