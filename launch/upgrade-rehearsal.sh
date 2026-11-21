#!/usr/bin/env bash
# Rehearses a chain upgrade on a running network, the way the test network
# will do it:
#
#   1. Four validators start on the released v0.1.0, each under Cosmovisor,
#      from a genesis made by the launch kit.
#   2. Some state is made: a VPN node is registered, coins are sent.
#   3. A governance proposal schedules upgrade v0.2.0 at a set height, and
#      the validators vote yes. Each puts the new version where Cosmovisor
#      looks for it.
#   4. At that height the chain stops, every Cosmovisor switches to v0.2.0,
#      and the chain carries on, with every balance, validator and node.
#
# Run it from the project folder, with the upgrade code (patch 0029) in it:
#   bash launch/upgrade-rehearsal.sh
# Needs Go, jq, curl and cosmovisor. Uses ports 47000-47499 and
# ~/upgrade-rehearsal; stops everything it started. About four minutes.
set -uo pipefail
REPO=$(cd "$(dirname "$0")/.." && pwd)
T=~/upgrade-rehearsal
CHAIN=smartnodes-upgrade-1
OLD_VERSION=v0.1.0
UPGRADE=v0.2.0
KR="--keyring-backend test"
PASS=0; FAIL=0; PIDS=()
step() { echo; echo "=================================================================="; echo "$1"; echo "=================================================================="; }
check() { if eval "$2"; then echo "  PASS  $1"; PASS=$((PASS+1)); else echo "  FAIL  $1"; FAIL=$((FAIL+1)); fi; }
cleanup() { for p in "${PIDS[@]}"; do kill "$p" 2>/dev/null; done; pkill -f "home $T/v" 2>/dev/null; sleep 1; echo; echo "== stopped the rehearsal network; ~/upgrade-rehearsal kept for a look"; }
trap cleanup EXIT
rpc() { echo "tcp://127.0.0.1:47${1}57"; }
height() { "$OLD" status --node "$(rpc "$1")" 2>/dev/null | jq -r '.sync_info.latest_block_height // 0' 2>/dev/null || echo 0; }

for tool in go jq curl cosmovisor; do command -v $tool >/dev/null || { echo "$tool is needed"; exit 1; }; done
rm -rf "$T" && mkdir -p "$T/old" "$T/new"

step "0. The two versions"
ARCH=$(dpkg --print-architecture 2>/dev/null || echo amd64)
NAME="smartnodes_${OLD_VERSION}_linux_${ARCH}"
curl -fsSL -o "$T/old.tgz" "https://github.com/mspvantage/smartnodes/releases/download/$OLD_VERSION/$NAME.tar.gz" || { echo "could not download $OLD_VERSION"; exit 1; }
tar -xzf "$T/old.tgz" -C "$T/old" --strip-components=1
OLD="$T/old/bin/smartnodesd"
(cd "$REPO" && go build -ldflags "-X github.com/cosmos/cosmos-sdk/version.Name=smartnodes -X github.com/cosmos/cosmos-sdk/version.AppName=smartnodesd -X github.com/cosmos/cosmos-sdk/version.Version=$UPGRADE" -o "$T/new/" ./cmd/smartnodesd) || { echo "the new version did not build"; exit 1; }
NEW="$T/new/smartnodesd"
echo "  old: $($OLD version)   new: $($NEW version)"
check "the released version and the new one are both here" "[ \"\$($OLD version)\" = $OLD_VERSION ] && [ \"\$($NEW version)\" = $UPGRADE ]"

step "1. Four validators start on $OLD_VERSION, under Cosmovisor"
cp "$REPO/launch/build-genesis.sh" "$T/"
for i in 1 2 3 4; do
  $OLD init "validator-$i" --chain-id $CHAIN --home "$T/v$i" >/dev/null 2>&1
  $OLD keys add val $KR --home "$T/v$i" >/dev/null 2>&1
