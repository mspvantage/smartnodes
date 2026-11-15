#!/usr/bin/env bash
# Live trial of the SmartNodes verify module (stage 1, report-only) on a
# local chain.
#
# Run this in a second terminal while "ignite30 cosmos chain serve --reset-once"
# is running in the first, on a FRESH chain. It uses the throwaway test
# accounts alice and bob as probers. It does not stop on errors: several
# steps are SUPPOSED to be rejected. It takes a few minutes, because it has
# to wait for three verification rounds.

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

ALICE=$($BIN keys show alice -a $KR)
BOB=$($BIN keys show bob -a $KR)
VERIFY=$($BIN query auth module-account verify -o json | first_address)
echo "alice (node operator and prober): $ALICE"
echo "bob   (prober):                   $BOB"
echo "verify module account:            $VERIFY"
[ -n "$VERIFY" ] || { echo "could not read the verify module address; is the chain running with the new code?"; exit 1; }

ROUND_BLOCKS=$($BIN query verify params -o json | jq -r '.params.round_blocks')
echo "round length: $ROUND_BLOCKS blocks; report-only: $($BIN query verify params -o json | jq -r '.params.report_only')"
if [ "$ROUND_BLOCKS" -gt 100 ]; then
  echo "The round is too long for a trial. Add the short test round to config.yml (see the instructions) and restart the chain with --reset-once."
  exit 1
fi

step "1. Alice registers a VPN node"
$BIN tx registry register-node "vpn1.example.com:51820" "$(head -c 32 /dev/urandom | base64)" 1 ${BOND}usnode $AS_ALICE | ok
sleep $WAIT
NODE=$($BIN query registry list-node -o json | jq -r '.node[-1].id // "0"')
echo "node id: $NODE"

step "2. Alice and Bob each post a 1,000 SNODE bond to become probers"
BOB_BEFORE=$(usnode "$BOB")
$BIN tx verify register-prober $BOND $AS_ALICE | ok
$BIN tx verify register-prober $BOND $AS_BOB | ok
sleep $WAIT
A_ID=$(prober_field "$ALICE" id); B_ID=$(prober_field "$BOB" id)
echo "alice is prober $A_ID, status $(status_name "$(prober_field "$ALICE" status)")"
echo "bob   is prober $B_ID, status $(status_name "$(prober_field "$BOB" status)")"
echo "bob paid:               $((BOB_BEFORE - $(usnode "$BOB"))) usnode   (want $BOND plus a small fee)"
echo "verify account balance: $(usnode "$VERIFY") usnode   (want $((BOND * 2)))"

step "2b. A bond below the minimum (should be REJECTED)"
$BIN tx verify register-prober 5 $AS_ALICE 2>&1 | tail -2
step "2c. A second prober for the same address (should be REJECTED)"
$BIN tx verify register-prober $BOND $AS_BOB 2>&1 | tail -2
step "2d. Sending coins straight to the verify account (should be REJECTED: the account is blocked)"
$BIN tx bank send bob "$VERIFY" 1000usnode $COMMON 2>&1 | tail -2
step "2e. Sealing a report before joining the active set (should be REJECTED)"
R=$(round)
$BIN tx verify commit-reports "$R" "$NODE" "$(seal "$R" "$NODE" "$B_ID" 1 3 00112233445566778899aabbccddeeff)" $AS_BOB 2>&1 | tail -2

step "3. A new round starts and both probers join the active set"
wait_new_round
R1=$(round)
echo "alice status: $(status_name "$(prober_field "$ALICE" status)")   (want 2)"
echo "bob status:   $(status_name "$(prober_field "$BOB" status)")   (want 2)"
$BIN query verify round
echo "assignment for node $NODE:"
$BIN query verify assignment "$NODE"

