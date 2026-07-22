#!/usr/bin/env bash
# DaanVeer multi-node testbed.
#
# Launches N nodes on one host, each with its own database, wallet and ports,
# all sharing one chain configuration and peer list.
#
#   ./experiments/testbed.sh up 3      # start a 3-node network
#   ./experiments/testbed.sh status    # show each node's height
#   ./experiments/testbed.sh down      # stop everything
#   ./experiments/testbed.sh clean     # stop and delete all state
#
# Node i (1-based) uses:
#   API port      8080 + (i-1)*10
#   p2p port      8081 + (i-1)*10
#   state         experiments/.testbed/node<i>/
#
# Requires: go, curl. Runs under Git Bash on Windows and any POSIX shell.

set -uo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

# Node state (databases, logs) lives outside the repository so it is never
# committed, and on a roomy volume: BadgerDB preallocates per node, and a
# multi-node run on a nearly-full system disk fails with "not enough space".
# Override with DAANVEER_TESTBED_DIR.
default_testbed_dir() {
  if [[ -n "${DAANVEER_TESTBED_DIR:-}" ]]; then
    echo "$DAANVEER_TESTBED_DIR"
  elif [[ -d /d ]]; then
    echo "/d/daanveer-testbed"
  else
    echo "$REPO_ROOT/experiments/.testbed"
  fi
}

TESTBED="$(default_testbed_dir)"
BIN="$TESTBED/daanveer"
[[ "$OSTYPE" == msys* || "$OSTYPE" == cygwin* || "$OSTYPE" == win32 ]] && BIN="$BIN.exe"

# The wallet passphrase is a test credential for a throwaway local network.
export DAANVEER_WALLET_KEY="${DAANVEER_WALLET_KEY:-testbed-local-network-key}"
export DAANVEER_NODE_ADDRESS="${DAANVEER_NODE_ADDRESS:-127.0.0.1}"
export DAANVEER_QUIET_DB=1
# Load tests need a genesis grant large enough to fund the whole run.
export DAANVEER_GENESIS_AMOUNT="${DAANVEER_GENESIS_AMOUNT:-100000000}"
export GIN_MODE=release

api_port() { echo $((8080 + ($1 - 1) * 10)); }
p2p_port() { echo $((8081 + ($1 - 1) * 10)); }
node_dir() { echo "$TESTBED/node$1"; }

build() {
  echo "building..."
  ( cd "$REPO_ROOT" && go build -o "$BIN" . ) || { echo "build failed"; exit 1; }
}

# Wait until a node answers, or give up.
wait_ready() {
  local port=$1 tries=${2:-50}
  for _ in $(seq "$tries"); do
    if curl -sf --max-time 1 "http://127.0.0.1:$port/my-wallet/balance" >/dev/null 2>&1; then
      return 0
    fi
    sleep 0.2
  done
  return 1
}

