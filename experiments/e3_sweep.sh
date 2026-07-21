#!/usr/bin/env bash
# E3 -- consensus latency against quorum size.
#
#   ./experiments/e3_sweep.sh [nodes] [rounds]
#
# The quorum is part of the chain configuration, so each point in the sweep
# requires restarting the network. Results accumulate in
# experiments/results/e3_quorum.csv.

set -uo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
NODES=${1:-5}
ROUNDS=${2:-20}
CSV="$REPO_ROOT/experiments/results/e3_quorum.csv"

rm -f "$CSV"
mkdir -p "$(dirname "$CSV")"

PASSES=${3:-3}
WARMUP=${WARMUP:-10}

echo "E3: quorum sweep over $NODES nodes, $ROUNDS rounds x $PASSES passes, warmup $WARMUP"
echo

# Visit quorum sizes in a different random order each pass, and discard warmup
# rounds within each configuration.
#
# A first attempt ran quorums in ascending order once each, with no warmup. The
# result was non-monotonic, and reversing the order reversed the ranking: the
# configuration measured first was always the slowest. Start-up transient, not
# quorum size, was being measured. Randomised order spreads that transient
# across conditions instead of aligning it with one.
for pass in $(seq "$PASSES"); do
  echo "### pass $pass"
  for quorum in $(seq 1 "$NODES" | shuf); do
    echo "=== quorum $quorum of $NODES (pass $pass) ==="
    QUORUM=$quorum "$REPO_ROOT/experiments/testbed.sh" up "$NODES" >/dev/null 2>&1
    sleep 3
    ( cd "$REPO_ROOT" && go run ./experiments/e3 \
        -rounds "$ROUNDS" -warmup "$WARMUP" -nodes "$NODES" \
        -out "$CSV" -label "q${quorum}p${pass}" ) 2>&1 | tail -2
    echo
  done
done

"$REPO_ROOT/experiments/testbed.sh" down >/dev/null 2>&1

echo "=== summary ==="
python - "$CSV" <<'PY'
import csv, statistics, sys, collections

by = collections.defaultdict(list)
with open(sys.argv[1]) as fh:
    for row in csv.DictReader(fh):
        by[(int(row["nodes"]), int(row["quorum"]))].append(float(row["consensus_ms"]))

print(f"{'nodes':>6}{'quorum':>8}{'n':>5}{'min':>9}{'median':>9}{'mean':>9}{'sd':>9}{'max':>9}")
print("-" * 64)
for (nodes, quorum) in sorted(by):
    v = by[(nodes, quorum)]
    sd = statistics.stdev(v) if len(v) > 1 else 0.0
    print(f"{nodes:>6}{quorum:>8}{len(v):>5}{min(v):>9.2f}"
          f"{statistics.median(v):>9.2f}{statistics.mean(v):>9.2f}{sd:>9.2f}{max(v):>9.2f}")
print("\nAll figures in milliseconds (consensus round only).")
PY
