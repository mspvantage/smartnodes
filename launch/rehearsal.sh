#!/usr/bin/env bash
# Rehearses a test network launch on one machine, with the launch kit:
# four validators, each in its own folder and ports, as four separate people
# would run them.
#
#   1. The coordinator writes the settings and builds the draft genesis.
#   2. Each validator makes its signed genesis transaction against it.
#   3. The coordinator checks them and builds the final genesis.
#   4. All four start from it and make blocks; the chain's settings are
#      checked against testnet.json.
#   5. One validator stops; the other three carry on. It comes back and
#      catches up.
#
# Needs smartnodesd and jq. Uses ports 46000-46499 and ~/launch-rehearsal;
# stops everything it started. About two minutes.
set -uo pipefail
BIN=${BIN:-smartnodesd}
KIT=$(cd "$(dirname "$0")" && pwd)
T=~/launch-rehearsal
CHAIN=smartnodes-rehearsal-1
PASS=0; FAIL=0; PIDS=()
step() { echo; echo "=================================================================="; echo "$1"; echo "=================================================================="; }
check() { if eval "$2"; then echo "  PASS  $1"; PASS=$((PASS+1)); else echo "  FAIL  $1"; FAIL=$((FAIL+1)); fi; }
cleanup() { for p in "${PIDS[@]}"; do kill "$p" 2>/dev/null; done; sleep 1; echo; echo "== stopped the rehearsal network; ~/launch-rehearsal kept for a look"; }
trap cleanup EXIT
rpc() { echo "tcp://127.0.0.1:46${1}57"; }
height() { $BIN status --node "$(rpc "$1")" 2>/dev/null | jq -r '.sync_info.latest_block_height // 0' 2>/dev/null || echo 0; }

command -v jq >/dev/null && command -v "$BIN" >/dev/null || { echo "needs jq and smartnodesd"; exit 1; }
rm -rf "$T" && mkdir -p "$T"
cp "$KIT/build-genesis.sh" "$T/"

step "1. The coordinator: settings and draft genesis"
for i in 1 2 3 4; do
  $BIN init "validator-$i" --chain-id $CHAIN --home "$T/v$i" >/dev/null 2>&1
  $BIN keys add val --keyring-backend test --home "$T/v$i" >/dev/null 2>&1
done
$BIN keys add faucet --keyring-backend test --home "$T/coord" >/dev/null 2>&1
$BIN keys add issuer --keyring-backend test --home "$T/coord" >/dev/null 2>&1
addr() { $BIN keys show "$1" -a --keyring-backend test --home "$2"; }
jq --arg c $CHAIN --arg f "$(addr faucet "$T/coord")" --arg i "$(addr issuer "$T/coord")" \
   --arg a1 "$(addr val "$T/v1")" --arg a2 "$(addr val "$T/v2")" --arg a3 "$(addr val "$T/v3")" --arg a4 "$(addr val "$T/v4")" '
    .chain_id = $c
  | .accounts = [{address: $f, coins: "5000000000000usnode"}, {address: $i, coins: "100000000usnode"}]
  | .validators = [$a1, $a2, $a3, $a4 | {address: ., coins: "10000000000usnode"}]
  ' "$KIT/testnet.json" > "$T/testnet.json"
(cd "$T" && bash build-genesis.sh draft)
check "the draft genesis file was made" "[ -f $T/out/genesis-draft.json ]"
check "it holds the 6 starting balances" "[ \$(jq '.app_state.bank.balances | length' $T/out/genesis-draft.json) = 6 ]"

