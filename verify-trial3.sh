#!/usr/bin/env bash
# Live trial of the SmartNodes node rewards pool on a local chain: the pool
# is funded, a node passes verification, and its operator claims the reward.
#
# Run this in a second terminal while "ignite30 cosmos chain serve --reset-once"
# is running in the first, on a FRESH chain. It uses the throwaway test
# accounts alice and bob as probers. It does not stop on errors: several
# steps are SUPPOSED to be rejected. It takes about four minutes.

BIN=smartnodesd
CHAIN=smartnodes
KR="--keyring-backend test"
COMMON="$KR --chain-id $CHAIN --gas auto --gas-adjustment 1.6 --gas-prices 0.025usnode --yes"
AS_ALICE="--from alice $COMMON"
AS_BOB="--from bob $COMMON"
WAIT=6
BOND=1000000000   # 1,000 SNODE

step() { echo; echo "=================================================================="; echo "$1"; echo "=================================================================="; }
ok() { grep -E '^(code|raw_log|txhash)'; }
first_address() { jq -r '.. | .address? // empty' | head -1; }
usnode() { $BIN query bank balances "$1" -o json | jq -r '.balances[] | select(.denom=="usnode") | .amount'; }
round() { $BIN query verify round -o json | jq -r '.round // "0"'; }
height() { $BIN status 2>/dev/null | jq -r '.sync_info.latest_block_height // .SyncInfo.latest_block_height'; }
prober_field() { $BIN query verify list-prober -o json | jq -r --arg o "$1" --arg f "$2" '.prober[] | select(.owner==$o) | .[$f] // "0"'; }
status_name() { case "$1" in 1) echo "1 (waiting to join)";; 2) echo "2 (active)";; 3) echo "3 (leaving)";; 4) echo "4 (unbonding)";; *) echo "$1";; esac; }

# wait_new_round blocks until the round number changes, then one more block.
wait_new_round() {
  local cur; cur=$(round)
  echo "   waiting for round $((cur + 1)) to start (now round $cur, block $(height))..."
  while [ "$(round)" = "$cur" ]; do sleep 2; done
  sleep 2
  echo "   round $(round) started at block $(height)"
}

# seal ROUND NODE PROBER RESULT SPEED SALTHEX: the sealed form of a report,
# computed the same way the chain does.
seal() {
  python3 - "$CHAIN" "$@" <<'PY'
import hashlib, struct, sys
chain, rnd, node, prober, result, speed, salt = sys.argv[1], *map(int, sys.argv[2:7]), bytes.fromhex(sys.argv[7])
be = lambda v: struct.pack(">Q", v)
h = hashlib.sha256()
h.update(b"SMARTNODES-VERIFY-COMMIT-V1")
h.update(be(len(chain))); h.update(chain.encode())
for v in (rnd, node, prober, result, speed): h.update(be(v))
h.update(be(len(salt))); h.update(salt)
print(h.hexdigest())
PY
}

command -v jq >/dev/null || { echo "jq not found: sudo apt install -y jq"; exit 1; }

pool() { $BIN query verify reward-pool -o json; }
POOL_FUND=5000000000   # 5,000 SNODE
CAP=1901               # most an entry node can earn per pass with a 40-block round

ALICE=$($BIN keys show alice -a $KR)
BOB=$($BIN keys show bob -a $KR)
ROUND_BLOCKS=$($BIN query verify params -o json | jq -r '.params.round_blocks')
if [ "$ROUND_BLOCKS" != "40" ]; then
  echo "This trial expects a 40-block round (config.yml sets it for the test chain). Found: $ROUND_BLOCKS"
  exit 1
fi
if [ "$($BIN query registry list-node -o json | jq -r '.node | length')" != "0" ]; then
  echo "This chain already has nodes. Restart it with: ignite30 cosmos chain serve --reset-once"
  exit 1
fi
POOL_ADDR=$(pool | jq -r '.address')
echo "alice (node operator and prober): $ALICE"
echo "bob   (prober):                   $BOB"
echo "node rewards pool:                $POOL_ADDR"