up() {
  local n=${1:-3}
  down >/dev/null 2>&1
  mkdir -p "$TESTBED"
  build

  echo "provisioning $n node(s)..."
  local shared_config="$TESTBED/chain.json"

  # Wipe previous node state. `up` regenerates the chain configuration, so a
  # retained database would belong to a different genesis -- silently mixing
  # two chains and invalidating any measurement taken afterwards.
  rm -f "$shared_config"
  rm -rf "$TESTBED"/node*

  # Node 1 bootstraps the chain: its wallet is the genesis recipient and the
  # first validator. Every other node adds itself as a validator.
  for i in $(seq "$n"); do
    local dir; dir=$(node_dir "$i")
    mkdir -p "$dir/config"
    if [[ $i -eq 1 ]]; then
      "$BIN" -init -config "$shared_config" -wallet "$dir/wallet.txt" >/dev/null || exit 1
    else
      "$BIN" -add-validator -config "$shared_config" -wallet "$dir/wallet.txt" >/dev/null || exit 1
    fi
  done

  # Optional consensus parameters, swept by the experiments:
  #   QUORUM=k            require k distinct validator signatures (0 = ceil(2N/3))
  #   REQUIRE_SCHEDULE=1  enforce the round-robin proposer schedule
  if [[ -n "${QUORUM:-}" || -n "${REQUIRE_SCHEDULE:-}" ]]; then
    python - "$shared_config" "${QUORUM:-0}" "${REQUIRE_SCHEDULE:-0}" <<'PY'
import json, sys
path, quorum, schedule = sys.argv[1], int(sys.argv[2]), sys.argv[3] not in ("", "0")
with open(path) as fh:
    cfg = json.load(fh)
if quorum:
    cfg["quorum"] = quorum
cfg["require_schedule"] = schedule
with open(path, "w") as fh:
    json.dump(cfg, fh, indent=2)
    fh.write("\n")
PY
    echo "  quorum=${QUORUM:-auto} require_schedule=${REQUIRE_SCHEDULE:-0}"
  fi

  # One peer list, shared. Entry order matters: the first is the implicit
  # coordinator for transaction relay.
  local peers="  \"nodes\": ["
  for i in $(seq "$n"); do
    peers="$peers\n    \"127.0.0.1:$(p2p_port "$i")\""
    [[ $i -lt $n ]] && peers="$peers,"
  done
  peers="$peers\n  ]"

  for i in $(seq "$n"); do
    local dir; dir=$(node_dir "$i")
    cp "$shared_config" "$dir/config/chain.json"
    printf "{\n$peers\n}\n" > "$dir/config/knownNodes.json"
  done

  echo "starting..."
  for i in $(seq "$n"); do
    local dir; dir=$(node_dir "$i")
    (
      cd "$dir" || exit 1
      DAANVEER_P2P_PORT=$(p2p_port "$i") \
      DAANVEER_PEERS="$dir/config/knownNodes.json" \
        "$BIN" -config "$dir/config/chain.json" \
               -wallet "$dir/wallet.txt" \
               -db "$dir/db" \
               -port "$(api_port "$i")" \
               > "$dir/node.log" 2>&1 &
      echo $! > "$dir/pid"
    )
  done

  for i in $(seq "$n"); do
    if wait_ready "$(api_port "$i")"; then
      echo "  node$i ready on $(api_port "$i") (p2p $(p2p_port "$i"))"
    else
      echo "  node$i FAILED to start; see $(node_dir "$i")/node.log"
    fi
  done
  echo "$n" > "$TESTBED/count"
}

status() {
  local n; n=$(cat "$TESTBED/count" 2>/dev/null || echo 0)
  [[ $n -eq 0 ]] && { echo "no testbed running"; return 1; }
  for i in $(seq "$n"); do
    local port; port=$(api_port "$i")
    local block; block=$(curl -sf --max-time 2 "http://127.0.0.1:$port/block/last" 2>/dev/null)
    if [[ -z "$block" ]]; then
      echo "node$i ($port): DOWN"
    else
      local height hash balance
      height=$(echo "$block" | sed -n 's/.*"height":\([0-9]*\).*/\1/p')
      hash=$(echo "$block" | sed -n 's/.*"block_hash":"\([0-9a-f]\{12\}\).*/\1/p')
      balance=$(curl -sf --max-time 2 "http://127.0.0.1:$port/my-wallet/balance" 2>/dev/null \
                | sed -n 's/.*"balance":\([0-9]*\).*/\1/p')
      echo "node$i ($port): height=$height tip=$hash… balance=$balance"
    fi
  done
}

down() {
  local n; n=$(cat "$TESTBED/count" 2>/dev/null || echo 0)
  for i in $(seq "$n"); do
    local pidfile; pidfile="$(node_dir "$i")/pid"
    [[ -f "$pidfile" ]] && kill "$(cat "$pidfile")" 2>/dev/null
    rm -f "$pidfile"
  done
  # Belt and braces on Windows, where the shell PID is not the process PID.
  if command -v taskkill >/dev/null 2>&1; then
    taskkill //F //IM daanveer.exe >/dev/null 2>&1
  else
    pkill -f "$BIN" 2>/dev/null
  fi
  rm -f "$TESTBED/count"
  echo "stopped"
}

clean() {
  down >/dev/null 2>&1
  rm -rf "$TESTBED"
  echo "testbed removed"
}

case "${1:-}" in
  up)     up "${2:-3}" ;;
  status) status ;;
  down)   down ;;
  clean)  clean ;;
  *)      echo "usage: $0 {up [n]|status|down|clean}"; exit 1 ;;
esac