step "2. Each validator makes its genesis transaction"
mkdir -p "$T/gentx"
for i in 1 2 3 4; do
  cp "$T/out/genesis-draft.json" "$T/v$i/config/genesis.json"
  $BIN genesis gentx val 5000000000usnode --chain-id $CHAIN --home "$T/v$i" --keyring-backend test \
    --moniker "validator-$i" --commission-rate 0.10 --commission-max-rate 0.20 --commission-max-change-rate 0.01 >/dev/null 2>&1
  cp "$T"/v$i/config/gentx/*.json "$T/gentx/v$i.json"
done
check "4 genesis transactions came back" "[ \$(ls $T/gentx | wc -l) = 4 ]"

step "3. The coordinator checks them and builds the final genesis"
# A greedy one first: it must be refused.
jq '.body.messages[0].commission.max_rate = "0.250000000000000000"' "$T/gentx/v4.json" > "$T/greedy.json"
mkdir -p "$T/gentx-greedy" && cp "$T/gentx/v1.json" "$T/greedy.json" "$T/gentx-greedy/"
(cd "$T" && bash build-genesis.sh final --gentx gentx-greedy > greedy.log 2>&1)
check "a transaction with a maximum commission above 20% is refused" "grep -q 'above 20%' $T/greedy.log"
(cd "$T" && bash build-genesis.sh final)
check "the final genesis has 4 genesis transactions" "[ \$(jq '.app_state.genutil.gen_txs | length' $T/out/genesis.json) = 4 ]"
check "its checksum file matches" "(cd $T/out && sha256sum -c --status genesis.json.sha256)"

step "4. All four start from the final genesis"
ID() { $BIN comet show-node-id --home "$T/v$1"; }
for i in 1 2 3 4; do cp "$T/out/genesis.json" "$T/v$i/config/genesis.json"; done
for i in 1 2 3 4; do
  peers=""
  for j in 1 2 3 4; do [ $j = $i ] || peers="$peers,$(ID $j)@127.0.0.1:46${j}56"; done
  sed -i -e 's|^allow_duplicate_ip = .*|allow_duplicate_ip = true|' -e 's|^addr_book_strict = .*|addr_book_strict = false|' \
         -e 's|^timeout_commit = .*|timeout_commit = "1s"|' "$T/v$i/config/config.toml"
  $BIN config set app minimum-gas-prices 0.025usnode --home "$T/v$i" >/dev/null
  $BIN start --home "$T/v$i" --p2p.laddr "tcp://127.0.0.1:46${i}56" --p2p.persistent_peers "${peers#,}" \
    --rpc.laddr "$(rpc $i)" --proxy_app "tcp://127.0.0.1:46${i}58" --rpc.pprof_laddr "127.0.0.1:46${i}60" \
    --grpc.address "127.0.0.1:46${i}90" --api.enable=false > "$T/v$i.log" 2>&1 &
  PIDS+=($!)
done
for _ in $(seq 1 60); do [ "$(height 1)" -ge 5 ] && break; sleep 1; done
echo "  height: $(height 1)"
check "the network makes blocks" "[ \$(height 1) -ge 5 ]"
VALS=$($BIN query staking validators --node "$(rpc 1)" -o json 2>/dev/null | jq '[.validators[] | select(.status == "BOND_STATUS_BONDED")] | length')
check "4 validators are bonded (found $VALS)" "[ \"$VALS\" = 4 ]"
P() { $BIN query "$@" --node "$(rpc 1)" -o json 2>/dev/null; }
check "at most 30 validators" "[ \"\$(P staking params | jq -r '.params.max_validators')\" = 30 ]"
check "minimum commission 5%" "P staking params | jq -e '(.params.min_commission_rate | tonumber) == 0.05' >/dev/null"
check "flat 2% a year new issue" "P mint params | jq -e '(.params.inflation_min | tonumber) == 0.02 and (.params.inflation_max | tonumber) == 0.02' >/dev/null"
check "hard cap of 100,000,000 SNODE" "[ \"\$(P mint params | jq -r '.params.max_supply')\" = 100000000000000 ]"
check "governance votes last an hour" "P gov params | jq -r '.params.voting_period' | grep -Eq '^(3600s|1h0m0s)$'"
check "everything is in usnode" "[ \"\$(P staking params | jq -r '.params.bond_denom')\" = usnode ]"

step "5. One validator stops; the others carry on"
kill "${PIDS[3]}"; sleep 1
H=$(height 1); sleep 6
echo "  height went from $H to $(height 1) with validator 4 stopped"
check "three of four keep making blocks" "[ \$(height 1) -gt $H ]"
i=4; peers=""; for j in 1 2 3; do peers="$peers,$(ID $j)@127.0.0.1:46${j}56"; done
$BIN start --home "$T/v4" --p2p.laddr "tcp://127.0.0.1:46456" --p2p.persistent_peers "${peers#,}" \
  --rpc.laddr "$(rpc 4)" --proxy_app "tcp://127.0.0.1:46458" --rpc.pprof_laddr "127.0.0.1:46460" \
  --grpc.address "127.0.0.1:46490" --api.enable=false >> "$T/v4.log" 2>&1 &
PIDS+=($!)
for _ in $(seq 1 30); do [ "$(height 4)" -ge "$(height 1)" ] && break; sleep 1; done
echo "  validator 4 at $(height 4), validator 1 at $(height 1)"
check "the stopped validator comes back and catches up" "[ \$(( \$(height 1) - \$(height 4) )) -le 2 ]"

echo
echo "=================================================================="
echo "Launch rehearsal: $PASS passed, $FAIL failed."
