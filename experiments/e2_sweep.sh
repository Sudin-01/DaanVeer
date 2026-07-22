#!/usr/bin/env bash
# E2 -- throughput and latency against offered load.
#
#   ./experiments/e2_sweep.sh [duration] [passes]
#
# Sweeps concurrent donation clients to locate the saturation knee, and repeats
# the sweep with the pre-index balance scan so the two are compared on identical
# hardware in the same session.
#
# Results accumulate in experiments/results/e2_throughput.csv.

set -uo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DURATION=${1:-15s}
PASSES=${2:-1}
CSV="$REPO_ROOT/experiments/results/e2_throughput.csv"
WORKERS=${WORKERS:-"1 2 4 8 16 32"}

rm -f "$CSV"
mkdir -p "$(dirname "$CSV")"

TMPDIR_E2="${TMPDIR:-/tmp}/daanveer-e2"
mkdir -p "$TMPDIR_E2"

run_sweep() {
  local mode="$1"
  for pass in $(seq "$PASSES"); do
    for w in $WORKERS; do
      "$REPO_ROOT/experiments/testbed.sh" up 1 >/dev/null 2>&1
      sleep 3
      # Output goes to a file rather than through a pipe. An earlier version
      # piped into `tail`, and points went missing from the sweep -- six of
      # twelve runs produced no row. Redirecting keeps each run independent
      # and makes a failure visible instead of silently absent.
      local log="$TMPDIR_E2/e2_${mode}_${w}_${pass}.log"
      ( cd "$REPO_ROOT" && go run ./experiments/e2 \
          -workers "$w" -duration "$DURATION" -warmup 5s \
          -label "$mode" -out "$CSV" ) > "$log" 2>&1
      printf "%-8s workers=%-3s pass=%-2s %s\n" "$mode" "$w" "$pass" \
        "$(grep -E '^throughput' "$log" || echo 'FAILED -- see '"$log")"
    done
  done
}

echo "E2: load sweep, $DURATION per point, $PASSES pass(es), workers: $WORKERS"
echo

# Indexed balances -- the current implementation.
unset DAANVEER_BALANCE_SCAN
run_sweep "indexed"

# Full-chain scan -- the pre-index behaviour, for the before/after comparison.
export DAANVEER_BALANCE_SCAN=1
run_sweep "scan"
unset DAANVEER_BALANCE_SCAN

"$REPO_ROOT/experiments/testbed.sh" down >/dev/null 2>&1

echo "=== summary ==="
python - "$CSV" <<'PY'
import csv, sys, collections, statistics

rows = collections.defaultdict(list)
with open(sys.argv[1]) as fh:
    for r in csv.DictReader(fh):
        rows[(r["label"], int(r["workers"]))].append(r)

print(f"{'mode':<9}{'workers':>8}{'tps':>10}{'median_ms':>11}{'p95_ms':>10}{'committed':>11}{'rejected':>10}")
print("-" * 69)
for key in sorted(rows, key=lambda k: (k[0], k[1])):
    mode, workers = key
    group = rows[key]
    tps = statistics.mean(float(r["tps"]) for r in group)
    med = statistics.mean(float(r["median_ms"]) for r in group)
    p95 = statistics.mean(float(r["p95_ms"]) for r in group)
    committed = sum(int(r["committed"]) for r in group)
    rejected = sum(int(r["rejected"]) for r in group)
    print(f"{mode:<9}{workers:>8}{tps:>10.1f}{med:>11.1f}{p95:>10.1f}{committed:>11}{rejected:>10}")

print()
best = {}
for (mode, workers), group in rows.items():
    tps = statistics.mean(float(r["tps"]) for r in group)
    if mode not in best or tps > best[mode][1]:
        best[mode] = (workers, tps)
for mode, (workers, tps) in sorted(best.items()):
    print(f"peak {mode}: {tps:.1f} tx/s at {workers} workers")
if "indexed" in best and "scan" in best and best["scan"][1] > 0:
    print(f"\nindexed / scan at peak: {best['indexed'][1] / best['scan'][1]:.1f}x")
PY