step "1. Alice registers a VPN node; Alice and Bob become probers"
$BIN tx registry register-node "vpn0.example.com:51820" "$(head -c 32 /dev/urandom | base64)" 1 ${BOND}usnode $AS_ALICE | ok
sleep $WAIT
$BIN tx verify register-prober $BOND $AS_ALICE | ok
$BIN tx verify register-prober $BOND $AS_BOB | ok
sleep $WAIT
A_ID=$(prober_field "$ALICE" id); B_ID=$(prober_field "$BOB" id)

step "2. The pool starts empty"
pool | jq -c '{balance: (.balance // "0"), owed: (.owed // "0"), round_budget: (.round_budget // "0")}'

step "2a. A direct transfer to the pool account (should be REJECTED: the account is blocked)"
$BIN tx bank send alice "$POOL_ADDR" 1000usnode $COMMON 2>&1 | tail -2
step "2b. Funding the pool with zero (should be REJECTED)"
$BIN tx verify fund-node-rewards 0 $AS_ALICE 2>&1 | tail -2

step "3. Alice funds the pool with 5,000 SNODE through the funding transaction"
$BIN tx verify fund-node-rewards $POOL_FUND $AS_ALICE | ok
sleep $WAIT
echo "pool (want balance $POOL_FUND):"
pool | jq -c '{balance: (.balance // "0"), owed: (.owed // "0"), round_budget: (.round_budget // "0")}'

step "4. A new round starts: it gets a budget, and the prober fund gets its share"
wait_new_round
R=$(round)
echo "pool (want a round_budget of about 5,070, and a balance about 1,267 lower: the prober fund's share):"
pool | jq -c '{balance: (.balance // "0"), owed: (.owed // "0"), round_budget: (.round_budget // "0")}'

step "5. Round $R: both probers seal PASS for node 0"
SA=$(head -c 16 /dev/urandom | od -An -tx1 | tr -d " \n")
SB=$(head -c 16 /dev/urandom | od -An -tx1 | tr -d " \n")
$BIN tx verify commit-reports "$R" 0 "$(seal "$R" 0 "$A_ID" 1 3 "$SA")" $AS_ALICE | ok
$BIN tx verify commit-reports "$R" 0 "$(seal "$R" 0 "$B_ID" 1 3 "$SB")" $AS_BOB | ok
sleep $WAIT

step "5a. Claiming before anything is earned (should be REJECTED)"
$BIN tx verify claim-node-reward 0 $AS_ALICE 2>&1 | tail -2

step "6. Next round: the reports are opened"
wait_new_round
$BIN tx verify reveal-report "$R" 0 1 3 "$SA" $AS_ALICE | ok
$BIN tx verify reveal-report "$R" 0 1 3 "$SB" $AS_BOB | ok
sleep $WAIT

step "7. Round after: the tally marks node 0 as an earner of round $R"
wait_new_round
sleep $WAIT
echo "node record (want verified: true, pending_round: $R, pending_weight: $CAP, no reward_balance yet):"
$BIN query verify node-record 0

step "7a. Bob, who does not run node 0, tries to claim its reward (should be REJECTED)"
$BIN tx verify claim-node-reward 0 $AS_BOB 2>&1 | tail -2

step "8. Alice claims"
BEFORE=$(pool | jq -r '.balance // "0"')
$BIN tx verify claim-node-reward 0 $AS_ALICE | ok
sleep $WAIT
AFTER=$(pool | jq -r '.balance // "0"')
echo "node record (want reward_paid: $CAP, no pending_round, no reward_balance):"
$BIN query verify node-record 0
echo "the claim paid $CAP usnode: the round's budget was larger, but the cap per pass applies."
echo "pool balance fell by $((BEFORE - AFTER)) usnode over the claim   (want $CAP, plus about 1,267 if a new round started meanwhile)"
echo "pool now:"
pool | jq -c '{balance: (.balance // "0"), owed: (.owed // "0"), round_budget: (.round_budget // "0")}'

step "8a. Claiming a second time (should be REJECTED: nothing left to claim)"
$BIN tx verify claim-node-reward 0 $AS_ALICE 2>&1 | tail -2

step "9. Both probers had their report accepted"
echo "accepted reports: alice $(prober_field "$ALICE" accepted), bob $(prober_field "$BOB" accepted)   (want 1 each)"

echo
echo "Trial finished. Copy everything above and send it back."