done
addr() { $OLD keys show val -a $KR --home "$T/v$1"; }
jq --arg c $CHAIN --arg a1 "$(addr 1)" --arg a2 "$(addr 2)" --arg a3 "$(addr 3)" --arg a4 "$(addr 4)" '
    .chain_id = $c | .accounts = []
  | .validators = [$a1, $a2, $a3, $a4 | {address: ., coins: "10000000000usnode"}]
  | .genesis_overrides.app_state.gov.params.voting_period = "20s"
  | .genesis_overrides.app_state.gov.params.expedited_voting_period = "10s"
  ' "$REPO/launch/testnet.json" > "$T/testnet.json"
(cd "$T" && bash build-genesis.sh draft --binary "$OLD" >/dev/null) || exit 1
mkdir -p "$T/gentx"
for i in 1 2 3 4; do
  cp "$T/out/genesis-draft.json" "$T/v$i/config/genesis.json"
  $OLD genesis gentx val 5000000000usnode --chain-id $CHAIN --home "$T/v$i" $KR --moniker "validator-$i" \
    --commission-rate 0.10 --commission-max-rate 0.20 --commission-max-change-rate 0.01 >/dev/null 2>&1
  cp "$T"/v$i/config/gentx/*.json "$T/gentx/v$i.json"
done
(cd "$T" && bash build-genesis.sh final --binary "$OLD" >/dev/null) || { echo "genesis failed"; exit 1; }
ID() { $OLD comet show-node-id --home "$T/v$1"; }
start() {
  local i=$1 peers=""
  for j in 1 2 3 4; do [ "$j" = "$i" ] || peers="$peers,$(ID "$j")@127.0.0.1:47${j}56"; done
  DAEMON_NAME=smartnodesd DAEMON_HOME="$T/v$i" DAEMON_RESTART_AFTER_UPGRADE=true DAEMON_ALLOW_DOWNLOAD_BINARIES=false UNSAFE_SKIP_BACKUP=true \
    cosmovisor run start --home "$T/v$i" --p2p.laddr "tcp://127.0.0.1:47${i}56" --p2p.persistent_peers "${peers#,}" \
    --rpc.laddr "$(rpc "$i")" --proxy_app "tcp://127.0.0.1:47${i}58" --rpc.pprof_laddr "127.0.0.1:47${i}60" \
    --grpc.address "127.0.0.1:47${i}90" --api.enable=false >> "$T/v$i.log" 2>&1 &
  PIDS+=($!)
}
for i in 1 2 3 4; do
  cp "$T/out/genesis.json" "$T/v$i/config/genesis.json"
  sed -i -e 's|^allow_duplicate_ip = .*|allow_duplicate_ip = true|' -e 's|^addr_book_strict = .*|addr_book_strict = false|' \
         -e 's|^timeout_commit = .*|timeout_commit = "1s"|' "$T/v$i/config/config.toml"
  $OLD config set app minimum-gas-prices 0.025usnode --home "$T/v$i" >/dev/null
  $OLD config set client chain-id $CHAIN --home "$T/v$i" >/dev/null
  $OLD config set client keyring-backend test --home "$T/v$i" >/dev/null
  $OLD config set client node "$(rpc "$i")" --home "$T/v$i" >/dev/null
  DAEMON_NAME=smartnodesd DAEMON_HOME="$T/v$i" cosmovisor init "$OLD" >/dev/null 2>&1
  start "$i"
done
for _ in $(seq 1 60); do [ "$(height 1)" -ge 5 ] && break; sleep 1; done
check "the network makes blocks on $OLD_VERSION (height $(height 1))" "[ \$(height 1) -ge 5 ]"

step "2. Some state to carry through the upgrade"
TX="--home $T/v1 --gas auto --gas-adjustment 1.6 --gas-prices 0.025usnode --yes -o json"
PUBKEY=$(head -c 32 /dev/urandom | base64)
$OLD tx registry register-node 203.0.113.7:51820 "$PUBKEY" 1 1000000000usnode --from val $TX > "$T/reg.json" 2>/dev/null; sleep 3
# A passive account: it only receives, so its balance must not change.
$OLD keys add watcher $KR --home "$T/v1" >/dev/null 2>&1
WATCHER=$($OLD keys show watcher -a $KR --home "$T/v1")
$OLD tx bank send val "$WATCHER" 123456789usnode --from val $TX > "$T/send.json" 2>/dev/null; sleep 3
NODES_BEFORE=$($OLD query registry list-node --home "$T/v1" -o json | jq -c '[.node[] | {endpoint, pubkey}]')
BAL_BEFORE=$($OLD query bank balances "$WATCHER" --home "$T/v1" -o json | jq -c .balances)
echo "  watcher holds: $BAL_BEFORE"
echo "  nodes: $NODES_BEFORE"
check "a VPN node is registered" "echo '$NODES_BEFORE' | grep -q 203.0.113.7"

step "3. A proposal schedules $UPGRADE; the validators vote yes"
H=$(( $(height 1) + 45 ))
$OLD tx upgrade software-upgrade $UPGRADE --upgrade-height $H --title "Upgrade to $UPGRADE" --summary "Rehearsal." \
  --deposit 10000000usnode --no-validate --from val $TX > "$T/proposal.json" 2>&1; sleep 3
PID=$($OLD query gov proposals --home "$T/v1" -o json 2>/dev/null | jq -r '[.proposals[] | (.id // "0") | tonumber] | max')
echo "  proposal $PID: upgrade at height $H"
check "the upgrade proposal was submitted" "[ -n \"$PID\" ] && [ \"$PID\" != null ]"
for i in 1 2 3 4; do
  $OLD tx gov vote "$PID" yes --from val --home "$T/v$i" --gas auto --gas-adjustment 1.6 --gas-prices 0.025usnode --yes > /dev/null 2>&1
  # Each validator puts the new version where Cosmovisor looks for it.
  mkdir -p "$T/v$i/cosmovisor/upgrades/$UPGRADE/bin" && cp "$NEW" "$T/v$i/cosmovisor/upgrades/$UPGRADE/bin/"
done
for _ in $(seq 1 40); do
  S=$($OLD query gov proposal "$PID" --home "$T/v1" -o json 2>/dev/null | jq -r '.proposal.status')
  [ "$S" = PROPOSAL_STATUS_PASSED ] && break; sleep 1
done
check "the proposal passed" "[ \"$S\" = PROPOSAL_STATUS_PASSED ]"

step "4. At height $H the chain switches to $UPGRADE and carries on"
for _ in $(seq 1 120); do [ "$(height 1)" -ge $((H + 5)) ] && break; sleep 1; done
echo "  height now $(height 1) (upgrade at $H)"
check "the chain carried on past the upgrade height" "[ \$(height 1) -ge $((H + 5)) ]"
for i in 1 2 3 4; do
  check "validator $i runs $UPGRADE" "[ \"\$(readlink -f $T/v$i/cosmovisor/current)\" = \"\$(readlink -f $T/v$i/cosmovisor/upgrades/$UPGRADE)\" ] && [ \$(height $i) -ge $((H + 3)) ]"
done
APPLIED=$($NEW query upgrade applied $UPGRADE --home "$T/v1" -o json 2>/dev/null | jq -r '.header.height // .height // empty')
check "the chain records $UPGRADE as applied (at ${APPLIED:-?})" "[ \"$APPLIED\" = $H ]"
check "the VPN node is still registered" "[ \"\$($NEW query registry list-node --home $T/v1 -o json | jq -c '[.node[] | {endpoint, pubkey}]')\" = '$NODES_BEFORE' ]"
check "balances are unchanged" "[ \"\$($NEW query bank balances $WATCHER --home $T/v1 -o json | jq -c .balances)\" = '$BAL_BEFORE' ]"
VALS=$($NEW query staking validators --home "$T/v1" -o json 2>/dev/null | jq '[.validators[] | select(.status == "BOND_STATUS_BONDED")] | length')
check "all 4 validators are still bonded" "[ \"$VALS\" = 4 ]"
$NEW tx bank send val "$WATCHER" 1000usnode --from val $TX > "$T/after.json" 2>"$T/after.err"; sleep 4
grep -v '^gas estimate' "$T/after.err" | head -3
AFTER=$($NEW query bank balances "$WATCHER" --home "$T/v1" -o json | jq -r '.balances[] | select(.denom=="usnode") | .amount')
check "new transactions work after the upgrade (watcher now $AFTER)" "[ \"$AFTER\" = 123457789 ]"

echo
echo "=================================================================="
echo "Upgrade rehearsal: $PASS passed, $FAIL failed."
