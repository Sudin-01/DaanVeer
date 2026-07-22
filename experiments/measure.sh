#!/usr/bin/env bash
# Regenerate every in-process measurement, each in its own test process.
#
# Isolation is the point of this script. A timing test that runs after the
# rest of the suite inherits its heap, its garbage collector state and its
# BadgerDB page cache, and reports different numbers as a result. Each
# experiment therefore gets a dedicated `go test` invocation with -run naming
# only that experiment, which is also the condition under which the harness
# agrees to write its CSV at all.
#
# Usage:
#   ./experiments/measure.sh            # full sweeps, slow (roughly 15 minutes)
#   ./experiments/measure.sh quick      # short sweeps, for checking the plumbing
#
# The multi-node experiments (E2, E3, E6) need a running testbed and are not
# started here; see experiments/testbed.sh.

set -uo pipefail
cd "$(dirname "$0")/.."

MODE="${1:-full}"
LOGDIR="${DAANVEER_LOGDIR:-/d/daanveer-logs}"
mkdir -p "$LOGDIR" 2>/dev/null || LOGDIR="$(mktemp -d)"
STAMP=$(date +%Y%m%d_%H%M%S)
LOG="$LOGDIR/measure_${MODE}_${STAMP}.log"

if [ "$MODE" = "quick" ]; then
  E4_MAX=2000;  E11_BLOCKS=1000; E11_HOLDERS=500;  TIMEOUT=30m
else
  E4_MAX=50000; E11_BLOCKS=5000; E11_HOLDERS=2000; TIMEOUT=120m
fi

echo "mode=$MODE  log=$LOG"
echo "e4-max=$E4_MAX  e11-blocks=$E11_BLOCKS  e11-holders=$E11_HOLDERS"
echo

run() {
  local name="$1"; shift
  echo "--- $name"
  {
    echo "############################################################"
    echo "# $name"
    echo "# $(date --iso-8601=seconds)"
    echo "# $*"
    echo "############################################################"
    "$@"
    echo "# exit=$?"
    echo
  } >> "$LOG" 2>&1
}

# Environment recorded once, because a measurement without its machine is not
# reproducible.
{
  echo "############################################################"
  echo "# environment"
  echo "############################################################"
  echo "date:  $(date --iso-8601=seconds)"
  echo "go:    $(go version)"
  echo "commit: $(git rev-parse HEAD 2>/dev/null || echo unknown)"
  echo "dirty:  $(git status --porcelain 2>/dev/null | wc -l) modified files"
  echo "os:     ${OS:-unknown}"
  echo
} > "$LOG"

# The clock profile comes first: every other figure is a difference of two
# clock readings, so the reader needs to know what the clock can resolve
# before reading any of them.
run "E12 clock resolution and quantisation" \
  go test ./blockchain/ -run TestE12 -v -timeout "$TIMEOUT"

run "E4 balance index vs full scan (to $E4_MAX blocks)" \
  go test ./blockchain/ -run TestE4 -v -e4-max="$E4_MAX" -timeout "$TIMEOUT"

run "E11 fund tracing (to $E11_BLOCKS blocks, $E11_HOLDERS holders)" \
  go test ./blockchain/ -run TestE11 -v \
    -e11-blocks="$E11_BLOCKS" -e11-holders="$E11_HOLDERS" -timeout "$TIMEOUT"

run "E5 storage cost" \
  go test ./blockchain/ -run TestE5 -v -timeout "$TIMEOUT"

run "E7 liveness under absent validators" \
  go test ./blockchain/ -run TestE7 -v -timeout "$TIMEOUT"

run "E9 cryptographic microbenchmarks" \
  go test ./blockchain/ -run '^$' -bench . -benchmem -benchtime 2s -timeout "$TIMEOUT"

run "statistics self-validation against scipy" \
  go test ./blockchain/ -run TestStats -v -timeout "$TIMEOUT"

echo
echo "=== summary ==="
grep -E "^(--- )?(PASS|FAIL|ok|# exit)" "$LOG" | grep -v "exit=0" || true
if grep -q "^FAIL" "$LOG"; then
  echo "SOME EXPERIMENTS FAILED -- see $LOG"
  exit 1
fi
echo "all experiments completed; full log at $LOG"
echo "regenerate figures with: python docs/paper/figures/make_figures.py"