step "4. Both probers seal a PASS report for node $NODE in round $R1"
SALT_A=$(head -c 16 /dev/urandom | od -An -tx1 | tr -d " \n")
SALT_B=$(head -c 16 /dev/urandom | od -An -tx1 | tr -d " \n")
HASH_A=$(seal "$R1" "$NODE" "$A_ID" 1 3 "$SALT_A")
HASH_B=$(seal "$R1" "$NODE" "$B_ID" 1 3 "$SALT_B")
echo "alice's sealed report: $HASH_A"
echo "bob's sealed report:   $HASH_B"
$BIN tx verify commit-reports "$R1" "$NODE" "$HASH_A" $AS_ALICE | ok
$BIN tx verify commit-reports "$R1" "$NODE" "$HASH_B" $AS_BOB | ok
sleep $WAIT

step "4b. Bob seals a second, different report for the same node (should be REJECTED)"
$BIN tx verify commit-reports "$R1" "$NODE" "$(seal "$R1" "$NODE" "$B_ID" 2 0 "$SALT_B")" $AS_BOB 2>&1 | tail -2
step "4c. Alice opens her report in the same round (should be REJECTED: too early)"
$BIN tx verify reveal-report "$R1" "$NODE" 1 3 "$SALT_A" $AS_ALICE 2>&1 | tail -2
step "4d. A sealed report for a round that is not the current one (should be REJECTED)"
$BIN tx verify commit-reports "$((R1 + 5))" "$NODE" "$HASH_A" $AS_ALICE 2>&1 | tail -2

step "5. The next round starts: reports from round $R1 can now be opened"
wait_new_round

step "5a. Alice tries to change her answer to FAIL (should be REJECTED: does not match what she sealed)"
$BIN tx verify reveal-report "$R1" "$NODE" 2 3 "$SALT_A" $AS_ALICE 2>&1 | tail -2
step "5b. Bob tries to open Alice's report as his own (should be REJECTED)"
$BIN tx verify reveal-report "$R1" "$NODE" 1 3 "$SALT_A" $AS_BOB 2>&1 | tail -2

step "5c. Both open their reports honestly"
$BIN tx verify reveal-report "$R1" "$NODE" 1 3 "$SALT_A" $AS_ALICE | ok
$BIN tx verify reveal-report "$R1" "$NODE" 1 3 "$SALT_B" $AS_BOB | ok
sleep $WAIT
step "5d. Alice opens her report a second time (should be REJECTED)"
$BIN tx verify reveal-report "$R1" "$NODE" 1 3 "$SALT_A" $AS_ALICE 2>&1 | tail -2

echo
echo "node record before the tally (want: not found):"
$BIN query verify node-record "$NODE" 2>&1 | tail -1

step "6. The round after that starts: round $R1 is tallied"
wait_new_round
sleep $WAIT
echo "node record (want verified: true, passes: 1, last_pass_round: $R1):"
$BIN query verify node-record "$NODE"
echo "alice accepted reports: $(prober_field "$ALICE" accepted)   (want 1)"
echo "bob accepted reports:   $(prober_field "$BOB" accepted)   (want 1)"

step "7. Jailing is not built yet: unjail (should be REJECTED)"
$BIN tx verify unjail "$NODE" $AS_ALICE 2>&1 | tail -2

step "8. Bob asks to leave"
$BIN tx verify deregister-prober $AS_BOB | ok
sleep $WAIT
echo "bob status: $(status_name "$(prober_field "$BOB" status)")   (want 3: still active until the round ends)"
wait_new_round
echo "bob status: $(status_name "$(prober_field "$BOB" status)")   (want 4)"
echo "active probers: $($BIN query verify round -o json | jq -r '.active_probers // "0"')   (want 1)"

step "8b. Bob withdraws his bond before the 21 days are over (should be REJECTED)"
$BIN tx verify withdraw-prober-bond $AS_BOB 2>&1 | tail -2
echo
echo "verify account balance: $(usnode "$VERIFY") usnode   (want $((BOND * 2)): both bonds still locked)"

echo
echo "Trial finished. Copy everything above and send it back."
